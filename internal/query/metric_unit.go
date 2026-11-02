package query

import (
	"context"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

// metricUnitTTL bounds how long a metric's reported unit is reused; a
// metric's unit rarely changes, and one lookup serves every panel on it.
// Windows without points or with units that disagree are not cached.
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
// expressions keep theirs; an authored panel unit wins in the renderer. An
// error means the unit could not be read and the columns were left as they
// were.
func (e *Executor) applyMetricUnit(ctx context.Context, p *Panel, checked *Checked, scope Scope, frames ...*Frame) error {
	if p.Query == nil || p.Query.From != "metrics" || p.Unit != "" {
		return nil
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
		return nil
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
				var err error
				if unit, err = e.metricUnit(ctx, name, scope); err != nil {
					return err
				}
				looked = true
			}
			frame.Columns[i].Unit = unit
		}
	}
	return nil
}

// metricUnit is the panel unit for the units the metric reports in the
// window, or "" when it reports none or units that disagree. Spellings of one
// unit, such as {packets} and packets, agree; so do units that map to the
// same panel unit. An empty unit reports nothing and defers to the others.
// An error means the units could not be read; nothing is remembered.
func (e *Executor) metricUnit(ctx context.Context, name string, scope Scope) (string, error) {
	now := e.now()
	e.mu.Lock()
	entry, ok := e.units[name]
	e.mu.Unlock()
	if ok && now.Before(entry.expires) {
		return entry.unit, nil
	}
	where, args := windowWhere(signals["metrics"], scope.Start, scope.End, "")
	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: scope.Start, End: scope.End})
	rows, err := e.engine.QueryContext(ctx, "SELECT DISTINCT coalesce(unit,'') FROM metrics WHERE name = ? AND "+where+" ORDER BY 1", append([]any{name}, args...)...)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	var units []string
	for rows.Next() {
		var u string
		if err := rows.Scan(&u); err != nil {
			return "", err
		}
		units = append(units, u)
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	if len(units) == 0 {
		// A window without points must not hide the unit from panels whose
		// windows see one.
		return "", nil
	}
	unit, agree := "", true
	spelling, mapped := "", map[string]bool{}
	for _, u := range units {
		if u == "" {
			continue
		}
		bare := strings.Trim(u, "{}")
		if spelling != "" && bare != spelling {
			agree = false
		}
		spelling = bare
		if m := panelUnit(u); m != "" {
			unit = m
			mapped[m] = true
		} else {
			mapped[""] = true
		}
	}
	if !agree && len(mapped) > 1 {
		return "", nil
	}
	e.mu.Lock()
	if e.units == nil || len(e.units) >= 1000 {
		e.units = map[string]unitEntry{}
	}
	e.units[name] = unitEntry{unit: unit, expires: now.Add(metricUnitTTL)}
	e.mu.Unlock()
	return unit, nil
}
