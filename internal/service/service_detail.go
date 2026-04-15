package service

import (
	"context"
	"fmt"
	"log/slog"
	"time"
)

func (s *Service) ServiceDetail(ctx context.Context, svcName string, window int, namespace, tenantID string) (*ServiceDetailResult, error) {
	if window <= 0 {
		window = 60
	}

	// 1. DiagnoseEnhanced
	diag, err := s.DiagnoseEnhanced(ctx, svcName, window, "", namespace, tenantID)
	if err != nil {
		return nil, fmt.Errorf("service detail diagnose: %w", err)
	}

	// 2. Spans grouped by operation
	spanResult, err := s.Spans(ctx, SpanParams{
		Service:          svcName,
		GroupBy:          []string{"operation"},
		IncludeExemplars: true,
		Window:           window,
		Namespace:        namespace,
		TenantID:         tenantID,
		Limit:            50,
	})
	if err != nil {
		return nil, fmt.Errorf("service detail spans: %w", err)
	}

	endpoints := make([]ServiceEndpoint, 0, len(spanResult.Groups))
	for _, g := range spanResult.Groups {
		ep := ServiceEndpoint{
			Operation: g.Key["operation"],
			Count:     g.Count,
			ErrorRate: g.ErrorRate,
			P50Ms:     g.P50Ms,
			P95Ms:     g.P95Ms,
			P99Ms:     g.P99Ms,
		}
		if len(g.ExemplarTraceIDs) > 0 {
			ep.ExemplarID = g.ExemplarTraceIDs[0]
		}
		endpoints = append(endpoints, ep)
	}

	// 3. Rollup buckets for charts
	now := time.Now().UTC()
	start := now.Add(-time.Duration(window) * time.Minute)
	rollupBuckets, err := s.QueryRollupBuckets(ctx, svcName, start, now)
	if err != nil {
		slog.Error("service detail rollup query failed", "service", svcName, "err", err)
		rollupBuckets = nil
	}

	buckets := make([]ServiceBucket, 0, len(rollupBuckets))
	for _, rb := range rollupBuckets {
		buckets = append(buckets, ServiceBucket{
			Time:      rb.Bucket.UTC().Format(time.RFC3339),
			ErrorRate: rb.ErrorRate,
			P95Ms:     rb.P95Ms,
			P50Ms:     rb.P50Ms,
			Spans:     rb.Spans,
		})
	}

	return &ServiceDetailResult{
		Diagnose:  *diag,
		Endpoints: endpoints,
		Buckets:   buckets,
	}, nil
}
