package engine

import (
	"context"
	"database/sql"
	"errors"
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
func (d *Duck) Stalled() bool {
	if since := d.publishing.Load(); since != 0 && time.Since(time.Unix(0, since)) > parquetSwapBudget+overrunGrace {
		return true
	}
	d.statements.mu.Lock()
	defer d.statements.mu.Unlock()
	for _, due := range d.statements.due {
		if time.Since(due) > overrunGrace {
			return true
		}
	}
	return false
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
