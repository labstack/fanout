package panel

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
// named, with the values seen for its field when it was an equality. Ties
// keep the author's filter order.
func (e *Executor) diagnose(ctx context.Context, p *Panel, filters []Filter, scope Scope) string {
	if p.Query == nil {
		return "The query returned no rows for this time range."
	}
	if (p.Viz == "logs" || p.Viz == "log_patterns") && p.Options != nil && p.Options.Highlight != "" {
		return fmt.Sprintf("No logs contain %q in this range", p.Options.Highlight)
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
	total, err := e.count(ctx, sig, nil, scope)
	if err != nil {
		return ""
	}
	if total == 0 {
		return fmt.Sprintf("No %s were recorded in this time range.", sig.name)
	}
	best, recovered := -1, int64(0)
	for i := range min(len(active), maxDiagnosedFilters) {
		others := append(append([]Filter{}, active[:i]...), active[i+1:]...)
		n, err := e.count(ctx, sig, others, scope)
		if err != nil || n <= recovered {
			continue
		}
		best, recovered = i, n
	}
	if best >= 0 {
		f := active[best]
		others := append(append([]Filter{}, active[:best]...), active[best+1:]...)
		message := fmt.Sprintf("No %s match %s. Without that filter, %d do.", sig.name, f.Source, recovered)
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
