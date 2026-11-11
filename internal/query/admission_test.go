package query

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	"github.com/labstack/pitot/internal/config"
	"github.com/labstack/pitot/internal/observability"
	"github.com/labstack/pitot/internal/queryrows"
)

type pooledEngine struct {
	Engine
	pool int
}

func (p pooledEngine) ReadConnections() int { return p.pool }

// Panels leave two read connections to annotations, trace detail, exemplars
// and readiness once the pool can spare them.
func TestPanelSlotsReserveTwoConnections(t *testing.T) {
	for pool, want := range map[int]struct{ slots, reserved, queue int }{
		1: {1, 0, 4}, 2: {2, 1, 4}, 3: {3, 1, 9}, 4: {2, 1, 4}, 8: {6, 1, 8}, 10: {8, 1, 7}, 12: {10, 1, 6}, 16: {14, 2, 4},
	} {
		slots, reserved, queue := config.QueryAdmission(pool)
		if slots != want.slots || reserved != want.reserved || queue != want.queue {
			t.Errorf("pool %d: slots=%d reserved=%d queue=%d, want %+v", pool, slots, reserved, queue, want)
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

	if e.admission.bounds[panelClass] != 8 {
		t.Errorf("queue bound %d, want 8", e.admission.bounds[panelClass])
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
	// Even a text panel cannot turn a refused refresh into partial success.
	with := other
	with.Panels = append(append([]Panel(nil), other.Panels...), Panel{ID: "note", Title: "Note", Viz: "text", Content: "hi"})
	results, err := e.Run(t.Context(), RunRequest{Dashboard: with})
	if !errors.Is(err, ErrBusy) || len(results) != 0 {
		t.Errorf("results=%+v err=%v", results, err)
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

// A refresh has one admission ticket even when both execution workers wait.
// Admitted siblings never re-enter bounded admission.
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
	waitForReadWaiters(t, a, 2)
	// One queued refresh fills the ticket bound; another is refused as a unit.
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
	if a.active[panelClass] != 1 {
		t.Fatal("detached flight lost its slot")
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

func TestRefreshPressureUsesEveryAvailablePanelSlot(t *testing.T) {
	a := newReadAdmission(6, 1, 48)
	if err := a.acquireN(t.Context(), panelClass, 4); err != nil {
		t.Fatal(err)
	}
	if !a.tryShared() {
		t.Fatal("an available panel slot was stranded by a refresh budget")
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

func TestRefusedRefreshEmitsNeitherCachedNorTextPanels(t *testing.T) {
	e := newFixtureExecutor(t)
	defer e.Close()
	e.SetCacheTTL(time.Minute)
	d := cacheDashboard()
	uncached := d.Panels[0]
	uncached.ID = "uncached"
	d.Panels = append(d.Panels, uncached, Panel{ID: "note", Title: "Note", Viz: "text", Content: "hi"})
	if _, err := e.Run(t.Context(), RunRequest{Dashboard: d, Panels: []string{d.Panels[0].ID}}); err != nil {
		t.Fatal(err)
	}
	e.admission = newReadAdmission(1, 0, 0)
	if err := e.admission.acquire(t.Context(), panelClass); err != nil {
		t.Fatal(err)
	}
	defer e.admission.release(panelClass)
	sent := 0
	err := e.Stream(t.Context(), RunRequest{Dashboard: d}, func(Result) { sent++ })
	if !errors.Is(err, ErrBusy) || sent != 0 {
		t.Fatalf("err=%v emitted=%d", err, sent)
	}
	// Fresh and still-servable stale cache snapshots add no execution work.
	clock := e.now
	for _, age := range []time.Duration{0, 45 * time.Second} {
		e.now = func() time.Time { return clock().Add(age) }
		sent = 0
		err = e.Stream(t.Context(), RunRequest{Dashboard: d, Panels: []string{d.Panels[0].ID, "note"}}, func(Result) { sent++ })
		if err != nil || sent != 2 {
			t.Fatalf("cached refresh age=%s: err=%v emitted=%d", age, err, sent)
		}
		e.cache.mu.Lock()
		flights := len(e.cache.flights)
		e.cache.mu.Unlock()
		if flights != 0 {
			t.Fatal("overloaded cache snapshot launched a revalidation")
		}
	}
	sent = 0
	err = e.Stream(WithoutCachedResults(t.Context()), RunRequest{Dashboard: d, Panels: []string{d.Panels[0].ID, "note"}}, func(Result) { sent++ })
	if !errors.Is(err, ErrBusy) || sent != 0 {
		t.Fatalf("forced fresh refresh: err=%v emitted=%d", err, sent)
	}

}

func TestRefreshLeaseIncludesRevalidationsScheduledAfterResponse(t *testing.T) {
	a := newReadAdmission(3, 1, 4)
	b := newPanelBatchAdmission(a, 2)
	if err := b.admit(t.Context()); err != nil {
		t.Fatal(err)
	}
	releaseFlight := b.retainFlight()
	b.close()
	ctx := context.WithValue(t.Context(), panelBatchKey{}, b)
	release, err := b.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	release()
	if a.active[panelClass] != 0 || a.batches != 1 {
		t.Fatal("pending flight must keep its ticket without retaining an idle connection")
	}
	releaseFlight()
	if a.active[panelClass] != 0 || a.batches != 0 {
		t.Fatal("late flight leaked lease")
	}
}

func TestRefreshYieldsSlotsWhileAwaitingAnotherBatchFlight(t *testing.T) {
	a := newReadAdmission(3, 1, 4)
	first := newPanelBatchAdmission(a, 2)
	if err := first.admit(t.Context()); err != nil {
		t.Fatal(err)
	}
	firstFlight := first.retainFlight()
	ctx := context.WithValue(t.Context(), panelBatchKey{}, first)
	release, err := first.acquire(ctx)
	if err != nil {
		t.Fatal(err)
	}
	release()
	firstFlight()
	// The response can still be waiting for another batch's coalesced flight.
	// It must yield its now idle lease so that other flight can get connections.
	if a.active[panelClass] != 0 {
		t.Fatal("coalesced waiter retained idle slots")
	}
	second := newPanelBatchAdmission(a, 2)
	if err := second.admit(t.Context()); err != nil {
		t.Fatal(err)
	}
	otherCtx := context.WithValue(t.Context(), panelBatchKey{}, second)
	otherRelease, err := second.acquire(otherCtx)
	if err != nil {
		t.Fatal(err)
	}
	otherRelease()
	second.close()
	first.close()
	if a.active[panelClass] != 0 || a.batches != 0 {
		t.Fatal("refresh leaked capacity")
	}
}

type refreshGateEngine struct {
	Engine
	entered, release chan struct{}
	once             sync.Once
}

func (g *refreshGateEngine) QueryContext(ctx context.Context, query string, args ...any) (queryrows.Rows, error) {
	g.once.Do(func() {
		close(g.entered)
		select {
		case <-g.release:
		case <-ctx.Done():
		}
	})
	return g.Engine.QueryContext(ctx, query, args...)
}

func TestFullRefreshCoalescesWithoutAnotherExecutionTicket(t *testing.T) {
	e := newFixtureExecutor(t)
	defer e.Close()
	e.admission = newReadAdmission(1, 0, 0)
	e.parallel = 1
	gate := &refreshGateEngine{Engine: e.engine, entered: make(chan struct{}), release: make(chan struct{})}
	e.engine = gate
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	d := cacheDashboard()
	for i := 1; i < 15; i++ {
		p := d.Panels[0]
		p.ID = fmt.Sprint("panel_", i)
		d.Panels = append(d.Panels, p)
	}
	type answer struct {
		results []Result
		err     error
	}
	first := make(chan answer, 1)
	go func() { r, err := e.Run(ctx, RunRequest{Dashboard: d}); first <- answer{r, err} }()
	select {
	case <-gate.entered:
	case a := <-first:
		t.Fatalf("first refresh returned before querying: %v", a.err)
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	deadline := time.Now().Add(time.Second)
	for {
		e.cache.mu.Lock()
		n := len(e.cache.flights)
		e.cache.mu.Unlock()
		if n == 15 {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("whole refresh was not registered for coalescing")
		}
		time.Sleep(time.Millisecond)
	}
	second := make(chan answer, 1)
	go func() { r, err := e.Run(ctx, RunRequest{Dashboard: d}); second <- answer{r, err} }()
	deadline = time.Now().Add(time.Second)
	for {
		e.cache.mu.Lock()
		joined := true
		for _, f := range e.cache.flights {
			joined = joined && f.waiters == 2
		}
		e.cache.mu.Unlock()
		if joined {
			break
		}
		select {
		case a := <-second:
			t.Fatalf("coalesced refresh refused: %v", a.err)
		default:
		}
		if time.Now().After(deadline) {
			t.Fatal("coalesced refresh never joined")
		}
		time.Sleep(time.Millisecond)
	}
	close(gate.release)
	for _, done := range []chan answer{first, second} {
		a := <-done
		if a.err != nil || len(a.results) != 15 {
			t.Fatalf("results=%d err=%v", len(a.results), a.err)
		}
		for _, r := range a.results {
			if r.Status != StatusOK {
				t.Fatalf("panel=%+v", r)
			}
		}
	}
}

func TestRefreshUsesTheLastAvailableGlobalPanelSlot(t *testing.T) {
	a := newReadAdmission(6, 1, 4)
	if err := a.acquireN(t.Context(), panelClass, 4); err != nil {
		t.Fatal(err)
	}
	b := newPanelBatchAdmission(a, 2)
	if err := b.admit(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	release, err := b.acquire(ctx)
	if err != nil {
		t.Fatalf("last global slot was stranded: %v", err)
	}
	if a.active[panelClass] != 5 {
		t.Fatalf("active panels=%d", a.active[panelClass])
	}
	if err := a.acquire(ctx, interactiveClass); err != nil {
		t.Fatal(err)
	}
	a.release(interactiveClass)
	release()
	b.close()
	a.releaseN(panelClass, 4)
	if a.batchActive != 0 || a.batches != 0 || !a.tryShared() {
		t.Fatal("refresh leaked capacity")
	}
}

func TestInteractiveBorrowingRetainsPanelProgressAndLendsIdleSlots(t *testing.T) {
	a := newReadAdmission(6, 1, 4)
	for range 6 {
		if err := a.acquire(t.Context(), interactiveClass); err != nil {
			t.Fatal(err)
		}
	}
	b := newPanelBatchAdmission(a, 2)
	if err := b.admit(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	panels := make(chan func(), 2)
	for range 2 {
		go func() {
			release, err := b.acquire(ctx)
			if err != nil {
				t.Error(err)
			}
			panels <- release
		}()
	}
	waitForReadWaiters(t, a, 2)
	interactive := make(chan error, 1)
	go func() { interactive <- a.acquire(ctx, interactiveClass) }()
	waitForReadWaiters(t, a, 3)
	a.release(interactiveClass)
	first := <-panels
	if first == nil {
		t.Fatal("interactive borrowing stranded panels")
	}
	a.release(interactiveClass)
	if err := <-interactive; err != nil {
		t.Fatal(err)
	}
	select {
	case <-panels:
		t.Fatal("sibling displaced interactive read")
	default:
	}
	first()
	second := <-panels
	if second == nil {
		t.Fatal("panel did not retain progress")
	}
	second()
	b.close()
	for range 5 {
		a.release(interactiveClass)
	}
	if !a.tryShared() || a.batches != 0 || a.batchActive != 0 {
		t.Fatal("reservation leaked capacity")
	}
}

// A running panel already supplies progress. Its sibling must not prevent a
// queued drill-down from borrowing the next released connection.
func TestPanelProgressDoesNotStarveInteractiveReplacement(t *testing.T) {
	a := newReadAdmission(3, 1, 4)
	if err := a.acquireN(t.Context(), panelClass, 2); err != nil {
		t.Fatal(err)
	}
	if err := a.acquire(t.Context(), interactiveClass); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), time.Second)
	defer cancel()
	interactive := make(chan error, 1)
	panel := make(chan error, 1)
	go func() { interactive <- a.acquire(ctx, interactiveClass) }()
	go func() { panel <- a.acquire(ctx, panelClass) }()
	waitForReadWaiters(t, a, 2)
	a.release(panelClass)
	select {
	case err := <-interactive:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(100 * time.Millisecond):
		t.Fatal("drill-down starved behind panel progress reservation")
	}
	select {
	case <-panel:
		t.Fatal("panel displaced interactive read while another panel was running")
	default:
	}
	a.release(panelClass)
	if err := <-panel; err != nil {
		t.Fatal(err)
	}
	a.release(panelClass)
	a.releaseN(interactiveClass, 2)
}
