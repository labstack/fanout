package query

import "sync"

// parquetReadGate protects Parquet file publication without sync.RWMutex's
// writer preference. Maintenance may wait behind a long reader, but merely
// queuing that maintenance must never block unrelated API reads or readiness
// probes. Once readers drain, the writer publishes while new readers wait.
type parquetReadGate struct {
	once           sync.Once
	mu             sync.Mutex
	changed        *sync.Cond
	readers        int
	writer         bool
	waitingWriters int
}

func (g *parquetReadGate) init() {
	g.once.Do(func() { g.changed = sync.NewCond(&g.mu) })
}

func (g *parquetReadGate) TryRLock() bool {
	g.init()
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.writer {
		return false
	}
	g.readers++
	return true
}

func (g *parquetReadGate) RLock() {
	g.init()
	g.mu.Lock()
	for g.writer {
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
	g.waitingWriters++
	for g.writer || g.readers > 0 {
		g.changed.Wait()
	}
	g.waitingWriters--
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
