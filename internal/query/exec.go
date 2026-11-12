package query

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/labstack/pitot/internal/config"
	"github.com/labstack/pitot/internal/engine"
	"github.com/labstack/pitot/internal/metrics"
	"github.com/labstack/pitot/internal/queryrows"
	"golang.org/x/sync/errgroup"
)

// Engine is the query engine the executor runs on. *engine.Duck implements it;
// QueryContext binds the Parquet snapshot for the window in the context.
type Engine interface {
	Parser
	QueryContext(ctx context.Context, query string, args ...any) (queryrows.Rows, error)
}

const (
	defaultPanelTimeout = 10 * time.Second
	defaultBatchTimeout = 12 * time.Second
	StatusOK            = "ok"
	StatusEmpty         = "empty"
	StatusError         = "error"
	StatusInvalid       = "invalid"
)

type RunRequest struct {
	Dashboard Dashboard        `json:"dashboard"`
	Panels    []string         `json:"panels,omitempty"`
	Time      *Time            `json:"time,omitempty"`
	Vars      map[string]Value `json:"vars,omitempty"`
	Widths    map[string]int   `json:"widths,omitempty"`
	Compare   *bool            `json:"compare,omitempty"`
}

type Result struct {
	Problems        Problems         `json:"problems,omitempty"`
	Ranking         *Ranking         `json:"ranking,omitempty"`
	AnnotationScope *AnnotationMatch `json:"annotation_scope,omitempty"`
	AnnotationError string           `json:"annotation_error,omitempty"`
	ID              string           `json:"id"`
	Status          string           `json:"status"`
	Frame           *Frame           `json:"frame,omitempty"`
	Previous        *Frame           `json:"previous,omitempty"`
	Error           string           `json:"error,omitempty"`
	// Reason classifies an error for clients that word it themselves:
	// "busy" means the panel never started (see BatchDeadlineError).
	Reason    string `json:"reason,omitempty"`
	Retryable bool   `json:"retryable,omitempty"`
	Diagnosis string `json:"diagnosis,omitempty"`
	SQL       string `json:"sql,omitempty"`
	Interval  string `json:"interval,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms"`
	FromMS    int64  `json:"from_ms"`
	ToMS      int64  `json:"to_ms"`
	FreshToMS int64  `json:"fresh_to_ms,omitempty"`
	Better    string `json:"better,omitempty"`
	// ShiftMS is how far Previous sits behind Frame, so a client can overlay it
	// without guessing from the first populated bucket.
	ShiftMS int64 `json:"shift_ms,omitempty"`
	// Cached marks a result served from the result cache rather than run for
	// this request; see PITOT_QUERY_CACHE_TTL.
	Cached bool `json:"cached,omitempty"`
	// partial marks a result missing part of what it would show: a sub-read
	// such as the previous period, annotations, table trends or the metric
	// unit failed, was skipped or ran out of the panel's time. It is
	// answered, but never cached.
	partial bool
}

type Executor struct {
	engine              Engine
	sqlPanels           map[string]sqlPanelPlan
	rollups             RollupReader
	maxWindow           time.Duration
	now                 func() time.Time
	timeout             time.Duration
	batchTimeout        time.Duration
	parallel            int
	admission           *readAdmission
	interactiveRefusals atomic.Uint64

	memo    checkMemo
	cache   resultCache
	mu      sync.Mutex
	checks  map[[32]byte]*Checked
	schemas map[string]schemaEntry
	units   map[string]unitEntry

	// Schema discoveries outlive their callers but not the executor: they
	// run under life, which Close cancels, two at a time through discoveries.
	life        context.Context
	stop        context.CancelFunc
	running     sync.WaitGroup
	discoveries chan struct{}
	// discoverWait bounds a discovery's wait for a slot; discoverRun bounds
	// its run once it holds one.
	discoverWait, discoverRun time.Duration
}

func NewExecutor(engine Engine, retentionDays int) *Executor {
	maxWindow := 30 * 24 * time.Hour
	if retentionDays > 0 {
		maxWindow = time.Duration(retentionDays) * 24 * time.Hour
	}
	// A panel admitted beyond the engine's read connections would only wait
	// for one inside the engine, with its own timeout already running. Two
	// connections stay free for reads that do not queue behind dashboards.
	slots, reserved, queue := config.QueryAdmission(6)
	if pool, ok := engine.(interface{ ReadConnections() int }); ok {
		slots, reserved, queue = config.QueryAdmission(pool.ReadConnections())
	}
	life, stop := context.WithCancel(context.Background())
	return &Executor{engine: engine, maxWindow: maxWindow, now: time.Now, timeout: defaultPanelTimeout, batchTimeout: defaultBatchTimeout, parallel: 4,
		admission: newReadAdmission(slots, reserved, queue), cache: newResultCache(), checks: map[[32]byte]*Checked{}, schemas: map[string]schemaEntry{},
		life: life, stop: stop, discoveries: make(chan struct{}, 2), discoverWait: time.Minute, discoverRun: 25 * time.Second}
}

// Close cancels the schema discoveries still running and waits for them to
// release their read connections.
func (e *Executor) Close() {
	e.mu.Lock()
	e.stop()
	e.mu.Unlock()
	e.running.Wait()
}

// Pressure reports queued reads or a fully occupied admission pool.
func (e *Executor) Pressure() bool {
	return !e.admission.tryShared()

}

// Validate normalizes d and runs every check, returning Problems or nil.
func (e *Executor) Validate(ctx context.Context, d *Dashboard) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	Normalize(d)
	_, err := e.check(ctx, d)
	return err
}

// Check runs Validate's database-backed checks: every filter expression and
// every SQL panel, through DuckDB's parser. It reports all problems it finds;
// an operational failure (cancelled request, expired deadline, closed
// database) is the error, never a Problem. The dashboard must already be
// normalized. Results are remembered per panel, so an edit re-checks only
// what changed.
func (e *Executor) Check(ctx context.Context, d *Dashboard) (*Checked, Problems, error) {
	return checkWith(ctx, e.engine, d, &e.memo)
}

// check memoizes Check by the spec's bytes: a dashboard refreshes the same
// spec every 30 seconds, and its filters do not need re-parsing each time.
func (e *Executor) check(ctx context.Context, d *Dashboard) (*Checked, error) {
	checked, _, err := e.checkKeyed(ctx, d)
	return checked, err
}

// checkKeyed is check that also returns the spec's hash, which keys its
// cached panel results.
func (e *Executor) checkKeyed(ctx context.Context, d *Dashboard) (*Checked, cacheKey, error) {
	if err := ctx.Err(); err != nil {
		return nil, cacheKey{}, err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, cacheKey{}, err
	}
	key := sha256.Sum256(raw)
	e.mu.Lock()
	cached, ok := e.checks[key]
	e.mu.Unlock()
	if ok {
		return cached, key, nil
	}
	checked, problems, err := e.Check(ctx, d)
	if err != nil {
		return nil, key, err
	}
	if len(problems) > 0 {
		return nil, key, problems
	}
	e.mu.Lock()
	if len(e.checks) >= 256 {
		clear(e.checks)
	}
	e.checks[key] = checked
	e.mu.Unlock()
	return checked, key, nil
}

// checkRun isolates panel diagnostics without weakening save validation. Each
// reduced spec is checked under its own hash; execution keeps the full spec hash.
func (e *Executor) checkRun(ctx context.Context, d *Dashboard) (*Checked, cacheKey, map[*Panel]Problems, error) {
	checked, spec, err := e.checkKeyed(ctx, d)
	invalid := map[*Panel]Problems{}
	var collected Problems
	reduced := *d
	indices := make([]int, len(d.Panels))
	for i := range indices {
		indices[i] = i
	}
	for err != nil {
		var problems Problems
		if !errors.As(err, &problems) {
			return nil, spec, nil, err
		}
		mapped := slices.Clone(problems)
		scoped := true
		owners := make([]*Panel, len(problems))
		for i, problem := range problems {
			rest, ok := strings.CutPrefix(problem.Path, "panels[")
			number, suffix, closed := strings.Cut(rest, "]")
			index, parseErr := strconv.Atoi(number)
			if !ok || !closed || parseErr != nil || index < 0 || index >= len(indices) || (suffix != "" && !strings.HasPrefix(suffix, ".")) {
				scoped = false
				continue
			}
			original := indices[index]
			owners[i] = &d.Panels[original]
			mapped[i].Path = fmt.Sprintf("panels[%d]%s", original, suffix)
		}
		if !scoped {
			return nil, spec, nil, append(mapped, collected...)
		}
		collected = append(collected, mapped...)
		for i, problem := range mapped {
			panel := owners[i]
			invalid[panel] = append(invalid[panel], problem)
		}
		reduced.Panels = nil
		remaining := make([]int, 0, len(indices))
		for _, original := range indices {
			if len(invalid[&d.Panels[original]]) == 0 {
				reduced.Panels = append(reduced.Panels, d.Panels[original])
				remaining = append(remaining, original)
			}
		}
		indices = remaining
		if len(indices) == 0 {
			// The full spec already passed all non-panel structural checks.
			var more Problems
			checked, more, err = checkValidated(ctx, e.engine, &reduced, &e.memo)
			if err == nil && len(more) > 0 {
				err = more
			}
		} else {
			checked, _, err = e.checkKeyed(ctx, &reduced)
		}
	}
	return checked, spec, invalid, nil
}

// Run validates the dashboard and runs the requested panels concurrently.
// Dashboard-level faults return Problems; invalid or failed panels report their
// failures in their own results, so one broken panel cannot blank a dashboard.
// Results are in panel order and share one response budget.
func (e *Executor) Run(ctx context.Context, req RunRequest) ([]Result, error) {
	var results []Result
	err := e.run(ctx, req, func(i int, r Result) {
		for len(results) <= i {
			results = append(results, Result{})
		}
		results[i] = r
	})
	if err != nil {
		return nil, err
	}
	limitBatchFrames(results)
	return results, nil
}

// Stream is Run with each result handed to emit as soon as its panel
// finishes, so a slow panel does not hold back the others. emit is never
// called concurrently, and only after the spec has checked out, so a caller
// can still answer Problems as a whole. Each result has its own response
// budget: the panel-order budget Run shares would need every result first.
func (e *Executor) Stream(ctx context.Context, req RunRequest, emit func(Result)) error {
	return e.run(ctx, req, func(_ int, r Result) {
		results := []Result{r}
		limitBatchFrames(results)
		emit(results[0])
	})
}

func (e *Executor) run(ctx context.Context, req RunRequest, emit func(int, Result)) error {
	caller := ctx
	// Relative windows end now; cached panel results are stamped with it.
	now := e.now()
	// Normalize fills defaults in place, and a shared run may outlive this
	// request: neither may write to or read from the caller's panels.
	d := req.Dashboard
	d.Panels = slices.Clone(d.Panels)
	for i := range d.Panels {
		if q := d.Panels[i].Query; q != nil {
			copied := *q
			d.Panels[i].Query = &copied
		}
	}
	Normalize(&d)
	if len(req.Panels) > len(d.Panels) {
		return Problems{{Path: "panels", Message: "panel selection cannot exceed the dashboard's panel count"}}
	}
	targets := make([]*Panel, 0, len(d.Panels))
	if len(req.Panels) == 0 {
		for i := range d.Panels {
			targets = append(targets, &d.Panels[i])
		}
	} else {
		seen := map[string]bool{}
		for _, id := range req.Panels {
			if seen[id] {
				continue
			}
			seen[id] = true
			index := slices.IndexFunc(d.Panels, func(p Panel) bool { return p.ID == id })
			if index < 0 {
				return Problems{{Path: "panels", Message: fmt.Sprintf("no panel has id %q", id)}}
			}
			targets = append(targets, &d.Panels[index])
		}
	}
	// The batch deadline bounds preparation and admission of the whole refresh.
	// Once admitted, every sibling keeps its full execution timeout.
	ctx, cancel := context.WithTimeout(ctx, e.batchTimeout)
	defer cancel()
	batchError := func(err error) error {
		if caller.Err() != nil {
			return caller.Err()
		}
		if ctx.Err() == context.DeadlineExceeded {
			return ErrBusy
		}
		return err
	}
	checked, spec, invalid, err := e.checkRun(ctx, &d)
	if err != nil {
		return batchError(err)
	}
	t := d.Time
	if req.Time != nil {
		t = *req.Time
		if t.Refresh == "" {
			t.Refresh = d.Time.Refresh
		}
		var problems Problems
		validateTime(t, &problems)
		if len(problems) > 0 {
			return problems
		}
	}
	start, end, err := resolveWindow(t, nil, now, e.maxWindow)
	if err != nil {
		return Problems{{Path: "time", Message: err.Error()}}
	}
	vars, err := e.values(ctx, &d, checked, start, end, req.Vars)
	if err != nil {
		return batchError(err)
	}
	compare := d.Time.Compare == "previous_period"
	if req.Compare != nil {
		compare = *req.Compare
	}
	invalidResult := func(p *Panel) Result {
		problems := PresentProblems(invalid[p])
		// Cards show repair guidance; structured diagnostics retain their paths.
		display := slices.Clone(problems)
		for i := range display {
			display[i].Path = ""
		}
		return Result{ID: p.ID, Status: StatusInvalid, Problems: problems, Error: display.Error()}
	}
	validCount := 0
	for _, p := range targets {
		if len(invalid[p]) == 0 {
			validCount++
		}
	}
	if validCount == 0 {
		for i, p := range targets {
			emit(i, invalidResult(p))
		}
		return caller.Err()
	}
	// Decide once before scheduling this batch's panels. A sibling waiting for
	// the same limited panel slots is normal dashboard concurrency, not evidence
	// of pre-existing pressure. Keep this policy even if the queue drains
	// before an admitted panel finishes; diagnosis uses its existing slot.
	deferDiagnosis := e.admission.waiting() > 0
	var group errgroup.Group
	batch := newPanelBatchAdmission(e.admission, min(e.parallel, 2, validCount))
	if err := batch.admit(ctx); err != nil {
		batch.close()
		if errors.Is(err, errQueueFull) {
			// An entirely cached/text refresh needs no execution ticket. Pin all
			// answers together so an eviction cannot create a partial refusal.
			var lookups []panelCacheLookup
			for _, p := range targets {
				if p.Viz == "text" || len(invalid[p]) > 0 {
					continue
				}
				key, keyErr := keyOf(panelCacheKey{Spec: spec, Panel: p.ID, Time: t, Vars: vars, Width: req.Widths[p.ID], Compare: compare})
				if keyErr != nil {
					return ErrBusy
				}
				lookups = append(lookups, panelCacheLookup{key: key, id: p.ID})
			}
			if results, available, snapshotErr := e.availablePanels(caller, lookups, now); available {
				if snapshotErr != nil {
					return snapshotErr
				}
				next := 0
				for i, p := range targets {
					if err := caller.Err(); err != nil {
						return err
					}
					if len(invalid[p]) > 0 {
						emit(i, invalidResult(p))
						continue
					}
					if p.Viz == "text" {
						emit(i, e.runPanel(caller, p, checked, t, now, vars, req.Widths[p.ID], compare))
						continue
					}
					emit(i, results[next])
					next++
				}
				return nil
			}
		}
		if errors.Is(err, errQueueFull) || errors.Is(err, context.DeadlineExceeded) {
			return ErrBusy
		}
		return err
	}
	// Workers finish and release the lease independently of a stalled stream.
	// The bounded buffer has one result per selected panel.
	type completedPanel struct {
		i int
		r Result
	}
	completed := make(chan completedPanel, len(targets))
	send := func(i int, r Result) { completed <- completedPanel{i, r} }
	go func() {
		defer close(completed)
		defer batch.close()
		// Register every selected panel's flight promptly. Execution is bounded
		// by the lease, while other refreshes can coalesce the complete batch.
		for i, p := range targets {
			if len(invalid[p]) > 0 {
				send(i, invalidResult(p))
				continue
			}
			group.Go(func() error {
				width := req.Widths[p.ID]
				if p.Viz == "text" {
					send(i, e.runPanel(caller, p, checked, t, now, vars, width, compare))
					return nil
				}
				if caller.Err() != nil {
					return nil
				}
				key, err := keyOf(panelCacheKey{Spec: spec, Panel: p.ID, Time: t, Vars: vars, Width: width, Compare: compare})
				if err != nil {
					send(i, failed(Result{ID: p.ID}, err, nil))
					return nil
				}
				out, outcome, err := loadCached(e, context.WithValue(caller, panelBatchKey{}, batch), key, now, func(run context.Context) (panelOutcome, int64, bool, error) {
					run = context.WithValue(run, deferDiagnosisKey{}, deferDiagnosis)
					// The refresh already owns its lease; siblings wait locally.
					admissionStarted := time.Now()
					release, admissionErr := batch.acquire(run)
					admissionStatus := "admitted"
					switch {
					case errors.Is(admissionErr, errQueueFull):
						admissionStatus = "rejected"
					case errors.Is(admissionErr, context.Canceled):
						admissionStatus = "canceled"
					case admissionErr != nil:
						admissionStatus = "timeout"
					}
					metrics.RecordPanelAdmission(p.ID, admissionStatus, time.Since(admissionStarted).Seconds())
					if admissionErr != nil {
						return panelOutcome{}, 0, false, admissionErr
					}
					r := func() Result {
						defer release()
						return e.runPanel(run, p, checked, t, now, vars, width, compare)
					}()
					return panelOutcome{Result: r}, r.size(), (r.Status == StatusOK || r.Status == StatusEmpty) && !r.partial, nil
				})
				switch {
				case errors.Is(err, errRunPanicked):
					send(i, failed(Result{ID: p.ID}, err, caller.Err()))
					return nil
				case err != nil:
					// The caller left; the shared run goes on for the others.
					if caller.Err() == nil {
						send(i, failed(Result{ID: p.ID}, err, caller.Err()))
					}
					return nil
				}
				r := out.Result.clone()
				r.Cached = outcome == cacheHit || outcome == cacheStale
				// The result is sent after the run released its slot: a stream
				// client that stops reading must not hold the engine from others.
				send(i, r)
				return nil
			})
		}
		_ = group.Wait()
	}()
	for r := range completed {
		emit(r.i, r.r)
	}
	if err := caller.Err(); err != nil {
		return err
	}
	return nil
}

func (e *Executor) acquireClass(ctx context.Context, class readClass) error {
	if err := e.admission.acquire(ctx, class); err != nil {
		return err
	}
	markStarted(ctx)
	return nil
}

// InteractiveRefusals counts interactive requests refused before execution.
func (e *Executor) InteractiveRefusals() uint64 { return e.interactiveRefusals.Load() }

var errQueueFull = errors.New("panel queue is full")

// admit takes a read slot for an exemplar or trace-detail run, which waits
// in a reserved, priority lane, so a flood of them is turned away
// as busy instead of holding the engine. The wait is bounded by the batch
// deadline; a full queue or a wait that outlasts it is ErrBusy.
func (e *Executor) admit(ctx context.Context) (release func(), err error) {
	wait, cancel := context.WithTimeout(ctx, e.batchTimeout)
	defer cancel()
	err = e.acquireClass(wait, interactiveClass)
	if errors.Is(err, errQueueFull) || errors.Is(err, context.DeadlineExceeded) {
		e.interactiveRefusals.Add(1)
	}
	switch {
	case err == nil:
		return func() { e.admission.release(interactiveClass) }, nil
	case ctx.Err() != nil:
		return nil, ctx.Err()
	default:
		return nil, ErrBusy
	}
}

// ErrBusy answers a request turned away before it started: every panel of a
// batch, an exemplar or a trace-detail read refused at the queue bound or
// left waiting past the batch deadline. HTTP answers it 503 with
// Retry-After.
var ErrBusy = errors.New("the server is busy: the read queue is full")

// BusyMessage presents ErrBusy to clients.
const BusyMessage = "The server is busy with other queries. Try again in a few seconds."

// BatchDeadlineError is shared by HTTP and MCP batch callers. It means the
// panel never started: other queries held the engine until a batch deadline
// passed, its own request's or, for a request that joined an identical run
// already waiting, the one that started that run, which may be sooner. A
// panel that started and ran too long reports TimeoutError.
const BatchDeadlineError = "Not run: other queries kept the server busy. Refresh to try again."

// ReasonBusy marks a result that was not run because of BatchDeadlineError.
const ReasonBusy = "busy"

// BatchTimeoutError describes the shared response budget.
const BatchTimeoutError = "Panels took longer than 12 seconds. Narrow the time range or reduce the number of panels."

// TimeoutError describes the executor's per-panel time limit.
const TimeoutError = "The query took longer than 10 seconds. Narrow the time range or add filters."

// Allocate the response budget in panel order, independently of completion
// order, so concurrent refreshes retain the same portion of their frames.
func limitBatchFrames(results []Result) {
	remaining := 200000
	for i := range results {
		for _, f := range []*Frame{results[i].Frame, results[i].Previous} {
			if f == nil {
				continue
			}
			if len(f.Totals) > remaining {
				f.Totals = nil
				f.Truncated = true
			}
			remaining -= len(f.Totals)
			rows := f.Rows
			if !f.bucketed {
				rows = min(rows, maxFrameRows)
			}
			if len(f.Columns) > 0 {
				rows = min(rows, remaining/len(f.Columns))
			}
			if rows < f.Rows {
				start := 0
				if f.bucketed {
					start = f.Rows - rows
					// Drop the entire oldest clipped bucket, keeping every series
					// in each retained bucket aligned with the newest data.
					for start > 0 && start < f.Rows && f.Values[0][start] == f.Values[0][start-1] {
						start++
					}
					rows = f.Rows - start
				}
				for j := range f.Values {
					f.Values[j] = f.Values[j][start : start+rows]
				}
				for name, series := range f.Trends {
					f.Trends[name] = series[start : start+rows]
				}
				f.Rows = rows
				f.Truncated = true
			}
			remaining -= rows * len(f.Columns)
			// Pattern trends are JSON arrays in a fixed row projection. Count
			// their points just like structured table trends, not as one cell.
			if f.Trend != nil {
				for column, c := range f.Columns {
					if c.Name != "trend" || c.Type != "json" {
						continue
					}
					for row, value := range f.Values[column] {
						var points []float64
						text, ok := value.(string)
						if !ok || json.Unmarshal([]byte(text), &points) != nil {
							continue
						}
						if len(points) > remaining {
							f.Values[column][row] = "[]"
							f.Truncated = true
							f.addNote("Some pattern trends were omitted to stay within the response budget.")
							continue
						}
						remaining -= len(points)
					}
				}
			}
			if f.Health != nil {
				points := min(len(f.Health.ErrorTrend), remaining/2)
				if points < len(f.Health.ErrorTrend) {
					f.Health.ErrorTrendTimes = f.Health.ErrorTrendTimes[len(f.Health.ErrorTrendTimes)-points:]
					f.Health.ErrorTrend = f.Health.ErrorTrend[len(f.Health.ErrorTrend)-points:]
					f.Truncated = true
					f.addNote("Health error trend was truncated to stay within the response budget.")
				}
				remaining -= 2 * points
			}
			names := make([]string, 0, len(f.Trends))
			for name := range f.Trends {
				names = append(names, name)
			}
			slices.Sort(names)
			for _, name := range names {
				for row, series := range f.Trends[name] {
					if len(series) > remaining {
						f.Trends[name][row] = nil
						f.Truncated = true
						f.addNote("Some row trends were omitted to stay within the response budget.")
						continue
					}
					remaining -= len(series)
				}
			}
		}
	}
}

func (e *Executor) runPanel(ctx context.Context, p *Panel, checked *Checked, t Time, now time.Time, vars map[string]Value, width int, compare bool) (res Result) {
	parent := ctx
	started := time.Now()
	res = Result{ID: p.ID, Status: StatusOK}
	if p.Better == "" {
		res.Better = inferBetter(p)
	}
	defer func() {
		elapsed := time.Since(started)
		res.ElapsedMS = elapsed.Milliseconds()
		metrics.RecordPanelQuery(res.ID, string(res.Status), elapsed.Seconds())
	}()
	if p.Viz == "text" {
		return res
	}
	start, end, err := resolveWindow(t, p.Time, now, e.maxWindow)
	if err != nil {
		return failed(res, err, parent.Err())
	}
	res.FromMS, res.ToMS = start.UnixMilli(), end.UnixMilli()
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	reduces := vizSpecs[p.Viz].reduces
	var interval time.Duration
	switch {
	case p.SQL != "" && p.Viz == "state_timeline":
		interval = time.Minute
	case reduces:
		interval = AutoInterval(end.Sub(start), 240)
	case p.Query != nil && p.Query.Bucket == "auto" && (p.Viz == "heatmap" || p.Viz == "state_timeline"):
		interval = autoCellInterval(end.Sub(start), width)
	case p.Query != nil:
		interval = bucketInterval(p.Query.Bucket, end.Sub(start), width)
	default:
		interval = AutoInterval(end.Sub(start), width)
	}
	if p.Query != nil && p.Query.From == "metrics" && (reduces || p.Query.Bucket == "auto") {
		interval = max(interval, time.Minute)
	}
	scope := Scope{Start: start, End: end, Interval: interval, Vars: vars}
	if p.Query != nil && checked != nil && rollupPlan(p, checked.Measures[p.ID], checked.Filters[p.ID], scope) {
		scope.Rollup = true
		scope.RollupSource = rollupSource(p)
		if interval > 0 {
			interval = max(interval, time.Minute)
			scope.Interval = interval
		}
		if reader, ok := e.engine.(interface {
			RollupWatermark(context.Context) (time.Time, error)
		}); ok {
			fresh, err := reader.RollupWatermark(ctx)
			if err != nil {
				return failed(res, err, parent.Err())
			}
			if !fresh.IsZero() {
				scope.FreshTo = fresh
				res.FreshToMS = fresh.UnixMilli()
			}
		}
	}
	ctx, coverage := queryrows.WithCoverage(ctx)
	defer func() {
		if fresh := coverage.FreshTo(); !fresh.IsZero() && (res.FreshToMS == 0 || fresh.UnixMilli() < res.FreshToMS) {
			res.FreshToMS = fresh.UnixMilli()
		}
	}()
	frame, sqlText, err := e.runScope(ctx, p, checked, scope)
	if err != nil {
		res.SQL = sqlText
		return failed(res, err, parent.Err())
	}
	res.Frame, res.SQL = frame, sqlText
	res.Ranking = frame.ranking
	// Trend failures are recorded on the frame and preserve the main table.
	if e.attachTableTrends(ctx, p, checked, scope, frame) != nil {
		res.partial = true
	}
	if p.Query != nil && (p.Viz == "timeseries" || p.Viz == "heatmap" || p.Viz == "state_timeline" || p.Viz == "health") {
		res.AnnotationScope, err = e.annotationScope(ctx, p, checked.AnnotationFilters[p.ID], scope)
		if err != nil {
			res.AnnotationError = "Annotation scope is unavailable."
			res.partial = true
		}
	}

	if interval > 0 && (reduces || p.Query == nil || p.Query.Bucket != "") {
		res.Interval = formatInterval(interval)
	}
	if frame.Trend != nil {
		res.Interval = formatInterval(time.Duration(frame.Trend.StepMS) * time.Millisecond)
	}
	// A panel without rows reads no totals, and an empty one no previous
	// range: those scans would cost the diagnosis its deadline, and nothing
	// shows them. A count of nothing is 0, and still compares.
	zero := zeroCount(p, checked.Measures[p.ID], frame)
	if zero && frame.Rows == 0 {
		// The stat's buckets are empty, so its total is the count's 0.
		frame.Totals = make([]any, len(frame.Columns))
		for i, c := range frame.Columns {
			if c.Role == "measure" {
				frame.Totals[i] = 0.0
			}
		}
	}
	if (p.Viz == "stat" || p.Viz == "gauge") && p.Query != nil && frame.Rows > 0 && frame.Totals == nil {
		totals, err := e.totals(ctx, p, checked, scope, frame)
		if err != nil {
			return failed(res, err, parent.Err())
		}
		frame.Totals = totals
	}
	if compare && (p.Viz == "stat" || p.Viz == "timeseries") && (frame.Rows > 0 || zero) {
		shift := end.Sub(start)
		previous := scope
		previous.Start, previous.End = start.Add(-shift), end.Add(-shift)
		if pf, _, err := e.runScope(ctx, p, checked, previous); err == nil {
			if p.Viz == "stat" && p.Query != nil && pf.Totals == nil {
				if pf.Totals, err = e.totals(ctx, p, checked, previous, pf); err != nil {
					res.partial = true
				}
			}
			res.Previous = pf
			res.ShiftMS = shift.Milliseconds()
		} else {
			res.partial = true
		}
	}
	if frame.Rows > 0 && e.applyMetricUnit(ctx, p, checked, scope, frame, res.Previous) != nil {
		res.partial = true
	}
	if zero {
		// A count of nothing is a real 0; the diagnosis says why, as a hint.
		var complete bool
		res.Diagnosis, complete = e.diagnose(ctx, p, checked.Filters[p.ID], scope)
		res.partial = res.partial || !complete
	} else if frame.Rows > 0 && p.Query != nil && p.Query.From != "metrics" && p.Viz != "state_timeline" && hasMeasure(frame) && !measuresHaveValue(frame) {
		// Rows whose every measure is null have nothing to plot. A state
		// timeline draws null buckets as unknown, so it keeps them.
		res.Status = StatusEmpty
		noun := "this measure"
		if len(p.Query.Measures) > 1 {
			noun = "these measures"
		}
		res.Diagnosis = fmt.Sprintf("The matching %s have no values for %s in this time range.", rowNoun(p), noun)
	} else if frame.Rows == 0 || emptyExpressionTotal(p, checked.Measures[p.ID], frame) || (p.Query != nil && p.Query.From == "metrics" && p.Viz != "histogram" && p.Viz != "heatmap" && !measuresHaveValue(frame)) {
		res.Status = StatusEmpty
		var complete bool
		res.Diagnosis, complete = e.diagnose(ctx, p, checked.Filters[p.ID], scope)
		res.partial = res.partial || !complete
	}
	// A sub-read cut short by the panel's timeout, or by its callers leaving,
	// left something out even where it reported no error, such as the
	// diagnosis, which gives up quietly.
	if ctx.Err() != nil {
		res.partial = true
	}
	return res
}

// totals runs the panel without a bucket and aligns the one-row result with
// the series frame's columns.
func (e *Executor) totals(ctx context.Context, p *Panel, checked *Checked, scope Scope, series *Frame) ([]any, error) {
	scope.Interval = 0
	total, _, err := e.runScope(ctx, p, checked, scope)
	if err != nil {
		return nil, err
	}
	row := totalsOf(total)
	out := make([]any, len(series.Columns))
	for i, column := range series.Columns {
		for j, totalColumn := range total.Columns {
			if column.Role == "measure" && totalColumn.Name == column.Name {
				out[i] = row[j]
			}
		}
	}
	return out, nil
}

func (e *Executor) runScope(ctx context.Context, p *Panel, checked *Checked, scope Scope) (*Frame, string, error) {
	ctx = queryrows.WithInventoryWindow(ctx, queryrows.Window{Start: scope.Start, End: scope.End})
	read := metricReadScope(p, scope)
	if p.SQL != "" {
		// SQL panels may look back up to the metric lookback before their
		// window (the default dashboard does for CPU rates and reporting
		// cadence), so their snapshot includes those files.
		read.Start = scope.Start.Add(-metricLookback)
	}
	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: read.Start, End: read.End})
	if p.Options != nil && p.Options.Split == "deploy" {
		return e.runDeploySplit(ctx, p, checked, scope)
	}

	if p.Viz == "health" || p.Viz == "service_map" {
		return e.runRollupPanel(ctx, p, checked.Filters[p.ID], scope)
	}
	if p.Query != nil {
		compiled, err := compileQuery(p, checked.Measures[p.ID], checked.Filters[p.ID], scope)
		if err != nil {
			return nil, "", err
		}
		// The disclosure is a scalar read of the same candidates CTE, so it
		// shares the ranking request and its pinned file snapshot.
		executionSQL := compiled.SQL
		scanColumns := compiled.Columns
		if compiled.ranking != nil {
			executionSQL = executionSQL[:compiled.rankOffset] + `, (SELECT count(*) FROM candidates WHERE n < 20)::DOUBLE AS "_fanql_below_floor"` + executionSQL[compiled.rankOffset:]
			scanColumns = append(append([]Column(nil), scanColumns...), Column{Name: "_fanql_below_floor", Type: "number"})
		}
		rows, err := e.engine.QueryContext(ctx, executionSQL, compiled.Args...)
		if err != nil {
			return nil, compiled.SQL, err
		}
		rowLimit := maxFrameRows
		if scope.Interval > 0 {
			rowLimit = 0 // Buckets are already bounded by interval and top-N.
		}
		if p.Viz == "heatmap" || p.Viz == "histogram" || p.Viz == "scatter" || p.Viz == "state_timeline" {
			rowLimit = distributionRows(p, len(compiled.Columns))
			if p.Viz == "scatter" {
				rowLimit = min(rowLimit, 1000)
			}
		}
		if p.Viz == "logs" || p.Viz == "traces" {
			rowLimit = rowLimitForPanel(p)
		}
		if p.Viz == "log_patterns" {
			rowLimit = patternLimit(p)
		}
		frame, err := scanFrame(rows, scanColumns, rowLimit)
		if frame != nil && compiled.ranking != nil {
			frame.ranking = compiled.ranking
			if frame.Rows > 0 {
				if n, ok := frame.Values[len(frame.Values)-1][0].(float64); ok {
					frame.ranking.BelowFloor = int(n)
				}
			}
			frame.Columns, frame.Values = frame.Columns[:len(frame.Columns)-1], frame.Values[:len(frame.Values)-1]
			if frame.ranking.BelowFloor > 0 {
				note := fmt.Sprintf("%d series had fewer than 20 samples", frame.ranking.BelowFloor)
				if compiled.rankingFloor {
					note += " and were ranked last"
				}
				frame.addNote(note)
			}
		}
		if frame != nil {
			if compiled.HasTotals {
				extractGroupingTotals(frame)
			}
			frame.bucketed = scope.Interval > 0 && len(compiled.Columns) > 0 && compiled.Columns[0].Role == "time"
			if compiled.TrendInterval > 0 {
				frame.Trend = &Trend{StartMS: compiled.TrendStart.UnixMilli(), StepMS: compiled.TrendInterval.Milliseconds()}
			}
		}
		if err == nil && (p.Viz == "heatmap" || p.Viz == "state_timeline") {
			err = boundAnalysisFrame(frame)
		}
		return frame, compiled.SQL, err
	}
	plan, err := e.sqlPanel(ctx, p.SQL, scope.Interval)
	if err != nil {
		return nil, "", err
	}
	patterns := plan.patterns
	query, _, args, err := bindParams(plan.canonical, func(name string) ([]any, bool, error) {
		switch name {
		case "__from":
			return []any{scope.Start.UTC()}, false, nil
		case "__to":
			return []any{scope.End.UTC()}, false, nil
		}
		v, ok := scope.Vars[name]
		if !ok {
			return nil, false, fmt.Errorf("$%s has no value", name)
		}
		if v.All || len(v.Values) == 0 {
			return []any{nil}, false, nil
		}
		if patterns[name] && len(v.Values[0]) > engine.MaxPatternBytes {
			return nil, false, patternTooLong(name)
		}
		return []any{v.Values[0]}, false, nil
	})
	if err != nil {
		return nil, "", err
	}
	rows, err := e.engine.QueryContext(ctx, plan.prepared, args...)
	if err != nil {
		return nil, query, err
	}
	frame, err := scanDynamicFrame(rows, plan.serialized, sqlPanelRows)
	if err == nil && p.Viz == "state_timeline" {
		if frame.Rows > 0 {
			err = validateTimelineColumns(frame.Columns)
		}
		if err == nil {
			err = boundAnalysisFrame(frame)
		}
	}
	return frame, query, err
}

// stopped is the batch context's error, separate from a panel's own timeout.
func failed(res Result, err error, stopped error) Result {
	ownTimeout := stopped == nil
	res.Status = StatusError
	res.Retryable = false
	var engineError *duckdb.Error
	isEngine := errors.As(err, &engineError)
	if isEngine {
		switch engineError.Type {
		case duckdb.ErrorTypeOutOfMemory, duckdb.ErrorTypeIO, duckdb.ErrorTypeInterrupt:
			res.Retryable = true
		}
	}
	// The engine reports a caller's cancellation as a bare interrupt. That is
	// expected navigation; unrelated failures and deadlines still need a log.
	interrupted := errors.Is(err, context.Canceled) || isEngine && engineError.Type == duckdb.ErrorTypeInterrupt
	if !errors.Is(stopped, context.Canceled) || !interrupted {
		slog.Error("panel query failed", "panel_id", res.ID, "error", err)
	}
	switch {
	case ownTimeout && errors.Is(err, context.DeadlineExceeded):
		res.Error = TimeoutError
		res.Retryable = true
	default:
		res.Error = errorMessage(err, executionErrorKind(err))
	}
	return res
}

func hasMeasure(f *Frame) bool {
	for _, c := range f.Columns {
		if c.Role == "measure" {
			return true
		}
	}
	return false
}

func measuresHaveValue(f *Frame) bool {
	if f == nil {
		return false
	}
	for i, c := range f.Columns {
		if c.Role == "measure" {
			for _, v := range f.Values[i] {
				if v != nil {
					return true
				}
			}
		}
	}
	return false
}

// zeroCount reports an ungrouped count that found no rows. A table gets one
// row holding 0; a stat or gauge totalling the window gets no buckets.
func zeroCount(p *Panel, measures []Measure, frame *Frame) bool {
	if p.Query == nil || len(p.Query.By) > 0 || p.Query.Bucket != "" || len(measures) == 0 {
		return false
	}
	for _, m := range measures {
		if m.Func != "count" || m.IsExpression() {
			return false
		}
	}
	if frame.Rows == 0 {
		return vizSpecs[p.Viz].reduces && p.Reduce == "window"
	}
	if frame.Rows != 1 {
		return false
	}
	for i, c := range frame.Columns {
		if c.Role == "measure" {
			if n, ok := frame.Values[i][0].(float64); !ok || n != 0 {
				return false
			}
		}
	}
	return true
}

func emptyExpressionTotal(p *Panel, measures []Measure, frame *Frame) bool {
	if p.Query == nil || (p.Viz != "stat" && p.Viz != "gauge") || len(measures) != 1 || !measures[0].IsExpression() {
		return false
	}
	for i, column := range frame.Columns {
		if column.Role == "measure" && i < len(frame.Totals) && frame.Totals[i] != nil {
			return false
		}
	}
	return true
}
