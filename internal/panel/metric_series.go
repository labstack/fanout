package panel

import (
	"fmt"
	"strings"
	"time"
)

const metricLookback = 10 * time.Minute
const metricStaleness = 5 * time.Minute
const metricSeriesKey = "series_id"

func metricReadScope(p *Panel, scope Scope) Scope {
	if p.Query == nil || p.Query.From != "metrics" {
		return scope
	}
	lookback := time.Duration(0)
	if p.Viz == "heatmap" || p.Viz == "histogram" {
		lookback = metricLookback
	} else {
		var ignored Problems
		for _, m := range parseMeasures(signals["metrics"], p.Query.Measures, "", &ignored) {
			if m.Func == "rate" || (m.Func == "sum" && metricValueMeasure(m)) {
				lookback = metricLookback
			}
			if m.Func == "last" && metricValueMeasure(m) {
				lookback = max(lookback, metricStaleness)
			}
		}
	}
	scope.Start = scope.Start.Add(-lookback)
	return scope
}

func metricBucketSQL(interval time.Duration) string {
	if interval <= 0 {
		return "0::BIGINT"
	}
	return fmt.Sprintf("epoch_ms(time_bucket(INTERVAL '%d seconds',time::TIMESTAMP_NS,%s))::BIGINT", int64(interval/time.Second), bucketOrigin)
}

// Point-only builders never lag or decode a ten-minute history. Separate SQL
// scopes let callers share a builder without rewriting authored filter text.
func metricPointsCTE(filters []Filter, scope Scope, kinds []string, lookback time.Duration) (string, []any, error) {
	if len(kinds) == 0 {
		return "", nil, fmt.Errorf("metric series requires at least one kind")
	}
	read := scope
	read.Start = read.Start.Add(-lookback)
	where, args, err := buildWhere(signals["metrics"], filters, read)
	if err != nil {
		return "", nil, err
	}
	quoted := make([]string, len(kinds))
	for i, kind := range kinds {
		quoted[i] = sqlString(kind)
	}
	where += " AND type IN (" + strings.Join(quoted, ",") + ")"
	text := `WITH metric_input AS (SELECT * FROM metrics WHERE ` + where + `),
      metric_decoded AS (SELECT *,
        CASE WHEN type='histogram' THEN TRY(from_json(hist_bounds_json,'["DOUBLE"]')) END AS _bounds,
        CASE WHEN type='histogram' THEN TRY(from_json(hist_counts_json,'["UBIGINT"]')) END AS _counts
        FROM metric_input),
      metric_valid AS (SELECT * FROM metric_decoded WHERE
        (type<>'histogram' AND isfinite(value)) OR
        (type='histogram' AND isfinite(hist_sum) AND hist_count>=0
         AND _bounds IS NOT NULL AND _counts IS NOT NULL
         AND len(_counts)=len(_bounds)+1
         AND coalesce(list_bool_and(list_transform(_bounds,lambda x: isfinite(x))),true)
         AND coalesce(list_bool_and(list_transform(range(2,len(_bounds)+1),lambda i: _bounds[i-1] < _bounds[i])),true))),
      metric_unique AS (SELECT * EXCLUDE (_retry) FROM (
        SELECT *,row_number() OVER (PARTITION BY ` + metricSeriesKey + `,time ORDER BY
          ingested_unix_nano DESC,value DESC,hist_count DESC,hist_sum DESC,
          hist_counts_json DESC,hist_bounds_json DESC,start_time DESC NULLS LAST) AS _retry
        FROM metric_valid) WHERE _retry=1),
      metric_points AS (SELECT *,` + metricBucketSQL(scope.Interval) + ` AS _bucket FROM metric_unique)`
	return strings.TrimSpace(text), args, nil
}

func metricSeriesCTE(filters []Filter, scope Scope, kinds []string) (string, []any, error) {
	text, args, err := metricPointsCTE(filters, scope, kinds, metricLookback)
	if err != nil {
		return "", nil, err
	}
	// Re-bin only whole previous buckets into the current layout. Unassigned
	// observations or negative differences reset all channels together.
	text += `,
      metric_previous AS (SELECT *,
        lag(time) OVER w AS _previous_time,
        lag(value) OVER w AS _previous_value,
        lag(start_time) OVER w AS _previous_start,
        lag(hist_count) OVER w AS _previous_count,
        lag(hist_sum) OVER w AS _previous_sum,
        lag(_bounds) OVER w AS _previous_bounds,
        lag(_counts) OVER w AS _previous_counts
        FROM metric_points WINDOW w AS (PARTITION BY ` + metricSeriesKey + ` ORDER BY time,ingested_unix_nano)),
      metric_rebinned AS (SELECT *,
        CASE WHEN type='histogram' AND temporality='cumulative' AND _bounds<>_previous_bounds THEN
          list_transform(range(1,len(_counts)+1),lambda j:
            list_sum(list_transform(range(1,len(_previous_counts)+1),lambda i:
              CASE WHEN coalesce(_bounds[j-1],'-Infinity'::DOUBLE)<=coalesce(_previous_bounds[i-1],'-Infinity'::DOUBLE)
                     AND coalesce(_previous_bounds[i],'Infinity'::DOUBLE)<=coalesce(_bounds[j],'Infinity'::DOUBLE)
                   THEN _previous_counts[i]::HUGEINT ELSE 0::HUGEINT END)))
        ELSE list_transform(_previous_counts,lambda n: n::HUGEINT) END AS _rebinned_previous
        FROM metric_previous),
      metric_reset AS (SELECT *,
        ((start_time IS NOT NULL AND _previous_start IS NOT NULL AND start_time<>_previous_start)
         OR CASE WHEN type='histogram' THEN hist_count<_previous_count OR
                   (_bounds<>_previous_bounds AND
                     list_sum(_rebinned_previous)<>list_sum(list_transform(_previous_counts,lambda n: n::HUGEINT)))
                   OR NOT list_bool_and(list_transform(range(1,len(_counts)+1),lambda i:
                     _counts[i]::HUGEINT-_rebinned_previous[i]>=0))
                 ELSE value<_previous_value END) AS _reset,
        CASE WHEN temporality='delta' AND start_time IS NOT NULL AND start_time<time THEN start_time
             ELSE _previous_time END AS _elapsed_from
        FROM metric_rebinned),
      metric_increases AS (SELECT *,
        CASE WHEN type='counter' THEN CASE WHEN temporality='delta' OR _reset THEN value ELSE value-_previous_value END END::DOUBLE AS increase,
        CASE WHEN type='histogram' THEN CASE WHEN temporality='delta' OR _reset THEN hist_count ELSE hist_count-_previous_count END END::DOUBLE AS count_increase,
        CASE WHEN type='histogram' THEN CASE WHEN temporality='delta' OR _reset THEN hist_sum ELSE hist_sum-_previous_sum END END::DOUBLE AS sum_increase,
        CASE WHEN type='histogram' THEN list_transform(range(1,len(_counts)+1),lambda i:
          CASE WHEN temporality='delta' OR _reset THEN _counts[i]::HUGEINT
               ELSE _counts[i]::HUGEINT-_rebinned_previous[i] END) END AS bound_increases,
        CASE WHEN _elapsed_from<time THEN (epoch_ns(time::TIMESTAMP_NS)::HUGEINT-epoch_ns(_elapsed_from::TIMESTAMP_NS)::HUGEINT)::DOUBLE/1e9 END AS elapsed_s
        FROM metric_reset
        WHERE time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS
          AND type IN ('counter','histogram') AND temporality IN ('delta','cumulative')
          AND (temporality='delta' OR _previous_time IS NOT NULL))`
	args = append(args, scope.Start.UTC(), scope.End.UTC())
	return strings.TrimSpace(text), args, nil
}

func metricHistogramRaw(dimSelect string, filters []Filter, scope Scope) (string, []any, error) {
	cte, args, err := metricSeriesCTE(filters, scope, []string{"histogram"})
	if err != nil {
		return "", nil, err
	}
	text := cte + ` SELECT time AS t,` + dimSelect + `,_bucket,
      CASE WHEN i=1 THEN NULL ELSE _bounds[i-1] END AS lo,
      CASE WHEN i<=len(_bounds) THEN _bounds[i] END AS hi,
      n AS n
      FROM metric_increases,UNNEST(bound_increases) WITH ORDINALITY AS u(n,i)
      WHERE type='histogram' AND n>=0`
	return text, args, nil
}

// A series' value at a bucket's exclusive end is its latest point strictly
// before that end, found per series by an ASOF join so the cost follows
// series times buckets rather than points times buckets.
func metricLastCTE(filters []Filter, scope Scope, kinds []string) (string, []any, error) {
	read := scope
	if scope.Interval <= 0 {
		read.Start = scope.End
	}
	cte, args, err := metricPointsCTE(filters, read, kinds, metricStaleness)
	if err != nil {
		return "", nil, err
	}
	ends := "SELECT 0::BIGINT AS _bucket,?::TIMESTAMP_NS::TIMESTAMPTZ_NS AS _at"
	if scope.Interval > 0 {
		lo := alignedBucketMillis(scope.Start, scope.Interval)
		hi := alignedBucketMillis(scope.End.Add(-time.Nanosecond), scope.Interval)
		ends = fmt.Sprintf("SELECT b AS _bucket,least(make_timestamp_ns((b+%d)*1000000)::TIMESTAMPTZ_NS,?::TIMESTAMP_NS::TIMESTAMPTZ_NS) AS _at FROM generate_series(%d::BIGINT,%d::BIGINT,%d::BIGINT) AS buckets(b)", scope.Interval.Milliseconds(), lo, hi, scope.Interval.Milliseconds())
	}
	args = append(args, scope.End.UTC())
	cte += `, metric_bucket_ends AS (` + ends + `),
        metric_grid AS (SELECT s.series_id AS _sid,b._bucket AS _end_bucket,b._at FROM (SELECT DISTINCT series_id FROM metric_points) s CROSS JOIN metric_bucket_ends b),
        metric_last_points AS (SELECT p.* EXCLUDE (_bucket),g._end_bucket AS _bucket FROM metric_grid g ASOF JOIN metric_points p
          ON g._sid=p.series_id AND p.time<g._at
          WHERE p.time>=make_timestamp_ns(epoch_ns(g._at::TIMESTAMP_NS)-300000000000)::TIMESTAMPTZ_NS)`
	return cte, args, nil
}
