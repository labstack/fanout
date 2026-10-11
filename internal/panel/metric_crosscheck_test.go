//go:build readbench

package panel

import (
	"fmt"
	"math"
	"os"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

func metricCrosscheckDashboard(name, measure string, start, end time.Time) Dashboard {
	var by []string
	switch measure {
	case "rate()":
		by = []string{"attributes['direction']"}
	case "last(value)":
		by = []string{"attributes['state']"}
	}
	return Dashboard{Name: "Metric cross-check", Time: Time{From: &start, To: &end}, Panels: []Panel{{
		ID: "metric", Title: "Metric", Viz: "timeseries", Query: &Query{From: "metrics",
			Where: []string{"name = " + sqlString(name)}, Measures: []string{measure}, By: by, Bucket: "1m"},
	}}}
}

// Independent SQL over the public metrics view. The four binds are read start,
// visible end, metric name and visible start. No panel SQL renderer is used.
// Identity and aggregation order follow the stored series_id contract.
func metricOracleSQL(measure string) string {
	base := `WITH settings AS (
      SELECT ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AS read_start,
        ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AS visible_end,?::VARCHAR AS metric_name,
        ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AS visible_start),
      decoded AS (
        SELECT m.*,TRY(from_json(hist_bounds_json,'["DOUBLE"]')) AS bounds,
          TRY(from_json(hist_counts_json,'["UBIGINT"]')) AS counts
        FROM metrics m,settings s
        WHERE time>=s.read_start AND time<s.visible_end AND name=s.metric_name AND `
	switch measure {
	case "rate()":
		base += `type='counter' AND isfinite(value))`
	case "last(value)":
		base += `type IN ('gauge','updown','counter') AND isfinite(value))`
	case "p95(value)":
		base += `type='histogram' AND isfinite(hist_sum) AND hist_count>=0),
      valid_histograms AS (
        SELECT * FROM decoded WHERE bounds IS NOT NULL AND counts IS NOT NULL
          AND len(counts)=len(bounds)+1
          AND coalesce(list_bool_and(list_transform(bounds,lambda b: isfinite(b))),true)
          AND coalesce(list_bool_and(list_transform(range(2,len(bounds)+1),lambda i: bounds[i]>bounds[i-1])),true))`
	default:
		panic("unsupported cross-check measure")
	}
	source := "decoded"
	if measure == "p95(value)" {
		source = "valid_histograms"
	}
	base += `, samples AS (
        SELECT * EXCLUDE (retry) FROM (
          SELECT *,row_number() OVER (PARTITION BY series_id,time ORDER BY
            ingested_unix_nano DESC,value DESC,hist_count DESC,hist_sum DESC,
            hist_counts_json DESC,hist_bounds_json DESC,start_time DESC NULLS LAST) AS retry
          FROM ` + source + `) WHERE retry=1)`
	if measure == "last(value)" {
		// Enumerate bucket ends and rank eligible points with a normal join and
		// window function, independently of production's ASOF join.
		return base + `, limits AS (
          SELECT *,epoch_ns(visible_start::TIMESTAMP_NS) AS lo,
            epoch_ns(visible_end::TIMESTAMP_NS)-1 AS hi FROM settings),
        ends AS (
          SELECT b AS bucket,
            least(make_timestamp_ns((b+60000)*1000000)::TIMESTAMPTZ_NS,visible_end) AS bucket_end
          FROM limits,generate_series(
            (lo//60000000000-CASE WHEN lo%60000000000<0 THEN 1 ELSE 0 END)*60000,
            (hi//60000000000-CASE WHEN hi%60000000000<0 THEN 1 ELSE 0 END)*60000,
            60000::BIGINT) AS buckets(b)),
        latest AS (
          SELECT bucket,series_id,value,coalesce(TRY_CAST(attributes['state'] AS VARCHAR),'') AS dimension,
            row_number() OVER (PARTITION BY bucket,series_id ORDER BY time DESC,ingested_unix_nano DESC) AS recency
          FROM ends JOIN samples ON time<bucket_end
            AND time>=make_timestamp_ns(epoch_ns(bucket_end::TIMESTAMP_NS)-300000000000)::TIMESTAMPTZ_NS)
        SELECT bucket,dimension,sum(value)::DOUBLE AS value FROM latest WHERE recency=1
        GROUP BY bucket,dimension ORDER BY 1,2`
	}
	base += `, ordered AS (
        SELECT *,lag(value) OVER w AS previous_value,lag(time) OVER w AS previous_time,
          lag(start_time) OVER w AS previous_start,lag(hist_count) OVER w AS previous_count,
          lag(bounds) OVER w AS previous_bounds,lag(counts) OVER w AS previous_counts
        FROM samples WINDOW w AS (PARTITION BY series_id ORDER BY time,ingested_unix_nano)),
      intervals AS (
        SELECT *,epoch_ms(time_bucket(INTERVAL '1 minute',time::TIMESTAMP_NS,'1970-01-01'::TIMESTAMP_NS))::BIGINT AS point_bucket,
          (start_time IS NOT NULL AND previous_start IS NOT NULL AND start_time<>previous_start) AS restarted,
          CASE WHEN temporality='delta' AND start_time IS NOT NULL AND start_time<time THEN start_time
            ELSE previous_time END AS covered_from
        FROM ordered WHERE temporality IN ('delta','cumulative') AND (temporality='delta' OR previous_time IS NOT NULL)),
      steps AS (
        SELECT *,CASE WHEN covered_from<time THEN
          (epoch_ns(time::TIMESTAMP_NS)::HUGEINT-epoch_ns(covered_from::TIMESTAMP_NS)::HUGEINT)::DOUBLE/1e9 END AS elapsed
        FROM intervals),
      limits AS (
        SELECT *,epoch_ns(visible_start::TIMESTAMP_NS) AS lo,
          epoch_ns(visible_end::TIMESTAMP_NS) AS hi FROM settings),
      buckets AS (
        SELECT b AS bucket,greatest(lo,b*1000000) AS bucket_start,
          least(hi,(b+60000)*1000000) AS bucket_end
        FROM limits,generate_series(
          (lo//60000000000-CASE WHEN lo%60000000000<0 THEN 1 ELSE 0 END)*60000,
          ((hi-1)//60000000000-CASE WHEN (hi-1)%60000000000<0 THEN 1 ELSE 0 END)*60000,
          60000::BIGINT) AS indexes(b))`
	if measure == "rate()" {
		// Ordinary joins and a recency rank keep this independent of the
		// compiler's index expansion and ASOF carry implementation.
		// Gaps over 600 seconds enter as whole point shares with their full
		// elapsed time; only shorter intervals participate in overlap joins.
		return base + `, deltas AS (
          SELECT *,CASE WHEN temporality='delta' OR restarted OR value<previous_value THEN value
            ELSE value-previous_value END AS increase FROM steps WHERE elapsed>0),
        interval_shares AS (
          SELECT d.*,b.bucket,
            (least(epoch_ns(time::TIMESTAMP_NS),bucket_end)-greatest(epoch_ns(covered_from::TIMESTAMP_NS),bucket_start))::DOUBLE/1e9 AS covered
          FROM deltas d JOIN buckets b ON elapsed<=600 AND epoch_ns(time::TIMESTAMP_NS)>bucket_start
            AND epoch_ns(covered_from::TIMESTAMP_NS)<bucket_end),
        shares AS (
          SELECT bucket,series_id,time,ingested_unix_nano,attributes,
            increase*covered/elapsed AS share,covered FROM interval_shares WHERE covered>0
          UNION ALL SELECT point_bucket,series_id,time,ingested_unix_nano,attributes,increase,elapsed
          FROM deltas,settings WHERE elapsed>600 AND time>=visible_start),
        recent AS (
          SELECT d.*,b.bucket,row_number() OVER (PARTITION BY bucket,series_id ORDER BY time DESC) AS recency
          FROM buckets b JOIN deltas d ON epoch_ns(time::TIMESTAMP_NS)<=bucket_start
            AND epoch_ns(time::TIMESTAMP_NS)>=bucket_start-300000000000),
        rate_rows AS (
          SELECT * FROM shares UNION ALL BY NAME
          SELECT bucket,series_id,time,ingested_unix_nano,attributes,increase/elapsed AS share,1.0 AS covered
          FROM recent r WHERE recency=1 AND NOT EXISTS (SELECT 1 FROM shares s WHERE s.bucket=r.bucket AND s.series_id=r.series_id)),
        series_rates AS (
          SELECT bucket,series_id,coalesce(TRY_CAST(attributes['direction'] AS VARCHAR),'') AS dimension,
            sum(share ORDER BY time,ingested_unix_nano)/sum(covered ORDER BY time,ingested_unix_nano) AS rate
          FROM rate_rows GROUP BY bucket,series_id,dimension)
        SELECT bucket,dimension,sum(rate ORDER BY series_id)::DOUBLE AS value
        FROM series_rates GROUP BY bucket,dimension ORDER BY 1,2`
	}
	// Match whole old intervals to their containing new interval with an SQL
	// subquery. Lost intervals and any decreasing bin reset the entire point.
	// HUGEINT subtraction/sums preserve integer weights until interpolation.
	return base + `, bins AS (
        SELECT s.*,i,CASE WHEN i<=len(bounds) THEN bounds[i] END AS upper_bound,
          current_n::HUGEINT AS current_weight,
          coalesce((SELECT sum(old_n::HUGEINT) FROM UNNEST(previous_counts) WITH ORDINALITY AS old(old_n,j)
            WHERE coalesce(bounds[i-1],'-Infinity'::DOUBLE)<=coalesce(previous_bounds[j-1],'-Infinity'::DOUBLE)
              AND coalesce(previous_bounds[j],'Infinity'::DOUBLE)<=coalesce(bounds[i],'Infinity'::DOUBLE)),0::HUGEINT) AS old_weight
        FROM steps s,UNNEST(counts) WITH ORDINALITY AS current(current_n,i)),
      resets AS (
        SELECT *,restarted OR hist_count<previous_count
          OR sum(old_weight) OVER point<>list_sum(list_transform(previous_counts,lambda n: n::HUGEINT))
          OR bool_or(current_weight<old_weight) OVER point AS reset
        FROM bins WINDOW point AS (PARTITION BY series_id,time)),
      increases AS (
        SELECT *,CASE WHEN temporality='delta' OR reset THEN current_weight
          ELSE current_weight-old_weight END AS n FROM resets),
      interval_shares AS (
        SELECT b.bucket,i.upper_bound,
          i.n::DOUBLE*((least(epoch_ns(time::TIMESTAMP_NS),bucket_end)-greatest(epoch_ns(covered_from::TIMESTAMP_NS),bucket_start))::DOUBLE/1e9)/elapsed AS n
        FROM increases i JOIN buckets b ON elapsed>0 AND elapsed<=600 AND epoch_ns(time::TIMESTAMP_NS)>bucket_start
          AND epoch_ns(covered_from::TIMESTAMP_NS)<bucket_end
        UNION ALL SELECT point_bucket,upper_bound,n::DOUBLE FROM increases,settings
          WHERE (elapsed IS NULL OR elapsed>600) AND time>=visible_start),
      merged AS (
        SELECT bucket,upper_bound,sum(n)::DOUBLE AS n FROM interval_shares WHERE n>=0 GROUP BY bucket,upper_bound),
      cumulative AS (
        SELECT *,sum(n) OVER (PARTITION BY bucket ORDER BY upper_bound NULLS LAST ROWS BETWEEN UNBOUNDED PRECEDING AND UNBOUNDED FOLLOWING) AS total,
          sum(n) OVER (PARTITION BY bucket ORDER BY upper_bound NULLS LAST ROWS UNBOUNDED PRECEDING) AS seen,
          lag(upper_bound) OVER (PARTITION BY bucket ORDER BY upper_bound NULLS LAST) AS lower_bound,
          max(upper_bound) OVER (PARTITION BY bucket) AS largest FROM merged),
      picked AS (
        SELECT *,row_number() OVER (PARTITION BY bucket ORDER BY seen,upper_bound NULLS LAST) AS rank
        FROM cumulative WHERE n>0 AND seen>=0.95*total)
      SELECT bucket,'' AS dimension,CASE WHEN total<=0 THEN NULL WHEN upper_bound IS NULL THEN largest
        WHEN lower_bound IS NULL AND upper_bound<=0 THEN upper_bound
        ELSE coalesce(lower_bound,0.0)+(0.95*total-(seen-n))/n*(upper_bound-coalesce(lower_bound,0.0))
        END::DOUBLE AS value FROM picked WHERE rank=1 ORDER BY 1`
}

type metricCrosscheckCell struct {
	bucket    int64
	dimension string
}

func crosscheckMetricFrame(t *testing.T, engine Engine, name, measure string, start, end time.Time) map[metricCrosscheckCell]float64 {
	t.Helper()
	e := NewExecutor(engine, 30)
	e.now = func() time.Time { return end }
	results, err := e.Run(t.Context(), RunRequest{Dashboard: metricCrosscheckDashboard(name, measure, start, end)})
	if err != nil {
		t.Fatal("cross-check panel execution failed")
	}
	if len(results) != 1 || results[0].Status != StatusOK || results[0].Frame == nil {
		t.Fatal("cross-check needs one populated successful panel")
	}
	alias, dimension := "p95", ""
	if measure == "rate()" {
		alias, dimension = "rate", "direction"
	} else if measure == "last(value)" {
		alias, dimension = "last", "state"
	}
	r := results[0]
	times, values := metricValues(t, r, "time"), metricValues(t, r, alias)
	var dimensions []any
	if dimension != "" {
		dimensions = metricValues(t, r, dimension)
	}
	actual := map[metricCrosscheckCell]float64{}
	seen := map[metricCrosscheckCell]bool{}
	for i, v := range values {
		cell := metricCrosscheckCell{bucket: times[i].(int64)}
		if dimension != "" {
			cell.dimension = dimensions[i].(string)
		}
		if seen[cell] {
			t.Fatalf("duplicate panel bucket %d", cell.bucket)
		}
		seen[cell] = true
		if v != nil {
			actual[cell] = v.(float64)
		}
	}
	lookback := 10 * time.Minute
	if measure == "last(value)" {
		lookback = 5 * time.Minute
	}
	readStart := start.Add(-lookback)
	rows, err := engine.QueryContext(queryrows.WithWindow(t.Context(), queryrows.Window{Start: readStart, End: end}),
		metricOracleSQL(measure), readStart.UTC(), end.UTC(), name, start.UTC())
	if err != nil {
		t.Fatalf("independent oracle failed: %v", err)
	}
	defer rows.Close()
	expected := map[metricCrosscheckCell]float64{}
	for rows.Next() {
		var cell metricCrosscheckCell
		var value any
		if err := rows.Scan(&cell.bucket, &cell.dimension, &value); err != nil {
			t.Fatal(err)
		}
		if value != nil {
			if _, duplicate := expected[cell]; duplicate {
				t.Fatalf("duplicate oracle bucket %d", cell.bucket)
			}
			expected[cell] = value.(float64)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal("independent oracle iteration failed")
	}
	if len(expected) == 0 {
		t.Fatalf("metric %q has no comparable %s buckets", name, measure)
	}
	for cell, want := range expected {
		got, exists := actual[cell]
		equal := got == want
		if measure == "p95(value)" {
			// Fractional histogram shares are summed in different orders by
			// the independent oracle and the panel's spread expansion.
			equal = equal || math.Abs(got-want) <= 1e-12*math.Max(math.Abs(got), math.Abs(want))
		}
		if !exists || !equal {
			t.Fatalf("metric %q %s bucket=%d present=%t panel=%v oracle=%v", name, measure, cell.bucket, exists, got, want)
		}
	}
	if len(actual) != len(expected) {
		t.Fatalf("metric %q %s numeric buckets: panel=%d oracle=%d", name, measure, len(actual), len(expected))
	}
	t.Logf("metric=%q measure=%s equal_numeric_buckets=%d", name, measure, len(expected))
	return actual
}

func TestMetricDemoDataMatchesIndependentWindowSQL(t *testing.T) {
	input := os.Getenv("FANOUT_PANEL_BENCH_DATA_DIR")
	if input == "" {
		t.Skip("FANOUT_PANEL_BENCH_DATA_DIR is unset")
	}
	data := copyMetricCrosscheckData(t, input)
	engine, _ := panelBenchEngine(t, data)
	names := panelBenchmarkMetricNames(t, engine, data)
	histogram := names["histogram"]
	if histogram == "" {
		t.Fatal("format-4 fixture needs a histogram in the cross-check window")
	}
	for _, tc := range []struct{ name, measure string }{
		{"system.network.io", "rate()"}, {histogram, "p95(value)"}, {"system.memory.usage", "last(value)"},
	} {
		t.Run(tc.measure, func(t *testing.T) {
			crosscheckMetricFrame(t, engine, tc.name, tc.measure, data.Start, data.End)
		})
	}
}

func TestMetricOracleMatchesKnownFixture(t *testing.T) {
	e, db := metricFixture(t)
	for i := -1; i < 3; i++ {
		at := fixtureStart.Add(time.Duration(i) * time.Minute)
		m := metricCounter(at, float64(100+60*(i+1)))
		m.Attributes = map[string]any{"direction": "receive"}
		insertMetrics(t, db, m)
		h := metricHistogram(at, "[10,20]", fmt.Sprintf("[%d,%d,0]", 10*(i+2), 10*(i+2)), int64(20*(i+2)), float64(300*(i+2)), "a")
		h.Name = "latency"
		insertMetrics(t, db, h)
		m.Name, m.Monotonic, m.Value = "memory", false, float64(10+i)
		m.Attributes = map[string]any{"state": "used"}
		insertMetrics(t, db, m)
	}
	end := fixtureStart.Add(3 * time.Minute)
	for _, tc := range []struct {
		name, measure, dimension string
		values                   []float64
	}{
		{"net", "rate()", "receive", []float64{1, 1, 1}},
		{"latency", "p95(value)", "", []float64{19, 19}},
		{"memory", "last(value)", "used", []float64{10, 11, 12}},
	} {
		t.Run(tc.measure, func(t *testing.T) {
			actual := crosscheckMetricFrame(t, e.engine, tc.name, tc.measure, fixtureStart, end)
			for i, want := range tc.values {
				cell := metricCrosscheckCell{fixtureStart.Add(time.Duration(i) * time.Minute).UnixMilli(), tc.dimension}
				if got, exists := actual[cell]; !exists || got != want {
					t.Fatalf("known bucket %d: present=%t value=%v want=%v", i, exists, got, want)
				}
			}
		})
	}
}

func TestMetricOracleMatchesResetsRebinningRetriesAndStaleness(t *testing.T) {
	e, db := metricFixture(t)
	for i, p := range []struct {
		bounds, counts string
		count          int64
	}{
		{"[10,20,30]", "[10,10,10,0]", 30},
		{"[20,30]", "[25,15,0]", 40}, // Compatible coarsening, no reset.
		{"[20,30]", "[24,30,0]", 54}, // One bin decreases although total rises.
		{"[10,30]", "[30,30,0]", 60}, // Incompatible layout resets all bins.
		{"[10,30]", "[30,30,0]", 60}, // Real zero increase yields a NULL p95.
	} {
		h := metricHistogram(fixtureStart.Add(time.Duration(i-1)*time.Minute), p.bounds, p.counts, p.count, float64(p.count), "a")
		h.Name = "latency"
		insertMetrics(t, db, h)
	}
	for i, p := range []struct {
		seconds int
		value   float64
		id      string
	}{
		{-60, 100, "old"}, {0, 160, "old"}, {30, math.NaN(), "old"},
		{60, 1, "old"}, {60, 220, "old"}, // Latest retry wins.
		{120, 10, "old"}, {120, 1000, "new"}, {180, 1060, "new"},
	} {
		m := metricCounter(fixtureStart.Add(time.Duration(p.seconds)*time.Second), p.value)
		m.IngestedAt += int64(i)
		m.Attributes = map[string]any{"direction": "receive"}
		m.Resource = map[string]any{"container.id": p.id}
		insertMetrics(t, db, m)
	}
	for _, p := range []struct {
		seconds int
		value   float64
		id      string
	}{
		{-60, 10, "old"}, {60, 20, "new"}, {360, 30, "new"},
	} {
		m := metricCounter(fixtureStart.Add(time.Duration(p.seconds)*time.Second), p.value)
		m.Name, m.Monotonic = "memory", false
		m.Attributes = map[string]any{"state": "used"}
		m.Resource = map[string]any{"container.id": p.id}
		insertMetrics(t, db, m)
	}
	end := fixtureStart.Add(8*time.Minute + time.Nanosecond)
	for _, tc := range []struct{ name, measure string }{
		{"net", "rate()"}, {"latency", "p95(value)"}, {"memory", "last(value)"},
	} {
		t.Run(tc.measure, func(t *testing.T) {
			crosscheckMetricFrame(t, e.engine, tc.name, tc.measure, fixtureStart, end)
		})
	}
}

func TestMetricOracleMatchesDeltaStartsAndMultipleSeries(t *testing.T) {
	e, db := metricFixture(t)
	for series := range 3 {
		for i := -1; i < 3; i++ {
			at := fixtureStart.Add(time.Duration(i)*time.Minute + time.Duration(series)*time.Second + 17*time.Nanosecond)
			m := metricCounter(at, float64(101+series+i*7))
			m.Attributes = map[string]any{"direction": "receive", "series": series}
			if series != 0 {
				m.Temporality = "delta"
				if series == 1 {
					m.StartTimeUnixNanos = at.Add(-37*time.Second - 9*time.Nanosecond).UnixNano()
				}
			}
			insertMetrics(t, db, m)
			h := metricHistogram(at, "[10,20]", "[10,10,0]", 20, 300, fmt.Sprint(series))
			h.Name, h.Temporality, h.StartTimeUnixNanos = "latency", "delta", m.StartTimeUnixNanos
			insertMetrics(t, db, h)
		}
	}
	end := fixtureStart.Add(3 * time.Minute)
	for _, tc := range []struct{ name, measure string }{{"net", "rate()"}, {"latency", "p95(value)"}} {
		t.Run(tc.measure, func(t *testing.T) {
			crosscheckMetricFrame(t, e.engine, tc.name, tc.measure, fixtureStart, end)
		})
	}
}

func TestMetricOracleMatchesGapsAndLookbackBoundary(t *testing.T) {
	for _, elapsed := range []time.Duration{10 * time.Minute, 10*time.Minute + time.Nanosecond, 24 * time.Hour} {
		t.Run(elapsed.String(), func(t *testing.T) {
			e, db := metricFixture(t)
			first := fixtureStart.Add(time.Second)
			later := first.Add(elapsed)
			insertMetrics(t, db, metricCounter(first, 100))
			insertMetrics(t, db, metricCounter(later, 86500))
			h := metricHistogram(first, "[10,20]", "[10,10,0]", 20, 300, "a")
			h.Name = "latency"
			insertMetrics(t, db, h)
			h = metricHistogram(later, "[10,20]", "[43210,43210,0]", 86420, 1296300, "a")
			h.Name = "latency"
			insertMetrics(t, db, h)
			for _, tc := range []struct{ name, measure string }{{"net", "rate()"}, {"latency", "p95(value)"}} {
				t.Run(tc.measure, func(t *testing.T) {
					crosscheckMetricFrame(t, e.engine, tc.name, tc.measure, fixtureStart, later.Add(6*time.Minute))
				})
			}
		})
	}
}
