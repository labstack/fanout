package observability

import (
	"math"
	"testing"
	"time"
)

func TestHealthErrorTrendWeightsScopedServicesAndPreservesEmptyScope(t *testing.T) {
	d, _, svc := newCompletedReadTest(t)
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if _, err := d.DB.Exec(`INSERT INTO service_rollup VALUES ('shop',?,'checkout',10,10,50,100,.1,2,3),('shop',?,'checkout',30,30,70,200,0,4,5),('other',?,'checkout',100,100,1,1,1,0,0),('shop',?,'payment',100,100,1,1,1,0,0)`, at, at.Add(time.Minute), at, at); err != nil {
		t.Fatal(err)
	}
	scope := Scope{Namespace: "shop", Service: "checkout", Start: at, End: at.Add(time.Hour)}
	trend, err := svc.HealthErrorTrend(t.Context(), scope)
	if err != nil || len(trend.Values) != 1 || math.Abs(trend.Values[0]-2.5) > 1e-9 || len(trend.Times) != 1 || trend.Times[0] != at.UnixMilli() {
		t.Fatalf("trend=%v err=%v", trend, err)
	}
	overview, err := svc.Overview(t.Context(), scope, 400)
	if err != nil || math.Abs(overview.ErrorRate*100-2.5) > 1e-9 {
		t.Fatalf("overview=%+v err=%v", overview, err)
	}
	scope.Namespace = "missing"
	trend, err = svc.HealthErrorTrend(t.Context(), scope)
	if err != nil || len(trend.Values) != 0 {
		t.Fatalf("empty trend=%v err=%v", trend, err)
	}
	overview, err = svc.Overview(t.Context(), scope, 400)
	if err != nil || overview.Health != HealthUnknown {
		t.Fatalf("empty health=%+v err=%v", overview, err)
	}
}

func TestHealthErrorTrendRetainsSparseBucketTimes(t *testing.T) {
	d, _, svc := newCompletedReadTest(t)
	at := time.Date(2026, 10, 1, 12, 1, 17, 0, time.UTC)
	for i, offset := range []time.Duration{time.Minute, 23 * time.Minute} {
		if _, err := d.DB.Exec(`INSERT INTO service_rollup VALUES ('shop',?,'checkout',10,10,50,100,?,0,0)`, at.Add(offset), float64(i)/10); err != nil {
			t.Fatal(err)
		}
	}
	trend, err := svc.HealthErrorTrend(t.Context(), Scope{Namespace: "shop", Start: at, End: at.Add(time.Hour)})
	if err != nil || len(trend.Times) != 2 || len(trend.Values) != 2 || trend.Times[0] != time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC).UnixMilli() || trend.Times[1] != time.Date(2026, 10, 1, 12, 20, 0, 0, time.UTC).UnixMilli() || trend.Values[0] != 0 || trend.Values[1] != 10 {
		t.Fatalf("trend=%+v err=%v", trend, err)
	}
}
