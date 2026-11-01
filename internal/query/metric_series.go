package query

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
			if metricHistogramMeasure(m) {
				lookback = metricLookback
			}
			if (m.Func == "last" || m.Func == "sum" || m.Func == "avg") && metricValueMeasure(m) {
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

// metricRead is the relation a builder reads. The zero value reads metrics
// itself under the given filters. A query with several builders reads one
// shared scan that already applied its filters, so each builder only narrows
// it to its own window and kinds; the scan records the earliest window.
type metricRead struct {
	filters []Filter
	scan    string
	start   *time.Time
}

func (r metricRead) where(read Scope) (string, string, []any, error) {
	if r.scan == "" {
		where, args, err := buildWhere(signals["metrics"], r.filters, read)
		return "metrics", where, args, err
	}
	if r.start.IsZero() || read.Start.Before(*r.start) {
		*r.start = read.Start
	}
	where, args, err := buildWhere(signals["metrics"], nil, read)
	return r.scan, where, args, err
}

func metricKindList(kinds []string) string {
	quoted := make([]string, len(kinds))
	for i, kind := range kinds {
		quoted[i] = sqlString(kind)
	}
	return strings.Join(quoted, ",")
}

// An exponential histogram point becomes explicit bounds and counts: the
// negative buckets reversed below zero, the zero bucket (zlo, threshold] with
// zlo the smallest negative double when the threshold is zero, and the
// positive buckets above, each bound base^index with base 2^(2^-scale)
// written as one exponent so powers of two stay exact. Extreme scales and
// offsets overflow or underflow a bound; a bound that is not finite or not
// above every earlier finite bound cannot separate observations, so it is
// dropped and the bucket above it merges into the one below. Each segment
// ascends, so the earlier maximum is the previous one or two bounds, or the
// threshold for positive bounds. Every list operation is linear in buckets.
var (
	expRawSQL = `CASE WHEN exp_scale IS NOT NULL THEN
CASE WHEN len(exp_negative_counts)>0 THEN list_transform(range(len(exp_negative_counts),-1,-1),lambda i:-` + expBound("exp_negative_offset") + `) ELSE []::DOUBLE[] END||
[CASE WHEN exp_zero_threshold=0 THEN -4.9406564584124654e-324 ELSE -exp_zero_threshold END,exp_zero_threshold]||
CASE WHEN len(exp_positive_counts)>0 THEN list_transform(range(len(exp_positive_counts)+1),lambda i:` + expBound("exp_positive_offset") + `) ELSE []::DOUBLE[] END END AS _eb,
CASE WHEN exp_scale IS NOT NULL THEN [0]::UBIGINT[]||
CASE WHEN len(exp_negative_counts)>0 THEN list_reverse(exp_negative_counts)||[0]::UBIGINT[] ELSE [] END||[exp_zero_count,0]||
CASE WHEN len(exp_positive_counts)>0 THEN exp_positive_counts||[0]::UBIGINT[] ELSE [] END END AS _ec,
CASE WHEN len(exp_negative_counts)>0 THEN len(exp_negative_counts)+2 ELSE 1 END AS _ez`
	expKeptSQL = `list_filter(range(1,len(_eb)+1),lambda p:isfinite(_eb[p]) AND NOT coalesce(_eb[p]<=greatest(
CASE WHEN p>1 AND isfinite(_eb[p-1]) THEN _eb[p-1] END,CASE WHEN p>2 AND isfinite(_eb[p-2]) THEN _eb[p-2] END,
CASE WHEN p>_ez+1 THEN exp_zero_threshold END),false)) AS _ek`
	expBoundsSQL = `list_transform(_ek,lambda p:_eb[p])`
	expCountsSQL = `list_transform(range(1,len(_ek)+2),lambda j:
list_sum(_ec[CASE WHEN j>1 THEN _ek[j-1] ELSE 0 END+1:coalesce(_ek[j],len(_eb)+1)])::UBIGINT)`
)

// expBound is bucket offset+i's bound, base^(offset+i) with base 2^(2^-scale).
func expBound(offset string) string {
	return "pow(2.0,(" + offset + "+i)*pow(2.0,-exp_scale::DOUBLE))"
}

// Point-only builders never lag or decode a ten-minute history. Separate SQL
// scopes let callers share a builder without rewriting authored filter text.
func metricPointsCTE(source metricRead, scope Scope, kinds []string, lookback time.Duration) (string, []any, error) {
	if len(kinds) == 0 {
		return "", nil, fmt.Errorf("metric series requires at least one kind")
	}
	read := scope
	read.Start = read.Start.Add(-lookback)
	from, where, args, err := source.where(read)
	if err != nil {
		return "", nil, err
	}
	where += " AND type IN (" + metricKindList(kinds) + ")"
	text := `WITH metric_input AS (SELECT * FROM ` + from + ` WHERE ` + where + `),
      metric_exp_raw AS (SELECT *,` + expRawSQL + ` FROM metric_input),
      metric_exp_kept AS (SELECT *,` + expKeptSQL + ` FROM metric_exp_raw),
      metric_decoded AS (SELECT * EXCLUDE (_eb,_ec,_ez,_ek),
        CASE WHEN type='histogram' AND exp_scale IS NULL THEN hist_bounds WHEN type='histogram' THEN ` + expBoundsSQL + ` END AS _bounds,
        CASE WHEN type='histogram' AND exp_scale IS NULL THEN hist_counts WHEN type='histogram' THEN ` + expCountsSQL + ` END AS _counts
        FROM metric_exp_kept),
      -- OTLP allows a histogram without buckets: it counts toward count, sum,
      -- rate and avg, and its empty bucket list adds nothing to quantiles.
      metric_valid AS (SELECT * FROM metric_decoded WHERE
        (type<>'histogram' AND isfinite(value)) OR
        (type='histogram' AND (hist_sum IS NULL OR isfinite(hist_sum))
         AND (len(_counts)=len(_bounds)+1 OR len(_counts)=0 AND len(_bounds)=0))),
      metric_unique AS (SELECT * EXCLUDE (_retry) FROM (
        SELECT *,row_number() OVER (PARTITION BY ` + metricSeriesKey + `,time ORDER BY
          ingested_unix_nano DESC,value DESC,hist_count DESC,hist_sum DESC,
          _counts DESC,_bounds DESC,start_time DESC NULLS LAST) AS _retry
        FROM metric_valid) WHERE _retry=1),
      metric_points AS (SELECT *,` + metricBucketSQL(scope.Interval) + ` AS _bucket FROM metric_unique)`
	return strings.TrimSpace(text), args, nil
}

func metricSeriesCTE(source metricRead, scope Scope, kinds []string) (string, []any, error) {
	text, args, err := metricPointsCTE(source, scope, kinds, metricLookback)
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
      metric_deltas AS MATERIALIZED (SELECT *,
        CASE WHEN type='counter' THEN CASE WHEN temporality='delta' OR _reset THEN value ELSE value-_previous_value END END::DOUBLE AS increase,
        CASE WHEN type='histogram' THEN CASE WHEN temporality='delta' OR _reset THEN hist_count ELSE hist_count-_previous_count END END::DOUBLE AS count_increase,
        CASE WHEN type='histogram' THEN CASE WHEN temporality='delta' OR _reset THEN hist_sum ELSE hist_sum-_previous_sum END END::DOUBLE AS sum_increase,
        CASE WHEN type='histogram' THEN list_transform(range(1,len(_counts)+1),lambda i:
          CASE WHEN temporality='delta' OR _reset THEN _counts[i]::HUGEINT
               ELSE _counts[i]::HUGEINT-_rebinned_previous[i] END) END AS bound_increases,
        CASE WHEN _elapsed_from<time THEN (epoch_ns(time::TIMESTAMP_NS)::HUGEINT-epoch_ns(_elapsed_from::TIMESTAMP_NS)::HUGEINT)::DOUBLE/1e9 END AS elapsed_s
        FROM metric_reset
        WHERE type IN ('counter','histogram') AND temporality IN ('delta','cumulative')
          AND (temporality='delta' OR _previous_time IS NOT NULL)),
      metric_increases AS (SELECT * FROM metric_deltas
        WHERE time>=?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND time<?::TIMESTAMP_NS::TIMESTAMPTZ_NS)`
	args = append(args, scope.Start.UTC(), scope.End.UTC())
	return strings.TrimSpace(text), args, nil
}

func metricHistogramRaw(dimSelect string, filters []Filter, scope Scope) (string, []any, error) {
	cte, args, err := metricSeriesCTE(metricRead{filters: filters}, scope, []string{"histogram"})
	if err != nil {
		return "", nil, err
	}
	spread, spreadArgs := metricSpreadCTE(scope)
	cte += spread
	args = append(args, spreadArgs...)
	// Zipped select-list unnests avoid the lateral join WITH ORDINALITY plans;
	// bucket k spans (bounds[k-1], bounds[k]], open at both ends.
	timeSQL := "time"
	if scope.Interval > 0 {
		timeSQL = "make_timestamp_ns(_bucket*1000000)::TIMESTAMPTZ_NS"
	}
	text := cte + ` SELECT * FROM (SELECT ` + timeSQL + ` AS t,` + dimSelect + `,_bucket,
      unnest(list_prepend(NULL::DOUBLE,_bounds)) AS lo,
      unnest(list_append(_bounds,NULL::DOUBLE)) AS hi,
      unnest(bound_increases) AS n
      FROM metric_spread WHERE type='histogram') WHERE n>=0`
	return text, args, nil
}

// A series' value at a bucket's exclusive end is its latest point strictly
// before that end, found per series by an ASOF join so the cost follows
// series times buckets rather than points times buckets.
func metricLastCTE(source metricRead, scope Scope, kinds []string) (string, []any, error) {
	read := scope
	if scope.Interval <= 0 {
		read.Start = scope.End
	}
	cte, args, err := metricPointsCTE(source, read, kinds, metricStaleness)
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
