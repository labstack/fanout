package query

import (
	"context"
	"fmt"
	"strings"

	"github.com/labstack/fanout/internal/queryrows"
)

// maxDiagnosedFilters bounds the reruns one empty panel can cost: the
// validator caps queries at 16 filters; diagnosis examines only eight.
const maxDiagnosedFilters = 8

// diagnose explains an empty structured panel. It counts the signal in the
// window, then reruns the count with each of the first maxDiagnosedFilters
// filters removed in turn. The filter whose removal recovers the most rows is
// named in the context of the active combination, with the values seen for its
// field when it was an equality. Ties keep the author's filter order.
func (e *Executor) diagnose(ctx context.Context, p *Panel, filters []Filter, scope Scope) string {
	if p.Query == nil {
		return "The query returned no rows for this time range."
	}
	if p.Viz == "traces" && p.Query.Sort == "errors" {
		return "No erroring traces in this range"
	}
	sig, _ := lookupSignal(p.Query.From)
	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: scope.Start, End: scope.End})
	active := make([]Filter, 0, len(filters))
	for _, f := range filters {
		if !scope.dropped(f) {
			active = append(active, f)
		}
	}
	if (p.Viz == "logs" || p.Viz == "log_patterns") && p.Options != nil && p.Options.Highlight != "" {
		// The highlight is applied separately from the structured filters. Only
		// blame it when those active filters would otherwise return log rows.
		n, err := e.count(ctx, sig, active, scope)
		if err != nil {
			return ""
		}
		if n > 0 {
			return fmt.Sprintf("No logs contain %q in this range", p.Options.Highlight)
		}
	}
	if diagnosis := e.metricKindDiagnosis(ctx, p, active, scope); diagnosis != "" {
		return diagnosis
	}
	if sig.name == "metrics" && p.Viz != "heatmap" && p.Viz != "histogram" {
		matching, err := e.count(ctx, sig, active, scope)
		if err != nil {
			return ""
		}
		if matching > 0 {
			return "The selected metric points have no defined values for these measures in this time range."
		}
	}
	total, without, err := e.filterCounts(ctx, sig, active, scope)
	if err != nil {
		// One statement fails when any predicate fails on rows another filter
		// normally excludes, such as a cast; counted separately, a failing
		// rerun hides only its own count.
		total, without, err = e.rerunCounts(ctx, sig, active, scope)
	}
	if err != nil {
		return ""
	}
	if total == 0 {
		return fmt.Sprintf("No %s were recorded in this time range.", sig.name)
	}
	best, recovered := -1, int64(0)
	for i, n := range without {
		if n > recovered {
			best, recovered = i, n
		}
	}
	if best >= 0 {
		f := active[best]
		others := append(append([]Filter{}, active[:best]...), active[best+1:]...)
		selection := "one filter"
		if f.DisplayField != "" {
			value := f.DisplayLiteral
			if f.DisplayParam != "" {
				value = strings.Join(scope.Vars[f.DisplayParam].Values, ", ")
			}
			if value != "" {
				selection = f.DisplayField + " " + value
			} else {
				selection = f.DisplayField + " filter"
			}
		}
		message := fmt.Sprintf("No %s match the selected filters in this time range. Without %s, %d match.", sig.name, selection, recovered)
		if f.DisplayField != "" && (f.DisplayLiteral != "" || f.DisplayParam != "") {
			message = fmt.Sprintf("No %s for %s in this time range. Without that filter, %d match.", sig.name, selection, recovered)
		}
		if len(active) > 1 {
			message = fmt.Sprintf("No %s match this combination of filters in this time range. Without %s, %d match the remaining filters.", sig.name, selection, recovered)
		}
		if f.EqField != "" {
			if seen := e.topValues(ctx, sig, f.EqField, others, scope); seen != "" {
				message += " Values seen: " + seen + "."
			}
		}
		return message
	}
	return fmt.Sprintf("%d %s are in this time range, but the filters together match none of them.", total, sig.name)
}

func (e *Executor) count(ctx context.Context, sig *signal, filters []Filter, scope Scope) (int64, error) {
	where, args, err := buildWhere(sig, filters, scope)
	if err != nil {
		return 0, err
	}
	rows, err := e.engine.QueryContext(ctx, "SELECT count(*) FROM "+structuredSource(sig.name)+" WHERE "+where, args...)
	if err != nil {
		return 0, err
	}
	defer rows.Close()
	var n int64
	if rows.Next() {
		if err := rows.Scan(&n); err != nil {
			return 0, err
		}
	}
	return n, rows.Err()
}

// filterCounts scans the signal once for everything the diagnosis compares:
// the rows in the window, and for each of the first maxDiagnosedFilters
// active filters the rows that satisfy every other active filter. Each FILTER
// clause is the predicate the rerun count would have carried, so one
// statement replaces one scan per filter without changing a single count.
func (e *Executor) filterCounts(ctx context.Context, sig *signal, active []Filter, scope Scope) (total int64, without []int64, err error) {
	window, windowArgs, err := buildWhere(sig, nil, scope)
	if err != nil {
		return 0, nil, err
	}
	clauses := make([]string, len(active))
	clauseArgs := make([][]any, len(active))
	for i, f := range active {
		if clauses[i], clauseArgs[i], err = filterClause(f, scope); err != nil {
			return 0, nil, err
		}
	}
	// Positional parameters bind in textual order: the FILTER clauses precede
	// the window predicate in the statement, so their arguments come first.
	without = make([]int64, min(len(active), maxDiagnosedFilters))
	selects := []string{"count(*)"}
	dest := []any{&total}
	var args []any
	for i := range without {
		var others []string
		for j := range active {
			if j != i {
				others = append(others, clauses[j])
				args = append(args, clauseArgs[j]...)
			}
		}
		if len(others) == 0 {
			selects = append(selects, "count(*)")
		} else {
			selects = append(selects, "count(*) FILTER (WHERE "+strings.Join(others, " AND ")+")")
		}
		dest = append(dest, &without[i])
	}
	args = append(args, windowArgs...)
	rows, err := e.engine.QueryContext(ctx, "SELECT "+strings.Join(selects, ", ")+" FROM "+structuredSource(sig.name)+" WHERE "+window, args...)
	if err != nil {
		return 0, nil, err
	}
	defer rows.Close()
	if rows.Next() {
		if err := rows.Scan(dest...); err != nil {
			return 0, nil, err
		}
	}
	return total, without, rows.Err()
}

// rerunCounts is filterCounts one statement at a time. A rerun that fails
// counts no rows.
func (e *Executor) rerunCounts(ctx context.Context, sig *signal, active []Filter, scope Scope) (int64, []int64, error) {
	total, err := e.count(ctx, sig, nil, scope)
	if err != nil {
		return 0, nil, err
	}
	without := make([]int64, min(len(active), maxDiagnosedFilters))
	for i := range without {
		others := append(append([]Filter{}, active[:i]...), active[i+1:]...)
		without[i], _ = e.count(ctx, sig, others, scope)
	}
	return total, without, nil
}

func (e *Executor) topValues(ctx context.Context, sig *signal, fieldText string, filters []Filter, scope Scope) string {
	ref, err := sig.field(fieldText)
	if err != nil {
		return ""
	}
	where, args, err := buildWhere(sig, filters, scope)
	if err != nil {
		return ""
	}
	expr := ref.stringSQL()
	rows, err := e.engine.QueryContext(ctx, fmt.Sprintf("SELECT %s AS v, count(*) AS n FROM %s WHERE %s AND %s IS NOT NULL GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT 5", expr, structuredSource(sig.name), where, expr), args...)
	if err != nil {
		return ""
	}
	defer rows.Close()
	var parts []string
	for rows.Next() {
		var value string
		var n int64
		if rows.Scan(&value, &n) == nil {
			parts = append(parts, fmt.Sprintf("%s (%d)", value, n))
		}
	}
	return strings.Join(parts, ", ")
}

func (e *Executor) metricKindDiagnosis(ctx context.Context, p *Panel, filters []Filter, scope Scope) string {
	if p.Query == nil || p.Query.From != "metrics" || p.Viz == "heatmap" || p.Viz == "histogram" {
		return ""
	}
	var ignored Problems
	measures := parseMeasures(signals["metrics"], p.Query.Measures, "", &ignored)
	relevant := []Measure{}
	for _, m := range measures {
		if m.Func == "rate" || metricValueMeasure(m) {
			relevant = append(relevant, m)
		}
	}
	if len(relevant) == 0 {
		return ""
	}
	where, args, err := buildWhere(signals["metrics"], filters, scope)
	if err != nil {
		return ""
	}
	rows, err := e.engine.QueryContext(ctx, "SELECT name,type,count(*) FROM metrics WHERE "+where+" GROUP BY 1,2 ORDER BY 3 DESC,1,2 LIMIT 6", args...)
	if err != nil {
		return ""
	}
	defer rows.Close()
	type metricKind struct{ name, kind string }
	kinds := []metricKind{}
	for rows.Next() {
		var k metricKind
		var n int64
		if rows.Scan(&k.name, &k.kind, &n) != nil {
			return ""
		}
		kinds = append(kinds, k)
	}
	if rows.Err() != nil || len(kinds) == 0 {
		return ""
	}
	for _, m := range relevant {
		compatible := false
		for _, k := range kinds {
			for _, supported := range metricMeasureKinds(m) {
				if k.kind == supported {
					compatible = true
				}
			}
		}
		if compatible {
			continue
		}
		k := kinds[0]

		if m.Func == "rate" && (k.kind == "updown" || k.kind == "gauge") {
			article := "a"
			if k.kind == "updown" {
				article = "an"
			}
			return fmt.Sprintf("A rate needs a counter; %s is %s %s. Show its latest or average value instead.", k.name, article, k.kind)
		}
		suggestion := "the latest or average value"
		switch k.kind {
		case "counter":
			suggestion = "the rate or latest value"
		case "histogram":
			suggestion = "the rate, average or 95th percentile"
		case "summary":
			suggestion = "the number of observations"
		}
		return fmt.Sprintf("This measure is unavailable for %s (%s). Show %s instead.", k.name, k.kind, suggestion)
	}
	return ""
}
