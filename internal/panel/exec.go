package panel

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
	"golang.org/x/sync/errgroup"
)

// Engine is the query engine the executor runs on. *query.Duck implements it;
// QueryContext binds the Parquet snapshot for the window in the context.
type Engine interface {
	Parser
	QueryContext(ctx context.Context, query string, args ...any) (queryrows.Rows, error)
}

const (
	StatusOK    = "ok"
	StatusEmpty = "empty"
	StatusError = "error"
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
	ID        string `json:"id"`
	Status    string `json:"status"`
	Frame     *Frame `json:"frame,omitempty"`
	Previous  *Frame `json:"previous,omitempty"`
	Error     string `json:"error,omitempty"`
	Diagnosis string `json:"diagnosis,omitempty"`
	SQL       string `json:"sql,omitempty"`
	Interval  string `json:"interval,omitempty"`
	ElapsedMS int64  `json:"elapsed_ms"`
	Better    string `json:"better,omitempty"`
	// ShiftMS is how far Previous sits behind Frame, so a client can overlay it
	// without guessing from the first populated bucket.
	ShiftMS int64 `json:"shift_ms,omitempty"`
}

type Executor struct {
	engine       Engine
	maxWindow    time.Duration
	now          func() time.Time
	timeout      time.Duration
	batchTimeout time.Duration
	parallel     int

	mu      sync.Mutex
	checks  map[[32]byte]*Checked
	schemas map[string]schemaEntry
}

func NewExecutor(engine Engine, retentionDays int) *Executor {
	maxWindow := 30 * 24 * time.Hour
	if retentionDays > 0 {
		maxWindow = time.Duration(retentionDays) * 24 * time.Hour
	}
	return &Executor{engine: engine, maxWindow: maxWindow, now: time.Now, timeout: 10 * time.Second, batchTimeout: 20 * time.Second, parallel: 4,
		checks: map[[32]byte]*Checked{}, schemas: map[string]schemaEntry{}}
}

// Validate normalizes d and runs every check, returning Problems or nil.
func (e *Executor) Validate(ctx context.Context, d *Dashboard) error {
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	Normalize(d)
	_, err := e.check(ctx, d)
	return err
}

// check memoizes Check by the spec's bytes: a dashboard refreshes the same
// spec every 30 seconds, and its filters do not need re-parsing each time.
func (e *Executor) check(ctx context.Context, d *Dashboard) (*Checked, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	raw, err := json.Marshal(d)
	if err != nil {
		return nil, err
	}
	key := sha256.Sum256(raw)
	e.mu.Lock()
	cached, ok := e.checks[key]
	e.mu.Unlock()
	if ok {
		return cached, nil
	}
	checked, problems, err := Check(ctx, e.engine, d)
	if err != nil {
		return nil, err
	}
	if len(problems) > 0 {
		return nil, problems
	}
	e.mu.Lock()
	if len(e.checks) >= 256 {
		clear(e.checks)
	}
	e.checks[key] = checked
	e.mu.Unlock()
	return checked, nil
}

// Run validates the dashboard and runs the requested panels concurrently. An
// invalid spec returns its Problems; a panel that fails to run reports the
// failure in its own result, so one broken panel cannot blank a dashboard.
func (e *Executor) Run(ctx context.Context, req RunRequest) ([]Result, error) {
	caller := ctx
	d := req.Dashboard
	Normalize(&d)
	if len(req.Panels) > len(d.Panels) {
		return nil, Problems{{Path: "panels", Message: "panel selection cannot exceed the dashboard's panel count"}}
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
				return nil, Problems{{Path: "panels", Message: fmt.Sprintf("no panel has id %q", id)}}
			}
			targets = append(targets, &d.Panels[index])
		}
	}
	ctx, cancel := context.WithTimeout(ctx, e.batchTimeout)
	defer cancel()
	results := make([]Result, len(targets))
	for i, p := range targets {
		results[i] = Result{ID: p.ID, Status: StatusError, Error: BatchDeadlineError}
	}
	batchError := func(err error) ([]Result, error) {
		if caller.Err() != nil {
			return nil, caller.Err()
		}
		if ctx.Err() == context.DeadlineExceeded {
			return results, nil
		}
		return nil, err
	}
	checked, err := e.check(ctx, &d)
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
			return nil, problems
		}
	}
	start, end, err := resolveWindow(t, nil, e.now(), e.maxWindow)
	if err != nil {
		return nil, Problems{{Path: "time", Message: err.Error()}}
	}
	vars, err := e.values(ctx, &d, checked, start, end, req.Vars)
	if err != nil {
		return batchError(err)
	}
	compare := d.Time.Compare == "previous_period"
	if req.Compare != nil {
		compare = *req.Compare
	}
	var group errgroup.Group
	group.SetLimit(e.parallel)
	for i, p := range targets {
		group.Go(func() error {
			if ctx.Err() != nil {
				return nil
			}
			result := e.runPanel(ctx, p, checked, t, vars, req.Widths[p.ID], compare)
			if ctx.Err() == context.DeadlineExceeded {
				result = Result{ID: p.ID, Status: StatusError, Error: BatchDeadlineError, ElapsedMS: result.ElapsedMS}
			}
			results[i] = result
			return nil
		})
	}
	_ = group.Wait()
	if err := caller.Err(); err != nil {
		return nil, err
	}
	limitBatchFrames(results)
	return results, nil
}

// BatchDeadlineError is shared by HTTP and MCP batch callers.
const BatchDeadlineError = "Not run: the dashboard ran out of time. Narrow the time range or split the dashboard."

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
				f.Rows = rows
				f.Truncated = true
			}
			remaining -= rows * len(f.Columns)
		}
	}
}

func (e *Executor) runPanel(ctx context.Context, p *Panel, checked *Checked, t Time, vars map[string]Value, width int, compare bool) (res Result) {
	parent := ctx
	started := time.Now()
	res = Result{ID: p.ID, Status: StatusOK}
	if p.Better == "" {
		res.Better = inferBetter(p)
	}
	defer func() { res.ElapsedMS = time.Since(started).Milliseconds() }()
	if p.Viz == "text" {
		return res
	}
	start, end, err := resolveWindow(t, p.Time, e.now(), e.maxWindow)
	if err != nil {
		return failed(res, err, parent.Err() == nil)
	}
	ctx, cancel := context.WithTimeout(ctx, e.timeout)
	defer cancel()
	reduces := vizSpecs[p.Viz].reduces
	var interval time.Duration
	switch {
	case reduces:
		interval = AutoInterval(end.Sub(start), 240)
	case p.Query != nil:
		interval = bucketInterval(p.Query.Bucket, end.Sub(start), width)
	default:
		interval = AutoInterval(end.Sub(start), width)
	}
	scope := Scope{Start: start, End: end, Interval: interval, Vars: vars}
	frame, sqlText, err := e.runScope(ctx, p, checked, scope)
	if err != nil {
		res.SQL = sqlText
		return failed(res, err, parent.Err() == nil)
	}
	res.Frame, res.SQL = frame, sqlText
	if interval > 0 && (reduces || p.Query == nil || p.Query.Bucket != "") {
		res.Interval = formatInterval(interval)
	}
	if (p.Viz == "stat" || p.Viz == "gauge") && p.Query != nil {
		totals, err := e.totals(ctx, p, checked, scope, frame)
		if err != nil {
			return failed(res, err, parent.Err() == nil)
		}
		frame.Totals = totals
	}
	if compare && (p.Viz == "stat" || p.Viz == "timeseries") {
		shift := end.Sub(start)
		previous := scope
		previous.Start, previous.End = start.Add(-shift), end.Add(-shift)
		if pf, _, err := e.runScope(ctx, p, checked, previous); err == nil {
			if p.Viz == "stat" && p.Query != nil {
				pf.Totals, _ = e.totals(ctx, p, checked, previous, pf)
			}
			res.Previous = pf
			res.ShiftMS = shift.Milliseconds()
		}
	}
	if frame.Rows == 0 {
		res.Status = StatusEmpty
		res.Diagnosis = e.diagnose(ctx, p, checked.Filters[p.ID], scope)
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
	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: scope.Start, End: scope.End})
	if p.Query != nil {
		compiled, err := compileQuery(p, checked.Measures[p.ID], checked.Filters[p.ID], scope)
		if err != nil {
			return nil, "", err
		}
		rows, err := e.engine.QueryContext(ctx, compiled.SQL, compiled.Args...)
		if err != nil {
			return nil, compiled.SQL, err
		}
		rowLimit := maxFrameRows
		if scope.Interval > 0 {
			rowLimit = 0 // Buckets are already bounded by interval and top-N.
		}
		frame, err := scanFrame(rows, compiled.Columns, rowLimit)
		if frame != nil {
			frame.bucketed = scope.Interval > 0
		}
		return frame, compiled.SQL, err
	}
	expanded, err := expandMacros(p.SQL, scope.Interval)
	if err != nil {
		return nil, "", err
	}
	canonical, err := canonicalSQL(ctx, e.engine, expanded)
	if err != nil {
		return nil, "", err
	}
	query, describe, args, err := bindParams(canonical, func(name string) ([]any, bool, error) {
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
		return []any{v.Values[0]}, false, nil
	})
	if err != nil {
		return nil, "", err
	}
	prepared, serialized, err := e.engine.PrepareTelemetrySQL(ctx, query, describe, sqlPanelRows+1)
	if err != nil {
		return nil, query, err
	}
	rows, err := e.engine.QueryContext(ctx, prepared, args...)
	if err != nil {
		return nil, query, err
	}
	frame, err := scanDynamicFrame(rows, serialized, sqlPanelRows)
	return frame, query, err
}

func failed(res Result, err error, ownTimeout bool) Result {
	res.Status = StatusError
	slog.Error("panel query failed", "panel_id", res.ID, "error", err)
	switch {
	case ownTimeout && errors.Is(err, context.DeadlineExceeded):
		res.Error = "The query took longer than 10 seconds. Narrow the time range or add filters."
	default:
		message := RedactPaths(err.Error())
		if len(message) > 500 {
			message = message[:500] + "…"
		}
		res.Error = strings.TrimSpace(message)
	}
	return res
}
