package service

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"
)

// Home assembles all data for the deterministic Home triage page.
// It queries rollup data, builds sparklines, fetches top errors for unhealthy
// services, and integrates with the incident tracker.
func (s *Service) Home(ctx context.Context, window int, namespace, tenantID string, tracker *IncidentTracker) (*HomeResult, error) {
	if window <= 0 {
		window = 60
	}

	namespace, tenantID = s.defaults(namespace, tenantID)

	// Query per-service aggregates from service_rollup.
	q := fmt.Sprintf(`
SELECT
  service,
  SUM(spans)::BIGINT AS span_cnt,
  AVG(CASE WHEN spans > 0 THEN p50_ms END) AS p50_ms,
  AVG(CASE WHEN spans > 0 THEN p95_ms END) AS p95_ms,
  AVG(CASE WHEN spans > 0 THEN error_rate END) AS error_rate
FROM service_rollup
WHERE bucket >= now() - INTERVAL %d MINUTE
  AND tenant = ?
  AND (? = '' OR namespace = ?)
GROUP BY service
ORDER BY SUM(spans) DESC
LIMIT 100;
`, window)

	rows, err := s.duck.DB.QueryContext(ctx, q, tenantID, namespace, namespace)
	if err != nil {
		return nil, fmt.Errorf("home rollup query: %w", err)
	}
	defer rows.Close()

	type svcRow struct {
		name      string
		spans     int64
		p50       float64
		p95       float64
		errorRate float64
		health    string
		score     float64
	}

	var svcs []svcRow
	for rows.Next() {
		var r svcRow
		var p50null, p95null, errNull sql.NullFloat64
		if err := rows.Scan(&r.name, &r.spans, &p50null, &p95null, &errNull); err != nil {
			slog.Warn("scan failed", "method", "Home", "err", err)
			continue
		}
		if p50null.Valid {
			r.p50 = p50null.Float64
		}
		if p95null.Valid {
			r.p95 = p95null.Float64
		}
		if errNull.Valid {
			r.errorRate = errNull.Float64
		}
		r.health = DeriveHealth(r.errorRate, r.p95, r.spans)
		r.score = HealthScore(r.errorRate, r.p95, r.spans)
		svcs = append(svcs, r)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("rows iteration error", "method", "Home", "err", err)
	}

	// Return empty result if no services found.
	if len(svcs) == 0 {
		return &HomeResult{
			Incidents: []HomeIncident{},
			Services:  []HomeService{},
			Alerts:    []HomeAlert{},
		}, nil
	}

	// Feed each service into the incident tracker.
	now := time.Now()
	if tracker != nil {
		for _, r := range svcs {
			tracker.Tick(r.name, r.health, r.errorRate, r.p95, now)
		}
	}

	// Collect incident states from tracker.
	var incidentSvcNames []string
	incidentMap := make(map[string]Incident)
	if tracker != nil {
		for _, inc := range tracker.Incidents() {
			incidentMap[inc.Service] = inc
		}
	}

	// Query sparklines for all services.
	allSvcNames := make([]string, 0, len(svcs))
	for _, r := range svcs {
		allSvcNames = append(allSvcNames, r.name)
	}
	sparklines, err := s.homeSparklines(ctx, window, namespace, tenantID, allSvcNames)
	if err != nil {
		slog.Error("sparklines query failed", "method", "Home", "err", err)
		sparklines = make(map[string][]float64)
	}

	// Determine which services are incidents (degraded/unhealthy).
	for _, r := range svcs {
		if r.health == "degraded" || r.health == "unhealthy" {
			incidentSvcNames = append(incidentSvcNames, r.name)
		}
	}

	// Query top errors for degraded/unhealthy services — use a short window
	// (max 5 min) to avoid expensive full-table scans on raw spans.
	topErrors := make(map[string][]HomeTopError)
	if len(incidentSvcNames) > 0 {
		errWindow := window
		if errWindow > 5 {
			errWindow = 5
		}
		topErrors, err = s.homeTopErrors(ctx, errWindow, namespace, tenantID, incidentSvcNames)
		if err != nil {
			slog.Error("top errors query failed", "method", "Home", "err", err)
		}
	}

	// Compute summary aggregates.
	var totalSpans int64
	var weightedP95, weightedErr float64
	summary := HomeSummary{}
	summary.TotalServices = len(svcs)

	for _, r := range svcs {
		totalSpans += r.spans
		weightedP95 += r.p95 * float64(r.spans)
		weightedErr += r.errorRate * float64(r.spans)
		switch r.health {
		case "healthy", "active":
			summary.Healthy++
		case "degraded":
			summary.Degraded++
		case "unhealthy":
			summary.Unhealthy++
		}
	}

	summary.TrafficPerMin = float64(totalSpans) / float64(window)
	if totalSpans > 0 {
		summary.ErrorRate = weightedErr / float64(totalSpans)
		summary.P95Ms = weightedP95 / float64(totalSpans)
	}

	// Build incidents and healthy services lists.
	var incidents []HomeIncident
	var healthyServices []HomeService

	for _, r := range svcs {
		errKey := r.name + "_err"
		trafficKey := r.name + "_traffic"
		trafficPerMin := float64(r.spans) / float64(window)

		if r.health == "degraded" || r.health == "unhealthy" {
			inc := HomeIncident{
				Service:          r.name,
				Health:           r.health,
				HealthScore:      r.score,
				ErrorRate:        r.errorRate,
				P95Ms:            r.p95,
				TrafficPerMin:    trafficPerMin,
				Lifecycle:        "open",
				SparklineErrRate: sparklines[errKey],
				TopErrors:        topErrors[r.name],
				Related:          []string{},
			}

			// Enrich with tracker incident state if available.
			if tracked, ok := incidentMap[r.name]; ok {
				inc.Lifecycle = tracked.Lifecycle
				if !tracked.StartedAt.IsZero() {
					inc.StartedAt = tracked.StartedAt.UTC().Format(time.RFC3339)
				}
			}

			if inc.SparklineErrRate == nil {
				inc.SparklineErrRate = []float64{}
			}
			if inc.TopErrors == nil {
				inc.TopErrors = []HomeTopError{}
			}

			incidents = append(incidents, inc)
		} else {
			hsvc := HomeService{
				Name:             r.name,
				Health:           r.health,
				HealthScore:      r.score,
				TrafficPerMin:    trafficPerMin,
				ErrorRate:        r.errorRate,
				P95Ms:            r.p95,
				SparklineTraffic: sparklines[trafficKey],
			}
			if hsvc.SparklineTraffic == nil {
				hsvc.SparklineTraffic = []float64{}
			}
			healthyServices = append(healthyServices, hsvc)
		}
	}

	// Sort incidents by health score ascending (worst first).
	sortIncidents(incidents)

	return &HomeResult{
		Summary:   summary,
		Incidents: incidents,
		Services:  healthyServices,
		Alerts:    []HomeAlert{},
	}, nil
}

// homeSparklines queries per-minute rollup buckets for the specified services.
// Returns a map with keys like "svc-a_traffic" and "svc-a_err".
func (s *Service) homeSparklines(ctx context.Context, window int, namespace, tenantID string, services []string) (map[string][]float64, error) {
	if len(services) == 0 {
		return make(map[string][]float64), nil
	}

	placeholders := make([]string, len(services))
	args := []interface{}{tenantID, namespace, namespace}
	for i, svc := range services {
		placeholders[i] = "?"
		args = append(args, svc)
	}

	q := fmt.Sprintf(`
SELECT service, bucket, spans, error_rate
FROM service_rollup
WHERE bucket >= now() - INTERVAL %d MINUTE
  AND tenant = ?
  AND (? = '' OR namespace = ?)
  AND service IN (%s)
ORDER BY service, bucket;
`, window, strings.Join(placeholders, ", "))

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sparklines query failed: %w", err)
	}
	defer rows.Close()

	// Map from service name to ordered slices.
	trafficMap := make(map[string][]float64)
	errMap := make(map[string][]float64)

	for rows.Next() {
		var svcName string
		var bucket time.Time
		var spans int64
		var errRate sql.NullFloat64

		if err := rows.Scan(&svcName, &bucket, &spans, &errRate); err != nil {
			slog.Warn("sparklines scan failed", "err", err)
			continue
		}

		er := 0.0
		if errRate.Valid {
			er = errRate.Float64
		}

		trafficMap[svcName] = append(trafficMap[svcName], float64(spans))
		errMap[svcName] = append(errMap[svcName], er)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("sparklines rows error", "err", err)
	}

	out := make(map[string][]float64, len(services)*2)
	for _, svc := range services {
		out[svc+"_traffic"] = trafficMap[svc]
		out[svc+"_err"] = errMap[svc]
	}
	return out, nil
}

// homeTopErrors queries top error messages for a set of services.
// Queries raw spans table for error spans, groups by service+message, limits
// to top 5 per service (20 total). Returns a map keyed by service name.
func (s *Service) homeTopErrors(ctx context.Context, window int, namespace, tenantID string, services []string) (map[string][]HomeTopError, error) {
	if len(services) == 0 {
		return make(map[string][]HomeTopError), nil
	}

	// Build IN clause placeholders.
	placeholders := make([]string, len(services))
	args := make([]interface{}, 0, 3+len(services))
	args = append(args, tenantID, namespace, namespace)
	for i, svc := range services {
		placeholders[i] = "?"
		args = append(args, svc)
	}
	inClause := strings.Join(placeholders, ", ")

	q := fmt.Sprintf(`
SELECT
  service,
  COALESCE(NULLIF(status_message, ''), 'error') AS message,
  COUNT(*) AS cnt
FROM spans
WHERE start_time >= now() - INTERVAL %d MINUTE
  AND tenant = ?
  AND (? = '' OR namespace = ?)
  AND service IN (%s)
  AND status IN ('STATUS_CODE_ERROR', 'ERROR')
GROUP BY service, message
ORDER BY service, cnt DESC
LIMIT 20;
`, window, inClause)

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("top errors query failed: %w", err)
	}
	defer rows.Close()

	out := make(map[string][]HomeTopError)
	perSvcCount := make(map[string]int)

	for rows.Next() {
		var svcName, message string
		var cnt int64

		if err := rows.Scan(&svcName, &message, &cnt); err != nil {
			slog.Warn("top errors scan failed", "err", err)
			continue
		}

		if perSvcCount[svcName] >= 5 {
			continue
		}

		out[svcName] = append(out[svcName], HomeTopError{
			Message: message,
			Count:   cnt,
		})
		perSvcCount[svcName]++
	}
	if err := rows.Err(); err != nil {
		slog.Warn("top errors rows error", "err", err)
	}

	return out, nil
}

// sortIncidents sorts incidents by HealthScore ascending (worst first).
func sortIncidents(incidents []HomeIncident) {
	sort.Slice(incidents, func(i, j int) bool {
		return incidents[i].HealthScore < incidents[j].HealthScore
	})
}
