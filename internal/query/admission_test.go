package query

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/observability"
)

type pooledEngine struct {
	Engine
	pool int
}

func (p pooledEngine) ReadConnections() int { return p.pool }

// Panels leave two read connections to annotations, trace detail, exemplars
// and readiness once the pool can spare them.
func TestPanelSlotsReserveTwoConnections(t *testing.T) {
	for pool, want := range map[int]int{1: 1, 2: 2, 3: 3, 4: 2, 8: 6, 16: 14} {
		slots, reserved, queue := config.QueryAdmission(pool)
		if slots != want || queue != 8*want || reserved != min(want-1, max(1, want/6)) {
			t.Errorf("pool %d: slots=%d queue=%d, want %d and %d", pool, slots, queue, want, 8*want)
		}
	}
	e := NewExecutor(pooledEngine{pool: 8}, 30)
	defer e.Close()
	for range 5 {
		if err := e.admission.acquire(t.Context(), panelClass); err != nil {
			t.Fatal(err)
		}
	}
	if e.admission.tryShared() {
		t.Error("panels took reserved slot")
	}

	if e.admission.bounds[panelClass] != 48 {
		t.Errorf("queue bound %d, want 48", e.admission.bounds[panelClass])
	}
}

// Past the queue bound a panel is refused at once instead of waiting out the
// batch deadline, and a request whose every panel is refused is ErrBusy.
func TestPanelsAreRefusedAtTheQueueBound(t *testing.T) {
	e := newFixtureExecutor(t)
	defer e.Close()
	e.admission = newReadAdmission(1, 0, 8)
	e.admission.bounds[panelClass] = 1
	e.admission.bounds[interactiveClass] = 1
	if err := e.admission.acquire(t.Context(), panelClass); err != nil {
		t.Fatal(err)
	}
	queued := make(chan []Result, 1)
	go func() {
		results, _ := e.Run(t.Context(), RunRequest{Dashboard: cacheDashboard()})
		queued <- results
	}()
	deadline := time.Now().Add(5 * time.Second)
	for e.admission.waiting() < 1 {
		if time.Now().After(deadline) {
			t.Fatal("the first panel never queued")
		}
		time.Sleep(time.Millisecond)
	}
	other := cacheDashboard()
	other.Panels[0].ID = "other"
	other.Panels = append(other.Panels, Panel{ID: "third", Title: "Third", Viz: "table", Query: &Spec{From: "spans", Measures: []string{"count()"}}})
	started := time.Now()
	_, err := e.Run(t.Context(), RunRequest{Dashboard: other})
	if !errors.Is(err, ErrBusy) || time.Since(started) > time.Second {
		t.Fatalf("err=%v after %s", err, time.Since(started))
	}
	// A request with something to show keeps its refused panels as results.
	with := other
	with.Panels = append(append([]Panel(nil), other.Panels...), Panel{ID: "note", Title: "Note", Viz: "text", Content: "hi"})
	results, err := e.Run(t.Context(), RunRequest{Dashboard: with})
	if err != nil || len(results) != 3 {
		t.Fatalf("results=%+v err=%v", results, err)
	}
	for _, r := range results[:2] {
		if r.Status != StatusError || r.Reason != ReasonBusy || !r.Retryable || r.Error != QueueFullError {
			t.Errorf("refused panel=%+v", r)
		}
	}
	e.admission.release(panelClass)
	if r := <-queued; len(r) != 1 || r[0].Status != StatusOK {
		t.Fatalf("queued panel=%+v", r)
	}
	if PresentError(ErrBusy, ReadError) != BusyMessage {
		t.Error("busy is presented as a generic read failure")
	}
}

// Exemplars and trace detail wait in the same line as panels: past the
// queue bound, or past the batch deadline, they are busy instead of holding
// the engine.
func TestExemplarsAndTracesShareAdmission(t *testing.T) {
	e := newFixtureExecutor(t)
	defer e.Close()
	e.SetCacheTTL(10 * time.Second)
	e.admission = newReadAdmission(1, 0, 8)
	e.admission.bounds[panelClass] = 0
	e.admission.bounds[interactiveClass] = 0
	if err := e.admission.acquire(t.Context(), panelClass); err != nil {
		t.Fatal(err)
	}
	req := ExemplarRequest{Dashboard: shopDashboard(), PanelID: "by_route", From: fixtureStart.Add(30 * time.Minute), To: fixtureStart.Add(31 * time.Minute), Dimensions: map[string]string{"attributes['http.route']": "/cart"}}
	if _, err := e.Exemplars(t.Context(), req); !errors.Is(err, ErrBusy) {
		t.Fatalf("exemplars err=%v", err)
	}
	reader := &fakeTraces{}
	traces := e.CachedTraces(reader)
	if _, err := traces.Trace(t.Context(), observability.Scope{}, "abc", "", 10); !errors.Is(err, ErrBusy) || reader.calls.Load() != 0 {
		t.Fatalf("trace err=%v reads=%d", err, reader.calls.Load())
	}
	// Queued, a read waits no longer than the batch deadline.
	e.admission.bounds[panelClass] = 1
	e.admission.bounds[interactiveClass] = 1
	e.batchTimeout = 30 * time.Millisecond
	if _, err := traces.Trace(t.Context(), observability.Scope{}, "def", "", 10); !errors.Is(err, ErrBusy) {
		t.Fatalf("queued trace err=%v", err)
	}
	if got := e.InteractiveRefusals(); got != 3 {
		t.Fatalf("interactive refusals=%d want 3", got)
	}
	if len(e.cache.entries) != 0 {
		t.Fatal("a busy answer was stored")
	}
	e.admission.release(panelClass)
	if out, err := e.Exemplars(t.Context(), req); err != nil || len(out.Traces) != 1 {
		t.Fatalf("admitted exemplars=%+v err=%v", out, err)
	}
	if !e.admission.tryShared() {
		t.Fatal("an exemplar run kept its slot")
	}
}

func TestExecutorPressureTracksWaitersAndCapacity(t *testing.T) {
	e := NewExecutor(pooledEngine{pool: 2}, 30)
	defer e.Close()
	if e.Pressure() {
		t.Fatal("idle executor under pressure")
	}
	if err := e.admission.acquire(t.Context(), panelClass); err != nil {
		t.Fatal(err)
	}
	if !e.Pressure() {
		t.Fatal("full shared executor reported no pressure")
	}
	e.admission.release(panelClass)
	if e.Pressure() {
		t.Fatal("pressure did not clear")
	}
}

func TestInteractiveReadsAreAdmittedAheadOfPanels(t *testing.T) {
	a := newReadAdmission(6, 1, 48)
	for range 5 {
		if err := a.acquire(t.Context(), panelClass); err != nil {
			t.Fatal(err)
		}
	}
	panels := make(chan struct{}, 48)
	for range 48 {
		go func() {
			if err := a.acquire(t.Context(), panelClass); err == nil {
				panels <- struct{}{}
			}
		}()
	}
	waitForReadWaiters(t, a, 48)
	if err := a.acquire(t.Context(), interactiveClass); err != nil {
		t.Fatal(err)
	}
	interactive := make(chan struct{})
	go func() {
		if err := a.acquire(t.Context(), interactiveClass); err == nil {
			close(interactive)
		}
	}()
	waitForReadWaiters(t, a, 49)
	a.release(panelClass)
	select {
	case <-interactive:
	case <-time.After(time.Second):
		t.Fatal("interactive read queued behind panels")
	}
	select {
	case <-panels:
		t.Fatal("panel woke before interactive waiter")
	default:
	}
	a.release(interactiveClass)
	a.release(interactiveClass)
	for range 4 {
		a.release(panelClass)
	}
	for range 48 {
		<-panels
		a.release(panelClass)
	}
}

func waitForReadWaiters(t *testing.T, a *readAdmission, n int) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for a.waiting() != n {
		if time.Now().After(deadline) {
			t.Fatalf("waiters=%d want=%d", a.waiting(), n)
		}
		time.Sleep(time.Millisecond)
	}
}

func TestPanelsNeverTakeTheReservedSlot(t *testing.T) {
	a := newReadAdmission(6, 1, 0)
	for range 5 {
		if err := a.acquire(t.Context(), panelClass); err != nil {
			t.Fatal(err)
		}
	}
	if err := a.acquire(t.Context(), panelClass); !errors.Is(err, errQueueFull) {
		t.Fatal(err)
	}
	if err := a.acquire(t.Context(), interactiveClass); err != nil {
		t.Fatal(err)
	}
}

func TestInteractiveQueueBoundRefusesFloods(t *testing.T) {
	a := newReadAdmission(2, 1, 16)
	for range 2 {
		if err := a.acquire(t.Context(), interactiveClass); err != nil {
			t.Fatal(err)
		}
	}
	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan error, 4)
	for range 4 {
		go func() { done <- a.acquire(ctx, interactiveClass) }()
	}
	waitForReadWaiters(t, a, 4)
	if err := a.acquire(t.Context(), interactiveClass); !errors.Is(err, errQueueFull) {
		t.Fatal(err)
	}
	cancel()
	for range 4 {
		if err := <-done; !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	}
	if a.waiting() != 0 {
		t.Fatal("canceled waiters leaked")
	}
	for range 2 {
		a.release(interactiveClass)
	}
	if !a.tryShared() {
		t.Fatal("cancellation leaked capacity")
	}
}

func TestInteractiveRefusalsIncludeTheExemplarWaitDeadline(t *testing.T) {
	e := newFixtureExecutor(t)
	defer e.Close()
	e.admission = newReadAdmission(1, 0, 8)
	if err := e.admission.acquire(t.Context(), panelClass); err != nil {
		t.Fatal(err)
	}
	defer e.admission.release(panelClass)
	ctx, cancel := context.WithTimeout(t.Context(), time.Millisecond)
	defer cancel()
	if _, err := e.admit(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatal(err)
	}
	if e.InteractiveRefusals() != 1 {
		t.Fatal("caller-bounded interactive wait was not counted")
	}
}

// A refresh is one queue entry even when both workers are waiting. Once
// admitted, all 15 panels can run without another trip through the bounded FIFO.
func TestRefreshAdmissionIsAtomicAndLeavesCapacityForOtherReaders(t *testing.T) {
	a := newReadAdmission(6, 1, 1)
	for range 5 {
		if err := a.acquire(t.Context(), panelClass); err != nil {
			t.Fatal(err)
		}
	}
	b := newPanelBatchAdmission(a, 2)
	done := make(chan func(), 2)
	for range 2 {
		go func() {
			release, err := b.acquire(t.Context())
			if err != nil {
				t.Error(err)
				done <- nil
				return
			}
			done <- release
		}()
	}
	waitForReadWaiters(t, a, 1)
	// One batch fills one queue entry, so another batch is refused as a unit.
	other := newPanelBatchAdmission(a, 2)
	for range 2 {
		if _, err := other.acquire(t.Context()); !errors.Is(err, errQueueFull) {
			t.Fatalf("other batch: %v", err)
		}
	}
	other.close()
	a.releaseN(panelClass, 2)
	for range 2 {
		release := <-done
		if release == nil {
			t.Fatal("sibling refused")
		}
		release()
	}
	if err := a.acquire(t.Context(), interactiveClass); err != nil {
		t.Fatal(err)
	}
	a.release(interactiveClass)
	for range 15 {
		release, err := b.acquire(t.Context())
		if err != nil {
			t.Fatal(err)
		}
		release()
	}
	b.close()
	a.releaseN(panelClass, 3)
	if !a.tryShared() || a.batches != 0 {
		t.Fatal("batch leaked its reservation")
	}
}

func TestRefreshAdmissionKeepsDetachedFlightsAndReleasesCanceledWaiters(t *testing.T) {
	a := newReadAdmission(6, 1, 1)
	b := newPanelBatchAdmission(a, 2)
	release, err := b.acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	b.close()
	if a.active[panelClass] != 2 {
		t.Fatal("detached flight lost its slots")
	}
	release()
	if a.active[panelClass] != 0 {
		t.Fatal("detached flight leaked slots")
	}
	// A stale cache revalidation may start after the request closes its batch.
	release, err = b.acquire(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	if a.active[panelClass] != 1 {
		t.Fatal("late revalidation did not use single-slot admission")
	}
	release()
	for range 5 {
		if err := a.acquire(t.Context(), panelClass); err != nil {
			t.Fatal(err)
		}
	}
	c := newPanelBatchAdmission(a, 2)
	ctx, cancel := context.WithCancel(t.Context())
	failed := make(chan error, 1)
	go func() { _, err := c.acquire(ctx); failed <- err }()
	waitForReadWaiters(t, a, 1)
	cancel()
	if err := <-failed; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	c.close()
	a.releaseN(panelClass, 5)
	if !a.tryShared() || a.batches != 0 {
		t.Fatal("canceled batch leaked slots or queue")
	}
}

func TestRefreshPressureAccountsForItsWholeSlotBudget(t *testing.T) {
	a := newReadAdmission(6, 1, 48)
	if err := a.acquireN(t.Context(), panelClass, 4); err != nil {
		t.Fatal(err)
	}
	if a.tryShared() {
		t.Fatal("an unusable single-slot fragment hid dashboard pressure")
	}
	if err := a.acquire(t.Context(), interactiveClass); err != nil {
		t.Fatal(err)
	}
	a.release(interactiveClass)
	a.releaseN(panelClass, 4)
	if !a.tryShared() {
		t.Fatal("pressure did not clear")
	}
}
