package intelligence

import (
	"database/sql"
	"fmt"
	"math"
	"testing"
	"time"
)

// The current window and the baseline window are the same length, but they were
// not counted the same way: the baseline averaged per-5-minute-bucket counts
// while the current side summed the whole 15 minutes. Current was therefore
// about three times the baseline by construction, for every service, always --
// so every service was permanently a critical volume anomaly and the demo's
// health score sat at 0 with 17 "critical" insights that meant nothing.
//
// Steady traffic must produce no anomaly. That is the property here.
func TestVolumeAnomalyComparesEqualWindows(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE spans (
		service TEXT, namespace TEXT, start_time TIMESTAMP, start_unix_nano BIGINT
	)`); err != nil {
		t.Fatal(err)
	}

	// 30 minutes of steady traffic in six 5-minute buckets, with enough jitter
	// that STDDEV is not zero -- a perfectly flat fixture divides by zero and
	// the CASE returns 0.0, which would pass against the broken form too.
	end := time.Date(2026, 9, 21, 18, 0, 0, 0, time.UTC)
	start := end.Add(-15 * time.Minute)
	baselineStart := start.Add(-15 * time.Minute)
	perBucket := []int{98, 103, 99, 101, 97, 102}
	for bucket, count := range perBucket {
		at := baselineStart.Add(time.Duration(bucket) * 5 * time.Minute)
		for _, service := range []string{"cart", "checkout", "frontend"} {
			for i := range count {
				ts := at.Add(time.Duration(i) * time.Millisecond)
				if _, err := db.Exec(`INSERT INTO spans VALUES (?, 'default', ?, ?)`,
					service, ts, ts.UnixNano()); err != nil {
					t.Fatal(err)
				}
			}
		}
	}

	query := volumeAnomalySQL(start.UnixNano(), end.UnixNano(), "")
	rows, err := db.Query(query)
	if err != nil {
		t.Fatalf("volume query: %v", err)
	}
	defer rows.Close()

	seen := 0
	for rows.Next() {
		var service string
		var current, baseline, zScore float64
		if err := rows.Scan(&service, &current, &baseline, &zScore); err != nil {
			t.Fatal(err)
		}
		seen++
		if current <= 0 {
			t.Errorf("%s: current = %v, want the spans it actually served", service, current)
		}
		ratio := current / baseline
		if ratio < 0.8 || ratio > 1.25 {
			t.Errorf("%s: current %.0f against baseline %.0f (%.2fx) -- the two windows are not counted the same way",
				service, current, baseline, ratio)
		}
		if math.Abs(zScore) >= 3 {
			t.Errorf("%s: steady traffic scored z=%.2f, which reports as a critical anomaly", service, zScore)
		}
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if seen != 3 {
		t.Fatalf("got %d services, want 3", seen)
	}
	fmt.Print()
}

// The other direction, and the one that matters more: a test asserting only
// that steady traffic is quiet passes just as well against a detector that
// never fires at all. A service whose traffic really does collapse must still
// be reported.
func TestVolumeAnomalyStillFiresOnARealDrop(t *testing.T) {
	db, err := sql.Open("duckdb", "")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE spans (
		service TEXT, namespace TEXT, start_time TIMESTAMP, start_unix_nano BIGINT
	)`); err != nil {
		t.Fatal(err)
	}

	end := time.Date(2026, 9, 21, 18, 0, 0, 0, time.UTC)
	start := end.Add(-15 * time.Minute)
	baselineStart := start.Add(-15 * time.Minute)
	perBucket := []int{98, 103, 99, 101, 97, 102}
	for bucket, count := range perBucket {
		at := baselineStart.Add(time.Duration(bucket) * 5 * time.Minute)
		inCurrentWindow := !at.Before(start)
		for _, service := range []string{"steady", "collapsing"} {
			// "collapsing" serves its baseline, then drops to 5% of it.
			if service == "collapsing" && inCurrentWindow {
				count = count / 20
			}
			for i := range count {
				ts := at.Add(time.Duration(i) * time.Millisecond)
				if _, err := db.Exec(`INSERT INTO spans VALUES (?, 'default', ?, ?)`,
					service, ts, ts.UnixNano()); err != nil {
					t.Fatal(err)
				}
			}
			count = perBucket[bucket]
		}
	}

	rows, err := db.Query(volumeAnomalySQL(start.UnixNano(), end.UnixNano(), ""))
	if err != nil {
		t.Fatal(err)
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
		t.Logf("%-11s current=%.1f baseline=%.1f z=%.2f", service, current, baseline, zScore)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	if z, ok := scores["collapsing"]; !ok || math.Abs(z) < 3 {
		t.Errorf("a service that lost 95%% of its traffic scored z=%.2f (present: %v); it must be reported", z, ok)
	}
	if z := scores["steady"]; math.Abs(z) >= 3 {
		t.Errorf("steady traffic scored z=%.2f alongside it", z)
	}
}
