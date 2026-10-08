package observability

import (
	"context"
	"github.com/labstack/fanout/internal/queryrows"
	"math"
	"strings"
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
	if err != nil || len(trend) != 1 || math.Abs(trend[0]-2.5) > 1e-9 {
		t.Fatalf("trend=%v err=%v", trend, err)
	}
	overview, err := svc.Overview(t.Context(), scope, 400)
	if err != nil || math.Abs(overview.Data.ErrorRate*100-2.5) > 1e-9 {
		t.Fatalf("overview=%+v err=%v", overview, err)
	}
	scope.Namespace = "missing"
	trend, err = svc.HealthErrorTrend(t.Context(), scope)
	if err != nil || len(trend) != 0 {
		t.Fatalf("empty trend=%v err=%v", trend, err)
	}
	overview, err = svc.Overview(t.Context(), scope, 400)
	if err != nil || overview.Data.Health != HealthUnknown {
		t.Fatalf("empty health=%+v err=%v", overview, err)
	}
}

type healthReadRecorder struct {
	DB
	statements []string
}

func (r *healthReadRecorder) QueryContext(ctx context.Context, sql string, args ...any) (queryrows.Rows, error) {
	r.statements = append(r.statements, sql)
	return r.DB.QueryContext(ctx, sql, args...)
}
func TestHealthErrorTrendExecutesOnlyScopedWeightedRollupRead(t *testing.T) {
	d, _, svc := newCompletedReadTest(t)
	record := &healthReadRecorder{DB: d}
	svc.db = record
	at := time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)
	if _, err := svc.HealthErrorTrend(t.Context(), Scope{Start: at, End: at.Add(time.Hour), Namespace: "shop", Service: "checkout"}); err != nil {
		t.Fatal(err)
	}
	if len(record.statements) != 1 || !strings.Contains(record.statements[0], "FROM service_rollup") || strings.Contains(record.statements[0], "endpoint") || strings.Contains(record.statements[0], "heatmap") {
		t.Fatalf("unexpected health reads: %v", record.statements)
	}
}
