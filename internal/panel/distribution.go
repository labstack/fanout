package panel

import (
	"fmt"
	"sort"
	"strings"
	"time"
)

const analysisCellLimit = 20000

func distributionRows(p *Panel, columns int) int {
	n := analysisCellLimit / max(columns, 1)
	if p.Viz == "histogram" {
		n = min(n, 1000)
	}
	if p.Query.Limit > 0 {
		n = min(n, p.Query.Limit)
	}
	return n
}
func compileDistribution(p *Panel, filters []Filter, scope Scope) (Compiled, error) {
	sig, _ := lookupSignal(p.Query.From)
	where, args, err := buildWhere(sig, filters, scope)
	if err != nil {
		return Compiled{}, err
	}
	columns := []Column{}
	prefix := []string{}
	groups := []string{}
	if p.Viz == "heatmap" {
		prefix = append(prefix, fmt.Sprintf("epoch_ms(time_bucket(INTERVAL '%d seconds',t::TIMESTAMP_NS))::BIGINT AS time", int64(scope.Interval.Seconds())))
		groups = append(groups, "time")
		columns = append(columns, Column{Name: "time", Type: "time", Role: "time"})
	}
	dimSelect := "'' AS dim"
	if len(p.Query.By) > 0 {
		ref, err := sig.field(p.Query.By[0])
		if err != nil {
			return Compiled{}, err
		}
		dimSelect = "coalesce(" + ref.stringSQL() + ",'') AS dim"
		prefix = append(prefix, "dim")
		groups = append(groups, "dim")
		columns = append(columns, Column{Name: ref.alias(), Type: "string", Role: "dimension"})
	}
	unit := "ms"
	raw := ""
	if sig.name == "spans" {
		raw = fmt.Sprintf(`SELECT %s AS t,%s,
CASE WHEN duration_ms<1 THEN 0 ELSE pow(2,least(32,floor(log2(duration_ms)))) END AS lo,
CASE WHEN duration_ms<pow(2,32) THEN CASE WHEN duration_ms<1 THEN 1 ELSE pow(2,floor(log2(duration_ms))+1) END END AS hi,
1::DOUBLE AS n FROM spans WHERE %s AND NOT isnan(duration_ms)`, quoteIdent(sig.time), dimSelect, where)
	} else {
		unit = p.Unit
		raw = metricHistogramRaw(dimSelect, where, p.Query.Histogram.Temporality, scope.Interval, p.Viz == "heatmap")
	}
	columns = append(columns, Column{Name: "bucket_lower", Type: "number", Role: "dimension", Unit: unit}, Column{Name: "bucket_upper", Type: "number", Role: "dimension", Unit: unit}, Column{Name: "count", Type: "number", Role: "measure", Unit: "count"})
	prefix = append(prefix, "lo AS bucket_lower", "hi AS bucket_upper", "sum(n)::DOUBLE AS count")
	groups = append(groups, "lo", "hi")
	source := "raw"
	cte := "WITH raw AS (" + raw + ")"
	if len(p.Query.By) > 0 {
		cte += fmt.Sprintf(",top AS (SELECT dim FROM raw GROUP BY dim ORDER BY sum(n) DESC,dim LIMIT %d),folded AS (SELECT * REPLACE (CASE WHEN dim IN (SELECT dim FROM top) THEN dim ELSE (SELECT 'Other (' || count(DISTINCT dim)::VARCHAR || ')' FROM raw WHERE dim NOT IN (SELECT dim FROM top)) END AS dim) FROM raw)", p.Top())
		source = "folded"
	}
	text := cte + " SELECT " + strings.Join(prefix, ",") + " FROM " + source + " GROUP BY " + strings.Join(groups, ",")
	order := "bucket_lower NULLS FIRST,bucket_upper NULLS LAST"
	if p.Viz == "heatmap" {
		order = "time DESC," + order
	}
	if len(p.Query.By) > 0 {
		order = "dim," + order
	}
	n := distributionRows(p, len(columns))
	text += " ORDER BY " + order + fmt.Sprintf(" LIMIT %d", n+1)
	return Compiled{SQL: text, Args: args, Columns: columns}, nil
}

func metricHistogramRaw(dimSelect, where, temporality string, interval time.Duration, heat bool) string {
	period := "0::BIGINT"
	if heat {
		period = fmt.Sprintf("epoch_ms(time_bucket(INTERVAL '%d seconds',time::TIMESTAMP_NS))", int64(interval/time.Second))
	}
	delta := "FALSE"
	if temporality == "delta" {
		delta = "TRUE"
	}
	// Keep the reset clamp defensively; the final n>0 also filters negatives.
	return fmt.Sprintf(`WITH points AS (
 SELECT *,%s,%s AS period,
 TRY(from_json(hist_bounds_json,'["DOUBLE"]')) AS bounds,
 TRY(from_json(hist_counts_json,'["UBIGINT"]')) AS counts
 FROM metrics WHERE %s AND type='histogram'), expanded AS (
 SELECT namespace,service,name,unit,scope_name,scope_version,attributes,resource,dim,period,time AS t,ingested_unix_nano,
 CASE WHEN i=1 THEN NULL ELSE bounds[i-1] END AS lo,
 CASE WHEN i<=len(bounds) THEN bounds[i] END AS hi,n::DOUBLE AS n
 FROM points,UNNEST(counts) WITH ORDINALITY AS u(n,i)
 WHERE bounds IS NOT NULL AND counts IS NOT NULL AND len(counts)=len(bounds)+1), increases AS (
 SELECT dim,period,lo,hi,max(t) AS t,
 CASE WHEN %s THEN sum(n) ELSE greatest(0,arg_max(n,(t,ingested_unix_nano))-arg_min(n,(t,ingested_unix_nano))) END::DOUBLE AS n
 FROM expanded GROUP BY namespace,service,name,unit,scope_name,scope_version,attributes,resource,dim,period,lo,hi)
 SELECT t,dim,lo,hi,n FROM increases WHERE n>0`, dimSelect, period, where, delta)
}

func boundAnalysisFrame(f *Frame) {
	if f == nil || len(f.Columns) == 0 || f.Columns[0].Role != "time" || f.Rows == 0 {
		return
	}
	order := make([]int, f.Rows)
	for i := range order {
		order[i] = i
	}
	sort.SliceStable(order, func(i, j int) bool { return f.Values[0][order[i]].(int64) < f.Values[0][order[j]].(int64) })
	for c := range f.Values {
		values := make([]any, len(order))
		for r, index := range order {
			values[r] = f.Values[c][index]
		}
		f.Values[c] = values
	}
	cut := 0
	if f.Truncated {
		oldest := f.Values[0][0]
		for cut < f.Rows && f.Values[0][cut] == oldest {
			cut++
		}
	}
	times := []int{}
	for r := cut; r < f.Rows; r++ {
		if r == cut || f.Values[0][r] != f.Values[0][r-1] {
			times = append(times, r)
		}
	}
	if len(times) > maxSeriesPoints {
		cut = times[len(times)-maxSeriesPoints]
		f.Truncated = true
	}
	if cut > 0 {
		for c := range f.Values {
			f.Values[c] = f.Values[c][cut:]
		}
		f.Rows -= cut
	}
}
