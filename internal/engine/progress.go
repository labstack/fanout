package engine

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/labstack/fanout/internal/metrics"
)

// ErrStalled reports a stall in work Fanout owns: a Parquet publication
// running far past its budget, or an idle engine that does not answer a ping.
// Only a restart frees it.
var ErrStalled = errors.New("query engine stalled")

// overrunGrace is how long a read may run past its deadline before it is
// reported as an overrun: as long again as the longest read may run. DuckDB
// checks for interrupts only between chunks, so a costly expression can keep
// a statement running past its deadline. That costs the statement's own
// connection, not the engine, so it never fails liveness.
const overrunGrace = longestRead

// longestRead bounds the deadline of every read Fanout issues; panel batches
// take 20 s and the detector's statements 15 s.
const longestRead = 25 * time.Second

// idleLimit is how long the read pool may go without a statement returning
// before liveness confirms the engine with a ping: three of the longest reads.
const idleLimit = 3 * longestRead

// publishStall is how long a Parquet publication may run before the engine
// counts as stalled; ingest commits wait for its publish gate meanwhile. The swap's renames and fsyncs do not honour
// its context, so on slow network storage it can run well past its budget;
// restarting mid-swap would not make the storage faster. Ten minutes is
// longer than an NFS hard mount's default major timeout (timeo=600,
// retrans=2: three minutes), so only a publication that storage has stopped
// serving counts.
const publishStall = 10 * time.Minute

// statements tracks the read statements in flight.
type statements struct {
	mu   sync.Mutex
	next uint64
	runs map[uint64]*statement
}

type statement struct {
	// due is the deadline, or zero for a read without one.
	due time.Time
	// query is hashed only if it overruns, so logs never carry its text.
	query    string
	reported bool
}

// fingerprint is a short hash of a query, for logs that must not carry its
// text. It is computed only for an overrun, off the read path.
func fingerprint(query string) string {
	sum := sha256.Sum256([]byte(query))
	return hex.EncodeToString(sum[:6])
}

// begin tracks a read statement on ctx until the returned func reports how it
// ended.
func (d *Duck) begin(ctx context.Context, query string) func(error) {
	due, _ := ctx.Deadline()
	s := &d.statements
	s.mu.Lock()
	if s.runs == nil {
		s.runs = map[uint64]*statement{}
	}
	id := s.next
	s.next++
	s.runs[id] = &statement{due: due, query: query}
	s.mu.Unlock()
	var once sync.Once
	return func(err error) {
		once.Do(func() {
			s.mu.Lock()
			delete(s.runs, id)
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
	return len(d.statements.runs)
}

// held reports whether reads, or connections other than pings', hold the
// read pool. A ping that fails then may only have waited for them. A
// connection a panic abandoned may never return, so it is not counted.
func (d *Duck) held() bool {
	return d.inFlight() > 0 || int64(d.DB.Stats().InUse)-d.abandoned.Load() > d.pings.Load()
}

// noteProgress records that the engine answered.
func (d *Duck) noteProgress() { d.progress.Store(time.Now().UnixNano()) }

// Overruns counts the reads still running more than overrunGrace past their
// deadlines. They make readiness degraded, never liveness: a costly query
// holds its own connection while the engine serves the rest.
func (d *Duck) Overruns() int {
	d.statements.mu.Lock()
	defer d.statements.mu.Unlock()
	n := 0
	for _, run := range d.statements.runs {
		if overran(run) {
			n++
		}
	}
	return n
}

func overran(run *statement) bool {
	return !run.due.IsZero() && time.Since(run.due) > overrunGrace
}

// reportOverruns logs and counts each read the first time it is seen overrunning.
func (d *Duck) reportOverruns() {
	d.statements.mu.Lock()
	defer d.statements.mu.Unlock()
	for _, run := range d.statements.runs {
		if run.reported || !overran(run) {
			continue
		}
		run.reported = true
		metrics.DuckDBStatementOverruns.Inc()
		slog.Warn("query running past its deadline", "query", fingerprint(run.query), "overrun", time.Since(run.due).Round(time.Second))
	}
}

// Stalled reports whether a Parquet publication has run for publishStall:
// storage has stopped serving its rename or fsync.
func (d *Duck) Stalled() bool { return d.stall() != "" }

// stall names what stalls the engine, or is empty; see Stalled.
func (d *Duck) stall() string {
	if since := d.publishing.Load(); since != 0 && time.Since(time.Unix(0, since)) > publishStall {
		return "a Parquet publication ran past its bound"
	}
	return ""
}

// Live reports whether the engine can still serve. A stall fails it. A pool
// where statements returned recently is alive. Otherwise a ping confirms it,
// and a ping that fails while reads hold the pool may only have waited for
// them, not found the engine hung, so that is alive too.
func (d *Duck) Live(ctx context.Context) error {
	if d.Stalled() {
		return ErrStalled
	}
	return d.idle(ctx)
}

// idle pings an engine no statement has answered on for idleLimit, and
// reports the failure only while nothing holds the pool.
func (d *Duck) idle(ctx context.Context) error {
	if last := d.progress.Load(); last != 0 && time.Since(time.Unix(0, last)) <= idleLimit {
		return nil
	}
	if err := d.Ping(ctx); err != nil && !d.held() {
		return err
	}
	return nil
}

// Ping runs a trivial statement on the read pool, recording progress when it
// answers. It is not tracked as a statement in flight; the connection it
// holds is counted in pings.
func (d *Duck) Ping(ctx context.Context) error {
	conn, err := d.acquireRead(ctx)
	if err != nil {
		return err
	}
	d.pings.Add(1)
	defer func() { _ = conn.Close(); d.pings.Add(-1) }()
	var one int
	if err := conn.QueryRowContext(ctx, "SELECT 1").Scan(&one); err != nil {
		return err
	}
	d.noteProgress()
	return nil
}

// stallExit is how long a stall must last before Fanout exits for a restart:
// another overrunGrace, so supervisors that restart only on exit recover too.
const stallExit = overrunGrace

// pingWait bounds the watchdog's ping, which also counts as unanswered once
// it has been outstanding this long.
const pingWait = 5 * time.Second

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

// Watch reports read overruns, and delivers ErrStalled on Fatal once a stall
// in Fanout's own work has lasted stallExit, so the process exits and its
// supervisor restarts it. Overload and costly queries are no stall. It stops
// with ctx, so shutdown cannot fire it.
func (d *Duck) Watch(ctx context.Context) {
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	d.watch(ctx, ticker.C)
}

func (d *Duck) watch(ctx context.Context, ticks <-chan time.Time) {
	var w stallWatch
	// The ping runs apart from the ticks: an engine that hangs it must not
	// hang the watchdog.
	pings := make(chan string, 1)
	var pinged time.Time
	var pingFault string
	for {
		select {
		case <-ctx.Done():
			return
		case pingFault = <-pings:
			pinged = time.Time{}
		case now := <-ticks:
			d.reportOverruns()
			stall := d.stall()
			if stall == "" && !pinged.IsZero() && now.Sub(pinged) >= pingWait && !d.held() {
				stall = "the idle engine did not answer a ping"
			}
			if stall == "" {
				stall = pingFault
			}
			if w.observe(now, stall) {
				slog.Error("query engine stalled, exiting for a restart", "reason", stall, "after", stallExit)
				d.fatalOnce.Do(func() { d.fatal <- fmt.Errorf("%w: %s", ErrStalled, stall) })
				return
			}
			if pinged.IsZero() {
				pinged = now
				go func() {
					pingCtx, cancel := context.WithTimeout(ctx, pingWait)
					defer cancel()
					fault := ""
					if err := d.idle(pingCtx); err != nil {
						fault = "the idle engine did not answer a ping: " + err.Error()
					}
					pings <- fault
				}()
			}
		}
	}
}
