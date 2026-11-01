package query

import (
	"context"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

// metricUnitTTL bounds how long a metric's reported unit is reused; a
// metric's unit rarely changes, and one lookup serves every panel on it.
const metricUnitTTL = 10 * time.Minute

type unitEntry struct {
	unit    string
	expires time.Time
}

// panelUnit maps an OpenTelemetry (UCUM) unit to a panel unit, or "" when
// none fits. "1" is left out: receivers report both ratios and percents as 1.
func panelUnit(otel string) string {
	switch otel {
	case "s", "ms", "ns":
		return otel
	case "By":
		return "bytes"
	case "%":
		return "percent"
	}
	if strings.HasPrefix(otel, "{") && strings.HasSuffix(otel, "}") {
		return "count"
	}
	return ""
}

// applyMetricUnit gives a metric panel's value columns the unit its metric
// reports. Columns with a unit of their own, such as counts and rates, and
// expressions keep theirs; an authored panel unit wins in the renderer.
func (e *Executor) applyMetricUnit(ctx context.Context, p *Panel, checked *Checked, scope Scope, frames ...*Frame) {
	if p.Query == nil || p.Query.From != "metrics" || p.Unit != "" {
		return
	}
	name := ""
	for _, f := range checked.Filters[p.ID] {
		if f.EqField != "name" || !f.EqValue {
			continue
		}
		name = f.EqLiteral
		if v, ok := scope.Vars[f.EqParam]; f.EqParam != "" && ok && !v.All && len(v.Values) == 1 {
			name = v.Values[0]
		}
	}
	if name == "" {
		return
	}
	plain := map[string]bool{}
	for _, m := range checked.Measures[p.ID] {
		plain[m.Alias] = !m.IsExpression() && m.Field != nil && (m.Field.Column == "value" || m.Field.Column == "hist_sum")
	}
	unit := ""
	looked := false
	for _, frame := range frames {
		if frame == nil {
			continue
		}
		for i, c := range frame.Columns {
			if c.Role != "measure" || c.Unit != "" || !plain[c.Name] {
				continue
			}
			if !looked {
				unit, looked = panelUnit(e.metricUnit(ctx, name, scope)), true
			}
			frame.Columns[i].Unit = unit
		}
	}
}

// metricUnit is the unit the metric reports in the window, or "" when it
// reports none or more than one.
func (e *Executor) metricUnit(ctx context.Context, name string, scope Scope) string {
	now := e.now()
	e.mu.Lock()
	entry, ok := e.units[name]
	e.mu.Unlock()
	if ok && now.Before(entry.expires) {
		return entry.unit
	}
	where, args := windowWhere(signals["metrics"], scope.Start, scope.End, "")
	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: scope.Start, End: scope.End})
	rows, err := e.engine.QueryContext(ctx, "SELECT coalesce(min(unit),''),coalesce(max(unit),'') FROM metrics WHERE name = ? AND "+where, append([]any{name}, args...)...)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var lo, hi string
	if !rows.Next() || rows.Scan(&lo, &hi) != nil || rows.Err() != nil {
		return ""
	}
	if lo != hi {
		lo = ""
	}
	e.mu.Lock()
	if e.units == nil || len(e.units) >= 1000 {
		e.units = map[string]unitEntry{}
	}
	e.units[name] = unitEntry{unit: lo, expires: now.Add(metricUnitTTL)}
	e.mu.Unlock()
	return lo
}
