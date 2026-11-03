package query

import (
	"fmt"
	"strings"
	"time"
)

func rollupDimension(name string) bool {
	switch name {
	case "namespace", "service", "kind", "status":
		return true
	}
	return false
}

// rollupPlan accepts only aggregates whose complete population can be represented
// by minute summaries. Unchecked filters have unknown dependencies and stay raw.
func rollupPlan(p *Panel, measures []Measure, filters []Filter, scope Scope) bool {
	if p.Query == nil || p.Query.From != "spans" || len(measures) == 0 {
		return false
	}
	switch p.Viz {
	case "stat", "gauge", "timeseries", "bar", "table":
	default:
		return false
	}
	for _, f := range filters {
		if scope.dropped(f) {
			continue
		}
		if f.Columns == nil {
			return false
		}
		for _, c := range f.Columns {
			if !rollupDimension(c) {
				return false
			}
		}
	}
	for _, by := range p.Query.By {
		if !rollupDimension(by) {
			return false
		}
	}
	for _, m := range measures {
		switch m.Func {
		case "count", "rate", "error_rate", "share":
		case "avg", "min", "max", "sum", "p50", "p75", "p90", "p95", "p99", "quantile":
			if m.Field == nil || m.Field.Text != "duration_ms" || m.Field.Key != "" {
				return false
			}
		default:
			return false
		}
	}
	return true
}

func compileRollupQuery(p *Panel, measures []Measure, filters []Filter, scope Scope) (Compiled, error) {
	if scope.Interval > 0 {
		scope.Interval = max(scope.Interval, time.Minute)
	}
	return compileAggregateQuery(p, measures, filters, scope, true)
}

// Durations use fixed scale-3 OpenTelemetry exponential buckets. Merge their
// counts, find the rank's bucket, then interpolate linearly within that bucket.
// Positive bucket k covers (2^((k-1)/8), 2^(k/8)]; INT_MIN represents zero.
// The estimate is approximate, bounded by one positive bucket (9.05%).
func rollupQuantileSQL(q float64) string {
	return fmt.Sprintf(`(SELECT CASE WHEN k=-2147483648 THEN 0.0 ELSE
 pow(2.0,(k-1)/8.0)+(pow(2.0,k/8.0)-pow(2.0,(k-1)/8.0))*greatest(0.0,least(1.0,(%[1]s*total-(cum-n))/n)) END
 FROM (SELECT k,n,sum(n) OVER (ORDER BY k) cum,sum(n) OVER () total
 FROM (SELECT e.key k,sum(e.value)::DOUBLE n
 FROM unnest(flatten(list_transform(list(hist), lambda h: map_entries(h)))) entries(e)
 GROUP BY e.key)) cumulative WHERE cum >= %[1]s*total ORDER BY k LIMIT 1)`, sqlFloat(q))
}

func rollupMeasureSQL(m Measure, seconds, partition string) string {
	switch m.Func {
	case "count":
		return "coalesce(sum(spans),0)::DOUBLE"
	case "rate":
		return "coalesce(sum(spans),0) / nullif(" + seconds + ",0)"
	case "error_rate":
		return "100.0 * coalesce(sum(spans) FILTER (WHERE status='STATUS_CODE_ERROR'),0) / nullif(sum(spans),0)"
	case "share":
		return "100.0 * sum(spans) / sum(sum(spans)) OVER (" + partition + ")"
	case "avg":
		return "sum(duration_sum) / nullif(sum(spans),0)"
	case "sum":
		return "sum(duration_sum)::DOUBLE"
	case "min":
		return "min(duration_min)::DOUBLE"
	case "max":
		return "max(duration_max)::DOUBLE"
	default:
		return rollupQuantileSQL(m.Q)
	}
}

func rollupRankingSQL(extra string, m Measure, seconds float64) (string, string, string) {
	value := rollupMeasureSQL(m, sqlFloat(seconds), "")
	samples := "sum(spans)"
	extra = strings.ReplaceAll(extra, "sum(CASE WHEN status = 'STATUS_CODE_ERROR' THEN 1.0 ELSE 0.0 END)", "coalesce(sum(spans) FILTER (WHERE status='STATUS_CODE_ERROR'),0)")
	return value, samples, extra
}

func coveredSecondsSQL(scope Scope, bucket string, totals bool) string {
	end := scope.End
	if !scope.FreshTo.IsZero() && scope.FreshTo.Before(end) {
		end = scope.FreshTo
	}
	startNS, endNS := scope.Start.UnixNano(), end.UnixNano()
	full := sqlFloat(max(0, end.Sub(scope.Start).Seconds()))
	if bucket == "" {
		return full
	}
	covered := fmt.Sprintf("greatest(0.0, (least(%d::BIGINT, epoch_ns(%s)+%d::BIGINT)-greatest(%d::BIGINT,epoch_ns(%s)))/1000000000.0)", endNS, bucket, scope.Interval.Nanoseconds(), startNS, bucket)
	if totals {
		return "CASE WHEN grouping(" + bucket + ")=1 THEN " + full + " ELSE " + covered + " END"
	}
	return covered
}
