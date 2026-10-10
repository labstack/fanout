package panel

import (
	"fmt"
	"time"
)

// Bucket arithmetic stays in signed integer nanoseconds, including clipping
// partial buckets and excluding the interval's end from the next bucket.
func metricBucketIndexSQL(nanos string, interval time.Duration) string {
	if interval <= 0 {
		return "0::BIGINT"
	}
	n := interval.Nanoseconds()
	return fmt.Sprintf("((%s)//%d-CASE WHEN (%s)%%%d<0 THEN 1 ELSE 0 END)::BIGINT", nanos, n, nanos, n)
}

func metricBucketWindowCTE(scope Scope) (string, []any) {
	start := "_window_start"
	end := "_window_end"
	bucket := "0::BIGINT"
	if scope.Interval > 0 {
		start = fmt.Sprintf("greatest(_window_start,i*%d)", scope.Interval.Nanoseconds())
		end = fmt.Sprintf("least(_window_end,(i+1)*%d)", scope.Interval.Nanoseconds())
		bucket = fmt.Sprintf("i*%d", scope.Interval.Milliseconds())
	}
	return `, metric_window AS (SELECT epoch_ns(?::TIMESTAMP_NS) AS _window_start,
        epoch_ns(?::TIMESTAMP_NS) AS _window_end),
      metric_bucket_starts AS (SELECT ` + bucket + ` AS _bucket,` + start + ` AS _start_ns,` + end + ` AS _end_ns
        FROM metric_window,unnest(range(` + metricBucketIndexSQL("_window_start", scope.Interval) + `,` +
		metricBucketIndexSQL("_window_end-1", scope.Interval) + `+1)) AS indexes(i))`, []any{scope.Start.UTC(), scope.End.UTC()}
}

// Each delta expands only into its overlapped bucket indexes. There is no
// deltas-by-buckets range join. Interval-less deltas retain point attribution.
func metricSpreadCTE(scope Scope) (string, []any) {
	cte, args := metricBucketWindowCTE(scope)
	start, end := fmt.Sprint(scope.Start.UnixNano()), fmt.Sprint(scope.End.UnixNano())
	bucket, lo, hi := "0::BIGINT", "_lo", "_hi"
	if scope.Interval > 0 {
		bucket = fmt.Sprintf("i*%d", scope.Interval.Milliseconds())
		lo = fmt.Sprintf("greatest(_lo,i*%d)", scope.Interval.Nanoseconds())
		hi = fmt.Sprintf("least(_hi,(i+1)*%d)", scope.Interval.Nanoseconds())
	}
	indexes := `CASE WHEN elapsed_s IS NULL THEN [` + metricBucketIndexSQL("epoch_ns(time::TIMESTAMP_NS)", scope.Interval) + `]
        ELSE range(` + metricBucketIndexSQL("_lo", scope.Interval) + `,` + metricBucketIndexSQL("_hi-1", scope.Interval) + `+1) END`
	// A stat has exactly one bucket; no per-delta list expansion is needed.
	expansion := ",unnest(" + indexes + ") AS i"
	if scope.Interval <= 0 {
		expansion = ""
	}
	cte += `,
      metric_intervals AS (SELECT *,greatest(epoch_ns(_elapsed_from::TIMESTAMP_NS),` + start + `) AS _lo,
        least(epoch_ns(time::TIMESTAMP_NS),` + end + `) AS _hi
        FROM metric_deltas WHERE (elapsed_s>0 AND time>make_timestamp_ns(` + start + `)::TIMESTAMPTZ_NS)
          OR (elapsed_s IS NULL AND time>=make_timestamp_ns(` + start + `)::TIMESTAMPTZ_NS)),
      metric_interval_buckets AS (SELECT *` + expansion + ` FROM metric_intervals WHERE elapsed_s IS NULL OR _hi>_lo),
      metric_overlaps AS (SELECT *,` + bucket + ` AS _spread_bucket,
        CASE WHEN elapsed_s>0 THEN (` + hi + `-` + lo + `)::DOUBLE/1e9 END AS _covered
        FROM metric_interval_buckets),
      metric_spread AS MATERIALIZED (SELECT * EXCLUDE (_bucket,increase,count_increase,sum_increase,bound_increases,elapsed_s),
        _spread_bucket AS _bucket,CASE WHEN elapsed_s IS NULL THEN increase ELSE increase*_covered/elapsed_s END AS increase,
        CASE WHEN elapsed_s IS NULL THEN count_increase ELSE count_increase*_covered/elapsed_s END AS count_increase,
        CASE WHEN elapsed_s IS NULL THEN sum_increase ELSE sum_increase*_covered/elapsed_s END AS sum_increase,
        CASE WHEN elapsed_s IS NULL THEN list_transform(bound_increases,lambda n: n::DOUBLE)
          ELSE list_transform(bound_increases,lambda n: n::DOUBLE*_covered/elapsed_s) END AS bound_increases,
        _covered AS elapsed_s FROM metric_overlaps WHERE elapsed_s IS NULL OR _covered>0)`
	return cte, args
}

// Only ends of covered runs (and a lookback seed) can start a carry. Expand
// those anchors for at most five minutes, then remove covered keys. The work
// follows gaps in coverage, never a series-by-window grid. Multiple nearby
// gaps choose the latest completed point for each uncovered key.
func metricCarryCandidatesCTE(scope Scope, points, covered, eligible string, inclusive bool) string {
	start := fmt.Sprint(scope.Start.UnixNano())
	end := fmt.Sprint(scope.End.UnixNano())
	index, bucket, step := "0::BIGINT", "0::BIGINT", "1"
	if scope.Interval > 0 {
		index = fmt.Sprintf("_bucket//%d", scope.Interval.Milliseconds())
		bucket = fmt.Sprintf("i*%d", scope.Interval.Milliseconds())
		step = fmt.Sprint(scope.Interval.Milliseconds())
	}
	comparison := "<"
	if inclusive {
		comparison = "<="
	}
	return `, metric_covered AS MATERIALIZED (SELECT series_id,_bucket,` + index + ` AS _index,
        max(epoch_ns(time::TIMESTAMP_NS)) AS _last_ns FROM ` + covered + ` WHERE ` + eligible + ` GROUP BY series_id,_bucket),
      metric_carry_anchors AS (SELECT series_id,_index,_last_ns FROM metric_covered c
        WHERE NOT EXISTS (SELECT 1 FROM metric_covered n WHERE n.series_id=c.series_id AND n._bucket=c._bucket+` + step + `)
        UNION ALL SELECT series_id,` + metricBucketIndexSQL(start, scope.Interval) + `-1 AS _index,
          max(epoch_ns(time::TIMESTAMP_NS)) AS _last_ns FROM ` + points + `
        WHERE ` + eligible + ` AND time` + comparison + `make_timestamp_ns(` + start + `)::TIMESTAMPTZ_NS
          AND time>=make_timestamp_ns(` + start + `-300000000000)::TIMESTAMPTZ_NS
        GROUP BY series_id),
      metric_carry_indexes AS (SELECT series_id,_last_ns,unnest(range(_index+1,
        least(` + metricBucketIndexSQL(end+"-1", scope.Interval) + `,` +
		metricBucketIndexSQL("_last_ns+300000000000", scope.Interval) + `)+1)) AS i
        FROM metric_carry_anchors),
      metric_carry_candidates AS (SELECT c.series_id AS _sid,` + bucket + ` AS _bucket,max(_last_ns) AS _last_ns
        FROM metric_carry_indexes c JOIN metric_bucket_starts b ON b._bucket=` + bucket + `
        WHERE _last_ns` + comparison + `b._start_ns AND _last_ns>=b._start_ns-300000000000
          AND NOT EXISTS (SELECT 1 FROM metric_covered p WHERE p.series_id=c.series_id AND p._bucket=b._bucket)
        GROUP BY c.series_id,b._bucket,i)`
}

// Rates alone may extend a completed delta. Covered buckets win outright;
// a delta ending exactly on the boundary covers no new duration.
func metricRateCarryCTE(scope Scope) string {
	return metricCarryCandidatesCTE(scope, "metric_deltas", "metric_spread", "elapsed_s>0", true) + `,
      metric_rate_carry AS (SELECT p.* EXCLUDE (_bucket,increase,count_increase,sum_increase,bound_increases,elapsed_s),
        g._bucket,NULL::DOUBLE AS increase,NULL::DOUBLE AS count_increase,NULL::DOUBLE AS sum_increase,
        NULL::DOUBLE[] AS bound_increases,NULL::DOUBLE AS elapsed_s,
        CASE WHEN type='histogram' THEN p.count_increase ELSE p.increase END/p.elapsed_s AS _rate_share,
        1.0::DOUBLE AS _rate_covered,true AS _carry
        FROM metric_carry_candidates g JOIN metric_deltas p
          ON g._sid=p.series_id AND make_timestamp_ns(g._last_ns)::TIMESTAMPTZ_NS=p.time),
      metric_rate_rows AS (SELECT *,CASE WHEN type='histogram' THEN count_increase ELSE increase END AS _rate_share,
        elapsed_s AS _rate_covered,false AS _carry FROM metric_spread
        UNION ALL BY NAME SELECT * FROM metric_rate_carry)`
}

func metricGaugeCTE(filters []Filter, scope Scope, kinds []string) (string, []any, error) {
	cte, args, err := metricPointsCTE(filters, scope, kinds, metricStaleness)
	if err != nil {
		return "", nil, err
	}
	window, windowArgs := metricBucketWindowCTE(scope)
	cte += window + fmt.Sprintf(`, metric_visible_points AS (SELECT p.* FROM metric_points p
        WHERE time>=make_timestamp_ns(%d)::TIMESTAMPTZ_NS)`, scope.Start.UnixNano())
	cte += metricCarryCandidatesCTE(scope, "metric_points", "metric_visible_points", "true", false) + `,
      metric_gauge_carry AS (SELECT p.* EXCLUDE (_bucket),g._bucket,true AS _carry
        FROM metric_carry_candidates g JOIN metric_points p
          ON g._sid=p.series_id AND make_timestamp_ns(g._last_ns)::TIMESTAMPTZ_NS=p.time),
      metric_gauge_rows AS (SELECT *,false AS _carry FROM metric_visible_points UNION ALL BY NAME SELECT * FROM metric_gauge_carry)`
	args = append(args, windowArgs...)
	return cte, args, nil
}
