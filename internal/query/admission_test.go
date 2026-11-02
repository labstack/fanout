package query

import (
	"errors"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/observability"
	"golang.org/x/sync/semaphore"
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
		slots, queue := config.QueryAdmission(pool)
		if slots != want || queue != 8*want {
			t.Errorf("pool %d: slots=%d queue=%d, want %d and %d", pool, slots, queue, want, 8*want)
		}
	}
	e := NewExecutor(pooledEngine{pool: 8}, 30)
	defer e.Close()
	if !e.slots.TryAcquire(6) || e.slots.TryAcquire(1) {
		t.Error("a pool of 8 did not admit exactly 6 panels")
	}
	if e.queueBound != 48 {
		t.Errorf("queue bound %d, want 48", e.queueBound)
	}
}

// Past the queue bound a panel is refused at once instead of waiting out the
// batch deadline, and a request whose every panel is refused is ErrBusy.
func TestPanelsAreRefusedAtTheQueueBound(t *testing.T) {
	e := newFixtureExecutor(t)
	defer e.Close()
	e.slots = semaphore.NewWeighted(1)
	e.queueBound = 1
	if err := e.slots.Acquire(t.Context(), 1); err != nil {
		t.Fatal(err)
	}
	queued := make(chan []Result, 1)
	go func() {
		results, _ := e.Run(t.Context(), RunRequest{Dashboard: cacheDashboard()})
		queued <- results
	}()
	deadline := time.Now().Add(5 * time.Second)
	for e.waiting.Load() < 1 {
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
	e.slots.Release(1)
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
	e.slots = semaphore.NewWeighted(1)
	e.queueBound = 0
	if err := e.slots.Acquire(t.Context(), 1); err != nil {
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
	e.queueBound = 1
	e.batchTimeout = 30 * time.Millisecond
	if _, err := traces.Trace(t.Context(), observability.Scope{}, "def", "", 10); !errors.Is(err, ErrBusy) {
		t.Fatalf("queued trace err=%v", err)
	}
	if len(e.cache.entries) != 0 {
		t.Fatal("a busy answer was stored")
	}
	e.slots.Release(1)
	if out, err := e.Exemplars(t.Context(), req); err != nil || len(out.Traces) != 1 {
		t.Fatalf("admitted exemplars=%+v err=%v", out, err)
	}
	if !e.slots.TryAcquire(1) {
		t.Fatal("an exemplar run kept its slot")
	}
}
