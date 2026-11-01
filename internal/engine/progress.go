package engine

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"
)

// ErrStalled reports a read statement running long past its deadline: the
// engine has stopped honouring cancellation, and only a restart frees it.
var ErrStalled = errors.New("query engine stopped honouring statement deadlines")

// overrunGrace is how long a statement may run past its deadline before the
// engine counts as stalled: as long again as the longest read may run. An
// overloaded engine returns cancelled statements at their deadlines; a wedged
// one never returns them.
const overrunGrace = maxQueryTimeoutMs * time.Millisecond

// idleLimit is how long the read pool may go without a statement returning
// before liveness confirms the engine with a ping: three of the longest reads.
const idleLimit = 3 * maxQueryTimeoutMs * time.Millisecond

// statements tracks the read statements in flight by their deadlines.
type statements struct {
	mu   sync.Mutex
	next uint64
	due  map[uint64]time.Time
}

// begin tracks a read statement on ctx until the returned func reports how it
// ended. A read without a deadline has none to overrun, so it is not tracked.
func (d *Duck) begin(ctx context.Context) func(error) {
	due, ok := ctx.Deadline()
	if !ok {
		return func(err error) { d.answered(ctx, err) }
	}
	s := &d.statements
	s.mu.Lock()
	if s.due == nil {
		s.due = map[uint64]time.Time{}
	}
	id := s.next
	s.next++
	s.due[id] = due
	s.mu.Unlock()
	var once sync.Once
	return func(err error) {
		once.Do(func() {
			s.mu.Lock()
			delete(s.due, id)
			s.mu.Unlock()
			d.answered(ctx, err)
		})
	}
}

// answered records progress for a statement that shows the engine runs: one
// that succeeded, found no row, or stopped when its context ended. Other
// errors may come from a broken engine that fails everything at once.
func (d *Duck) answered(ctx context.Context, err error) {
	if err == nil || errors.Is(err, sql.ErrNoRows) || ctx.Err() != nil {
		d.noteProgress()
	}
}

func (d *Duck) inFlight() int {
	d.statements.mu.Lock()
	defer d.statements.mu.Unlock()
	return len(d.statements.due)
}

// noteProgress records that the engine answered.
func (d *Duck) noteProgress() { d.progress.Store(time.Now().UnixNano()) }

// Stalled reports whether a read statement is still running more than
// overrunGrace past its deadline, or a Parquet publication has held readers
// out that long past its swap budget: reads then fail before the engine.
func (d *Duck) Stalled() bool { return d.stall() != "" }

// stall names what stalls the engine, or is empty; see Stalled.
func (d *Duck) stall() string {
	if since := d.publishing.Load(); since != 0 && time.Since(time.Unix(0, since)) > parquetSwapBudget+overrunGrace {
		return "a Parquet publication held reads past its budget"
	}
	d.statements.mu.Lock()
	defer d.statements.mu.Unlock()
	for _, due := range d.statements.due {
		if time.Since(due) > overrunGrace {
			return "a read statement ran past its deadline"
		}
	}
	return ""
}

// Live reports whether the engine can still serve. A stall fails it. A pool
// where statements returned recently is alive. Otherwise a ping confirms it,
// and a ping that cannot get a connection while statements within their
// deadlines hold them all means overload, not a hang, so that is alive too.
func (d *Duck) Live(ctx context.Context) error {
	if d.Stalled() {
		return ErrStalled
	}
	if last := d.progress.Load(); last != 0 && time.Since(time.Unix(0, last)) <= idleLimit {
		return nil
	}
	if err := d.Ping(ctx); err != nil && d.inFlight() == 0 {
		return err
	}
	return nil
}

// Ping runs a trivial statement on the read pool, recording progress when it
// answers. It is not tracked as a statement in flight.
func (d *Duck) Ping(ctx context.Context) error {
	conn, err := d.acquireRead(ctx)
	if err != nil {
		return err
	}
	defer func() { _ = conn.Close() }()
	var one int
	if err := conn.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return err
	}
	d.noteProgress()
	return nil
}

// stallExit is how long the engine must stay stalled before Fanout exits for a
// restart: another overrunGrace, so a liveness probe acts first where there
// is one, and supervisors that restart only on exit still recover.
const stallExit = overrunGrace

// stallWatch fires once when a stall lasts stallExit without clearing.
type stallWatch struct {
	since time.Time
	fired bool
}

func (w *stallWatch) observe(now time.Time, stall string) bool {
	if w.fired {
		return false
	}
	if stall == "" {
		w.since = time.Time{}
		return false
	}
	if w.since.IsZero() {
		w.since = now
	}
	w.fired = now.Sub(w.since) >= stallExit
	return w.fired
}

// Watch delivers ErrStalled on Fatal once the engine has stayed stalled for
// stallExit, so the process exits and its supervisor restarts it. Overload
// is no stall. It stops with ctx, so shutdown cannot fire it.
func (d *Duck) Watch(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	d.watch(ctx, ticker.C)
}

func (d *Duck) watch(ctx context.Context, ticks <-chan time.Time) {
	var w stallWatch
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-ticks:
			stall := d.stall()
			if !w.observe(now, stall) {
				continue
			}
			slog.Error("query engine stalled, exiting for a restart", "reason", stall, "after", stallExit)
			d.fatalOnce.Do(func() { d.fatal <- fmt.Errorf("%w: %s", ErrStalled, stall) })
			return
		}
	}
}
