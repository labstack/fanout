package query

import (
	"sync"
	"time"
)

// defaultWriterGrace bounds how long readers may keep entering ahead of a
// waiting publisher. Long enough that ordinary dashboard traffic never queues
// behind maintenance, short enough that retention and compaction always run.
const defaultWriterGrace = 5 * time.Second

// parquetReadGate protects Parquet file publication without sync.RWMutex's
// unconditional writer preference. Queuing maintenance must not stall
// unrelated API reads or readiness probes, so readers continue to be admitted
// while a publisher waits — but only until that publisher's grace period
// expires, after which new readers queue so retention and compaction cannot be
// starved by overlapping query traffic.
type parquetReadGate struct {
	once        sync.Once
	mu          sync.Mutex
	changed     *sync.Cond
	readers     int
	writer      bool
	waiting     []time.Time
	writerGrace time.Duration
	now         func() time.Time
}

func (g *parquetReadGate) init() {
	g.once.Do(func() { g.changed = sync.NewCond(&g.mu) })
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
	return g.clock().Sub(g.waiting[0]) < g.grace()
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
	g.init()
	g.mu.Lock()
	for !g.admitsReaderLocked() {
		g.changed.Wait()
	}
	g.readers++
	g.mu.Unlock()
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
		g.changed.Broadcast()
	}
	g.mu.Unlock()
}

func (g *parquetReadGate) Lock() {
	g.init()
	g.mu.Lock()
	queued := g.clock()
	g.waiting = append(g.waiting, queued)
	// Wake any readers parked on an earlier publisher so they re-evaluate this
	// publisher's grace, and so the grace clock starts for readers immediately.
	g.changed.Broadcast()
	for g.writer || g.readers > 0 {
		g.changed.Wait()
	}
	for i, at := range g.waiting {
		if at.Equal(queued) {
			g.waiting = append(g.waiting[:i], g.waiting[i+1:]...)
			break
		}
	}
	g.writer = true
	g.mu.Unlock()
}

func (g *parquetReadGate) Unlock() {
	g.init()
	g.mu.Lock()
	if !g.writer {
		g.mu.Unlock()
		panic("query: parquetReadGate Unlock without Lock")
	}
	g.writer = false
	g.changed.Broadcast()
	g.mu.Unlock()
}

// WaitingWriters reports how many publishers are queued behind active readers.
func (g *parquetReadGate) WaitingWriters() int {
	g.init()
	g.mu.Lock()
	defer g.mu.Unlock()
	return len(g.waiting)
}
