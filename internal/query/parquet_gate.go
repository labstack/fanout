package query

import (
	"context"
	"errors"
	"sync"
	"time"
)

// defaultWriterGrace bounds how long readers may keep entering ahead of a
// waiting publisher. Long enough that ordinary dashboard traffic never queues
// behind maintenance, short enough that retention and compaction always run.
const defaultWriterGrace = 30 * time.Second

// rollupReaderLease bounds only how long rebuildable rollup work waits to enter
// the Parquet snapshot. Once admitted, the bounded rollup chunk runs under its
// caller context so a slow but healthy pass can commit instead of retrying the
// same chunk forever.
var rollupReaderLease = defaultWriterGrace / 2

// ErrParquetReadWait distinguishes publication contention from a query error.
var ErrParquetReadWait = errors.New("wait for Parquet publication")

// parquetReadGate protects Parquet file publication without sync.RWMutex's
// unconditional writer preference. Queuing maintenance must not stall
// unrelated API reads or readiness probes, so readers continue to be admitted
// while a publisher waits — but only until that publisher's grace period
// expires, after which new readers queue so retention and compaction cannot be
// starved by overlapping query traffic.
type parquetReadGate struct {
	once        sync.Once
	mu          sync.Mutex
	changed     chan struct{}
	readers     int
	writer      bool
	waiting     []parquetWaiter
	nextWaiter  uint64
	writerGrace time.Duration
	now         func() time.Time
}

type parquetWaiter struct {
	id       uint64
	queuedAt time.Time
}

func (g *parquetReadGate) init() {
	g.once.Do(func() { g.changed = make(chan struct{}) })
}

func (g *parquetReadGate) notifyLocked() {
	close(g.changed)
	g.changed = make(chan struct{})
}

func (g *parquetReadGate) clock() time.Time {
	if g.now != nil {
		return g.now()
	}
	return time.Now()
}

func (g *parquetReadGate) grace() time.Duration {
	if g.writerGrace != 0 {
		return g.writerGrace
	}
	return defaultWriterGrace
}

// admitsReaderLocked reports whether a new reader may enter. A reader is
// refused once a publisher is active, or once the longest-waiting publisher
// has been queued for longer than the grace period.
func (g *parquetReadGate) admitsReaderLocked() bool {
	if g.writer {
		return false
	}
	if len(g.waiting) == 0 {
		return true
	}
	return g.clock().Sub(g.waiting[0].queuedAt) < g.grace()
}

func (g *parquetReadGate) TryRLock() bool {
	g.init()
	g.mu.Lock()
	defer g.mu.Unlock()
	if !g.admitsReaderLocked() {
		return false
	}
	g.readers++
	return true
}

func (g *parquetReadGate) RLock() {
	if err := g.RLockContext(context.Background()); err != nil {
		panic(err)
	}
}

func (g *parquetReadGate) RLockContext(ctx context.Context) error {
	g.init()
	g.mu.Lock()
	for !g.admitsReaderLocked() {
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-changed:
		}
		g.mu.Lock()
	}
	g.readers++
	g.mu.Unlock()
	return nil
}

func (g *parquetReadGate) RUnlock() {
	g.init()
	g.mu.Lock()
	g.readers--
	if g.readers < 0 {
		g.mu.Unlock()
		panic("query: parquetReadGate RUnlock without RLock")
	}
	if g.readers == 0 {
		g.notifyLocked()
	}
	g.mu.Unlock()
}

func (g *parquetReadGate) Lock() {
	if err := g.LockContext(context.Background()); err != nil {
		panic(err)
	}
}

func (g *parquetReadGate) LockContext(ctx context.Context) error {
	g.init()
	g.mu.Lock()
	g.nextWaiter++
	waiter := parquetWaiter{id: g.nextWaiter, queuedAt: g.clock()}
	g.waiting = append(g.waiting, waiter)
	// Wake any readers parked on an earlier publisher so they re-evaluate this
	// publisher's grace, and so the grace clock starts for readers immediately.
	g.notifyLocked()
	for g.writer || g.readers > 0 {
		changed := g.changed
		g.mu.Unlock()
		select {
		case <-ctx.Done():
			g.mu.Lock()
			for i, queued := range g.waiting {
				if queued.id == waiter.id {
					g.waiting = append(g.waiting[:i], g.waiting[i+1:]...)
					break
				}
			}
			g.notifyLocked()
			g.mu.Unlock()
			return ctx.Err()
		case <-changed:
		}
		g.mu.Lock()
	}
	for i, queued := range g.waiting {
		if queued.id == waiter.id {
			g.waiting = append(g.waiting[:i], g.waiting[i+1:]...)
			break
		}
	}
	g.writer = true
	g.mu.Unlock()
	return nil
}

func (g *parquetReadGate) Unlock() {
	g.init()
	g.mu.Lock()
	if !g.writer {
		g.mu.Unlock()
		panic("query: parquetReadGate Unlock without Lock")
	}
	g.writer = false
	g.notifyLocked()
	g.mu.Unlock()
}
