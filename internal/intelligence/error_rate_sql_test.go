package intelligence

import (
	"database/sql"
	"math"
	"testing"
	"time"
)

// errorRateFixture writes six 5-minute buckets: the first three are the
// baseline window, the last three the current one. Each entry is a bucket's
// error percentage, so a service's whole history is one readable slice.
func errorRateFixture(t *testing.T, db *sql.DB, start time.Time, services map[string][]float64) {
	t.Helper()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS spans (
		service TEXT, namespace TEXT, status TEXT, start_time TIMESTAMP, start_unix_nano BIGINT
	)`); err != nil {
		t.Fatal(err)
	}
	const perBucket = 1000
	for service, rates := range services {
		for bucket, rate := range rates {
			at := start.Add(time.Duration(bucket) * 5 * time.Minute)
			errors := int(math.Round(rate * perBucket))
			for i := range perBucket {
				status := "STATUS_CODE_OK"
				if i < errors {
					status = "STATUS_CODE_ERROR"
				}
				ts := at.Add(time.Duration(i) * time.Millisecond)
				if _, err := db.Exec(`INSERT INTO spans VALUES (?, 'default', ?, ?, ?)`,
					service, status, ts, ts.UnixNano()); err != nil {
					t.Fatal(err)
				}
			}
		}
	}
}

func errorRateScores(t *testing.T, db *sql.DB, start, end time.Time) map[string]float64 {
	t.Helper()
	rows, err := db.Query(errorRateAnomalySQL(start.UnixNano(), end.UnixNano(), ""))
	if err != nil {
		t.Fatalf("error rate query: %v", err)
	}
	defer rows.Close()
	scores := map[string]float64{}
	for rows.Next() {
		var service string
		var current, baseline, zScore float64
		if err := rows.Scan(&service, &current, &baseline, &zScore); err != nil {
			t.Fatal(err)
		}
		scores[service] = zScore
		t.Logf("%-14s current=%.3f baseline=%.3f z=%.2f", service, current, baseline, zScore)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	return scores
}

// The z-score divided a difference of RATES by the STDDEV of a per-span 0/1
// indicator. That indicator's spread is sqrt(p(1-p)) -- the scatter of
// individual span outcomes, about 0.34 at a 14% error rate -- and not the
// scatter of the rate itself from bucket to bucket, which is what a z-score on
// a rate needs. The units did not match, and the mismatch got worse the noisier
// the service: at the 13.8% baseline the live demo actually ran, clearing the
// 2.0 threshold required the rate to jump by 69 percentage points.
//
// So the services most worth watching were the ones least able to alert.
const errorRateThreshold = 2.0

func TestErrorRateFiresOnASpikeFromANoisyBaseline(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	end := time.Date(2026, 9, 21, 18, 0, 0, 0, time.UTC)
	start := end.Add(-15 * time.Minute)
	errorRateFixture(t, db, start.Add(-15*time.Minute), map[string][]float64{
		// Sat at 13.8% and tripled. The old denominator scored this 0.47.
		"spiking-noisy": {0.13, 0.14, 0.138, 0.40, 0.42, 0.41},
		// Same noisy baseline, no spike. Must stay quiet.
		"steady-noisy": {0.13, 0.14, 0.138, 0.135, 0.142, 0.137},
	})

	scores := errorRateScores(t, db, start, end)
	if z := scores["spiking-noisy"]; math.Abs(z) < errorRateThreshold {
		t.Errorf("a service that went from 13.8%% to 41%% errors scored z=%.2f, below the %.1f threshold", z, errorRateThreshold)
	}
	if z := scores["steady-noisy"]; math.Abs(z) >= errorRateThreshold {
		t.Errorf("a service holding steady at 13.8%% scored z=%.2f and would alert", z)
	}
}

// A healthy service has a flat 0% error rate, so the bucket-to-bucket stddev of
// its rate is exactly zero. Dividing by it yields the CASE's 0.0 fallback, and
// the service that just started failing is the one that cannot alert. The
// denominator needs a floor.
func TestErrorRateFiresWhenAFlatlineBreaks(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	end := time.Date(2026, 9, 21, 18, 0, 0, 0, time.UTC)
	start := end.Add(-15 * time.Minute)
	errorRateFixture(t, db, start.Add(-15*time.Minute), map[string][]float64{
		"clean-then-broken": {0, 0, 0, 0.40, 0.45, 0.42},
		"clean-throughout":  {0, 0, 0, 0, 0, 0},
		// One stray error in a thousand spans is not an incident.
		"clean-with-a-blip": {0, 0, 0, 0, 0.001, 0},
	})

	scores := errorRateScores(t, db, start, end)
	if z := scores["clean-then-broken"]; math.Abs(z) < errorRateThreshold {
		t.Errorf("a service that went from no errors to 42%% scored z=%.2f, below the %.1f threshold", z, errorRateThreshold)
	}
	if z, ok := scores["clean-with-a-blip"]; ok && math.Abs(z) >= errorRateThreshold {
		t.Errorf("one error in three thousand spans scored z=%.2f and would alert", z)
	}
	// A service with no errors at all is filtered out by `WHERE c.error_rate > 0`.
	if z, ok := scores["clean-throughout"]; ok && math.Abs(z) >= errorRateThreshold {
		t.Errorf("a service with no errors scored z=%.2f", z)
	}
}
