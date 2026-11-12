package query

import (
	"context"
	"database/sql"
	"fmt"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/labstack/pitot/internal/queryrows"
)

// maxDiagnosedFilters bounds the reruns one empty panel can cost: the
// validator caps queries at 16 filters; diagnosis examines only eight.
const maxDiagnosedFilters = 8

// deferDiagnosisKey carries the batch admission policy into shared cache runs.
type deferDiagnosisKey struct{}

// diagnose explains an empty structured panel. It counts the signal in the
// window, then reruns the count with each of the first maxDiagnosedFilters
// filters removed in turn. The filter whose removal recovers the most rows is
// named in the context of the active combination, with the values seen for its
// field when it was an equality. Ties keep the author's filter order.
//
// The diagnosis shares the panel's deadline, so after a slow query it may not
// finish; the panel then says only what the query showed.
func (e *Executor) diagnose(ctx context.Context, p *Panel, filters []Filter, scope Scope) (message string, complete bool) {
	if p.Query == nil {
		return "The query returned no rows for this time range.", true
	}
	deferred, _ := ctx.Value(deferDiagnosisKey{}).(bool)
	if scope.Rollup || !deferred {
		if message := e.explain(ctx, p, filters, scope); message != "" {
			return message, true
		}
	}
	message = fmt.Sprintf("No %s match this query in this time range.", rowNoun(p))
	for _, f := range filters {
		if !scope.dropped(f) {
			message = fmt.Sprintf("No %s match these filters in this time range.", rowNoun(p))
			break
		}
	}
	if deferred && !scope.Rollup {
		message += " Filter diagnosis was skipped because the server is busy with other queries."
	}
	return message, false
}

// rowNoun names what a structured panel lists: traces on the traces viz,
// otherwise the rows of its signal.
func rowNoun(p *Panel) string {
	if p.Viz == "traces" {
		return "traces"
	}
	return p.Query.From
}

func (e *Executor) explain(ctx context.Context, p *Panel, filters []Filter, scope Scope) string {
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
			value := spoken(sig, f.EqField, f.DisplayLiteral)
			if f.DisplayParam != "" {
				value = strings.Join(scope.Vars[f.DisplayParam].Values, ", ")
			}
			if value != "" {
				selection = f.DisplayField + " " + value
			} else {
				selection = "the " + f.DisplayField + " filter"
			}
		}
		// Counts are of the signal's rows, so the message names them even
		// where the traces viz lists traces of spans.
		noun, matched := sig.name, grouped(recovered)
		message := fmt.Sprintf("No %s match the selected filters in this time range. Without %s, %s match.", noun, selection, matched)
		if f.DisplayField != "" && (f.DisplayLiteral != "" || f.DisplayParam != "") {
			message = fmt.Sprintf("No %s for %s in this time range. Without that filter, %s match.", noun, selection, matched)
		}
		if len(active) > 1 {
			message = fmt.Sprintf("No %s match this combination of filters in this time range. Without %s, %s match the remaining filters.", noun, selection, matched)
		}
		if f.EqField != "" {
			seen, n, near := e.seenValues(ctx, sig, f, others, scope)
			if near != "" {
				message += " Did you mean " + spoken(sig, f.EqField, near) + "?"
			}
			if seen != "" {
				message += " Values seen: " + seen + "."
			}
			if n > 0 {
				// The filter matches one type only; say which form finds the rest.
				if strings.Contains(f.Expr, "'"+strings.ReplaceAll(f.DisplayLiteral, "'", "''")+"'") {
					message += fmt.Sprintf(" %s %s store %s %s as a number; write the value without quotes, %s, to match them.", grouped(n), sig.name, f.DisplayField, f.DisplayLiteral, f.DisplayLiteral)
				} else {
					message += fmt.Sprintf(" %s %s store %s %s as text; quote the value, \"%s\", to match them.", grouped(n), sig.name, f.DisplayField, f.DisplayLiteral, f.DisplayLiteral)
				}
			}
		}
		return message
	}
	return fmt.Sprintf("%s %s are in this time range, but the filters together match none of them.", grouped(total), sig.name)
}

// grouped writes a count, which is never negative, with thousands separators.
func grouped(n int64) string {
	digits := strconv.FormatInt(n, 10)
	var b strings.Builder
	for i, d := range digits {
		if i > 0 && (len(digits)-i)%3 == 0 {
			b.WriteByte(',')
		}
		b.WriteRune(d)
	}
	return b.String()
}

// spoken writes a stored enum value as PitotQL spells it: STATUS_CODE_ERROR is
// error. Other values are unchanged.
func spoken(sig *signal, field, value string) string {
	if v := fieldVocabularies[field]; v.Signal == sig.name {
		for alias, stored := range v.Aliases {
			if stored == value {
				return alias
			}
		}
	}
	return value
}

func (e *Executor) count(ctx context.Context, sig *signal, filters []Filter, scope Scope) (int64, error) {
	where, args, err := buildWhere(sig, filters, scope)
	if err != nil {
		return 0, err
	}
	rows, err := e.engine.QueryContext(ctx, "SELECT "+diagnosisCount(scope, "")+" FROM "+diagnosisSource(sig, scope)+" WHERE "+where, args...)
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
	selects := []string{diagnosisCount(scope, "")}
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
			selects = append(selects, diagnosisCount(scope, ""))
		} else {
			selects = append(selects, diagnosisCount(scope, " FILTER (WHERE "+strings.Join(others, " AND ")+")"))
		}
		dest = append(dest, &without[i])
	}
	args = append(args, windowArgs...)
	rows, err := e.engine.QueryContext(ctx, "SELECT "+strings.Join(selects, ", ")+" FROM "+diagnosisSource(sig, scope)+" WHERE "+window, args...)
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

// seenValues lists the five most common values of the filter's field among
// the rows the other filters keep. For a literal it also finds the nearest
// other value within a small edit distance, a likely typo, and counts the rows
// whose attribute equals the literal as text: attribute equality is typed, so
// 500 misses a stored "500", and in an empty panel the filter itself matches
// none of these rows. One grouped scan answers all three: decoding attributes
// dominates the cost, and more scans could outlast the panel's deadline.
func (e *Executor) seenValues(ctx context.Context, sig *signal, f Filter, others []Filter, scope Scope) (seen string, other int64, near string) {
	ref, err := sig.field(f.EqField)
	if err != nil {
		return "", 0, ""
	}
	where, args, err := buildWhere(sig, others, scope)
	if err != nil {
		return "", 0, ""
	}
	expr := ref.stringSQL()
	query := fmt.Sprintf("WITH g AS MATERIALIZED (SELECT %s AS v, count(*) AS n FROM %s WHERE %s AND %s IS NOT NULL GROUP BY 1) "+
		"SELECT * FROM (SELECT v, n, 'seen' AS kind FROM g ORDER BY n DESC, v LIMIT 5) UNION ALL SELECT '', sum(n), 'rows' FROM g", expr, diagnosisSource(sig, scope), where, expr)
	if literal := f.DisplayLiteral; literal != "" {
		if _, err := strconv.ParseFloat(literal, 64); err != nil && ref.Type == TypeString && utf8.RuneCountInString(literal) >= 3 && fieldVocabularies[f.EqField].Signal != sig.name {
			// A typo changes about one character in four of a name; numbers,
			// short literals and named values such as status error are no
			// typos. Only the 1,000 most common values of the same length,
			// give or take that, are compared, so an ID field stays cheap.
			k := utf8.RuneCountInString(literal)/4 + 1
			query += " UNION ALL SELECT * FROM (SELECT v, n, 'near' FROM (SELECT v, n, levenshtein(v, ?) AS d FROM (SELECT v, n FROM g WHERE v <> ? AND abs(length(v) - length(?)) <= ? ORDER BY n DESC, v LIMIT 1000)) WHERE d <= ? ORDER BY d, n DESC, v LIMIT 1)"
			args = append(args, literal, literal, literal, k, k)
		}
		// A standard key reads typed: ingest converted its values, so no
		// other form of the literal matches more rows.
		if _, dedicated := sig.source.Dedicated(ref.Column, ref.Key); ref.Key != "" && !dedicated {
			query += " UNION ALL SELECT v, n, 'literal' FROM g WHERE v = ?"
			args = append(args, literal)
		}
	}
	if scope.Rollup {
		query = strings.ReplaceAll(query, "count(*) AS n", "sum(spans) AS n")
	}
	rows, err := e.engine.QueryContext(ctx, query, args...)
	if err != nil {
		return "", 0, ""
	}
	defer rows.Close()
	var parts []string
	var shown, total int64
	for rows.Next() {
		var value, kind string
		var n sql.NullInt64
		if rows.Scan(&value, &n, &kind) != nil {
			continue
		}
		switch kind {
		case "seen":
			if value == "" {
				value = `""`
			}
			parts = append(parts, fmt.Sprintf("%s (%s)", spoken(sig, f.EqField, value), grouped(n.Int64)))
			shown += n.Int64
		case "rows":
			total = n.Int64
		case "near":
			near = value
		case "literal":
			other = n.Int64
		}
	}
	if rows.Err() != nil {
		return "", 0, ""
	}
	if strings.HasSuffix(ref.Column, "_id") && ref.Key == "" || shown*100 < total {
		// IDs, and any field whose five most common values cover under one
		// row in a hundred, are near-unique: listing some says nothing.
		parts = nil
	}
	return strings.Join(parts, ", "), other, near
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

func diagnosisSource(sig *signal, scope Scope) string {
	if scope.Rollup {
		return rollupReadSource(scope.RollupSource)
	}
	return structuredSource(sig.name)
}
func diagnosisCount(scope Scope, filter string) string {
	if scope.Rollup {
		return "coalesce(sum(spans)" + filter + ",0)::BIGINT"
	}
	return "count(*)" + filter
}
