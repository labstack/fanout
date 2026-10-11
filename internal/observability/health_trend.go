package observability

import (
	"context"
	"fmt"
	"github.com/labstack/fanout/internal/queryrows"
	"time"
)

type HealthTrend struct {
	Times  []int64
	Values []float64
}

func (s *Service) HealthErrorTrend(ctx context.Context, scope Scope) (HealthTrend, error) {
	scope, err := s.normalizeScope(scope)
	if err != nil {
		return HealthTrend{}, err
	}
	ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: scope.Start, End: scope.End, Namespace: scope.Namespace, Service: scope.Service})
	sql := fmt.Sprintf(`SELECT time_bucket(INTERVAL '%s',bucket) AS point_time,
      100.0*COALESCE(SUM(error_rate*spans)/NULLIF(SUM(spans),0),0)
      FROM service_rollup WHERE bucket>=? AND bucket<?
      AND (?='' OR namespace=?) AND (?='' OR service=?)
      GROUP BY point_time ORDER BY point_time`, timelineBucketWidth(scope.End.Sub(scope.Start)))
	rows, err := s.db.QueryContext(ctx, sql, scope.Start, scope.End, scope.Namespace, scope.Namespace, scope.Service, scope.Service)
	if err != nil {
		return HealthTrend{}, fmt.Errorf("query health trend: %w", err)
	}
	defer rows.Close()
	out := HealthTrend{Times: []int64{}, Values: []float64{}}
	for rows.Next() {
		var at time.Time
		var rate float64
		if err := rows.Scan(&at, &rate); err != nil {
			return HealthTrend{}, fmt.Errorf("scan health trend: %w", err)
		}
		out.Times = append(out.Times, at.UnixMilli())
		out.Values = append(out.Values, rate)
	}
	if err := rows.Err(); err != nil {
		return HealthTrend{}, fmt.Errorf("iterate health trend: %w", err)
	}
	return out, nil
}
