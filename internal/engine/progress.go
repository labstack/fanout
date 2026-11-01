package engine

import (
	"context"
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

// begin tracks a read statement until the returned func reports that it
// returned. Any return counts as progress, success, error or cancellation
// alike: each shows the engine is still answering.
func (d *Duck) begin(deadline time.Time) func() {
	s := &d.statements
	s.mu.Lock()
	if s.due == nil {
		s.due = map[uint64]time.Time{}
	}
	id := s.next
	s.next++
	s.due[id] = deadline
	s.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			s.mu.Lock()
			delete(s.due, id)
			s.mu.Unlock()
			d.noteProgress()
		})
	}
}

// deadline is ctx's deadline, or the longest a read may run when it has none.
func deadline(ctx context.Context) time.Time {
	if at, ok := ctx.Deadline(); ok {
		return at
	}
	return time.Now().Add(maxQueryTimeoutMs * time.Millisecond)
}

func (d *Duck) inFlight() int {
	d.statements.mu.Lock()
	defer d.statements.mu.Unlock()
	return len(d.statements.due)
}

// noteProgress records that a read statement returned.
func (d *Duck) noteProgress() { d.progress.Store(time.Now().UnixNano()) }

// Stalled reports whether a read statement is still running more than
// overrunGrace past its deadline.
func (d *Duck) Stalled() bool {
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
