package panel

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

func trendKey(values []string) string { raw, _ := json.Marshal(values); return string(raw) }
func (e *Executor) attachTableTrends(ctx context.Context, p *Panel, checked *Checked, scope Scope, f *Frame) (err error) {
	defer func() {
		if err != nil && f != nil {
			f.addNote("Table trends unavailable.")
		}
	}()
	if p.Viz != "table" || p.Query == nil || p.Options == nil || f == nil || f.Rows == 0 {
		return nil
	}
	wanted := map[string]bool{}
	for _, format := range p.Options.Columns {
		if format.Format == "sparkline" {
			wanted[format.Field] = true
		}
	}
	if len(wanted) == 0 {
		return nil
	}
	sig, _ := lookupSignal(p.Query.From)
	where, args, err := buildWhere(sig, checked.Filters[p.ID], scope)
	if err != nil {
		return err
	}
	dimensions := []FieldRef{}
	dimensionSQL := []string{}
	indices := []int{}
	for _, by := range p.Query.By {
		ref, err := sig.field(by)
		if err != nil {
			return err
		}
		dimensions = append(dimensions, ref)
		dimensionSQL = append(dimensionSQL, "coalesce("+ref.stringSQL()+",'')")
		index := -1
		for i, c := range f.Columns {
			if c.Name == ref.alias() {
				index = i
				break
			}
		}
		if index < 0 {
			return fmt.Errorf("trend dimension missing")
		}
		indices = append(indices, index)
	}
	rowKeys := map[string]int{}
	tuples := []string{}
	for r := 0; r < f.Rows; r++ {
		values := []string{}
		slots := []string{}
		for _, index := range indices {
			value, _ := f.Values[index][r].(string)
			values = append(values, value)
			slots = append(slots, "?")
			args = append(args, value)
		}
		rowKeys[trendKey(values)] = r
		if len(slots) > 0 {
			tuples = append(tuples, "("+strings.Join(slots, ",")+")")
		}
	}
	if len(dimensionSQL) > 0 {
		where += " AND (" + strings.Join(dimensionSQL, ",") + ") IN (" + strings.Join(tuples, ",") + ")"
	}
	interval := max(time.Minute, AutoInterval(scope.End.Sub(scope.Start), 960))
	// Count every intersecting bucket, including an end a nanosecond past
	// a boundary. Duration alone can miss the last bucket of an unaligned window.
	for (scope.End.Add(-time.Nanosecond).UnixMilli()-alignedBucketMillis(scope.Start, interval))/interval.Milliseconds()+1 > 240 {
		interval *= 2
	}
	bucket := fmt.Sprintf("time_bucket(INTERVAL '%d seconds',%s::TIMESTAMP_NS,'1970-01-01'::TIMESTAMP_NS)", int64(interval/time.Second), quoteIdent(sig.time))
	selects := []string{"epoch_ms(" + bucket + ")::BIGINT AS _t"}
	groups := []string{bucket}
	columns := []Column{{Name: "time", Type: "time", Role: "time"}}
	for i, ref := range dimensions {
		selects = append(selects, dimensionSQL[i]+" AS "+quoteIdent(ref.alias()))
		groups = append(groups, dimensionSQL[i])
		columns = append(columns, Column{Name: ref.alias(), Type: "string", Role: "dimension"})
	}
	names := []string{}
	for _, m := range checked.Measures[p.ID] {
		if wanted[m.Alias] {
			selects = append(selects, measureSQL(m, sig, interval.Seconds(), "PARTITION BY "+bucket)+" AS "+quoteIdent(m.Alias))
			columns = append(columns, Column{Name: m.Alias, Type: "number", Role: "measure", Unit: m.Unit})
			names = append(names, m.Alias)
		}
	}
	if len(names) == 0 {
		return nil
	}
	limit := analysisCellLimit / len(columns)
	text := "SELECT " + strings.Join(selects, ",") + " FROM " + structuredSource(sig.name) + " WHERE " + where + " GROUP BY " + strings.Join(groups, ",") + fmt.Sprintf(" ORDER BY _t DESC LIMIT %d", limit+1)
	rows, err := e.engine.QueryContext(queryrows.WithWindow(ctx, queryrows.Window{Start: scope.Start, End: scope.End}), text, args...)
	if err != nil {
		return err
	}
	trend, err := scanFrame(rows, columns, limit)
	if err != nil {
		return err
	}
	boundAnalysisFrame(trend)
	f.Trends = map[string][][]any{}
	lo := alignedBucketMillis(scope.Start, interval)
	points := int((scope.End.Add(-time.Nanosecond).UnixMilli()-lo)/interval.Milliseconds() + 1)
	points = min(points, 240)
	budget := analysisCellLimit
	for _, name := range names {
		series := make([][]any, f.Rows)
		for r := range series {
			if budget < points {
				f.Truncated = true
				f.addNote("Table trends are limited by the cell budget.")
				continue
			}
			series[r] = make([]any, points)
			budget -= points
		}
		f.Trends[name] = series
	}
	for r := 0; r < trend.Rows; r++ {
		values := []string{}
		for i := range dimensions {
			value, _ := trend.Values[i+1][r].(string)
			values = append(values, value)
		}
		row, ok := rowKeys[trendKey(values)]
		if !ok {
			continue
		}
		point := int((trend.Values[0][r].(int64) - lo) / interval.Milliseconds())
		if point < 0 || point >= points {
			continue
		}
		for i, name := range names {
			if len(f.Trends[name][row]) > point {
				f.Trends[name][row][point] = trend.Values[len(dimensions)+1+i][r]
			}
		}
	}
	if trend.Truncated {
		f.Truncated = true
		f.addNote("Table trends are limited by the cell budget.")
	}
	return nil
}
