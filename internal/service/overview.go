package service

import (
	"context"
	"database/sql"
	"fmt"
	"log/slog"
	"sort"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/query"
)

// OverviewParams controls which sections Service.Overview populates and how
// expensive work (sparklines, top errors) is gated.
type OverviewParams struct {
	Window    int      // minutes; 0 → 15
	Namespace string   // empty = all namespaces
	Include   []string // sections to populate: "health", "services", "issues", "incidents", "sparklines". Empty = all except "incidents" and "sparklines". Specifying "incidents" implies "sparklines".
	SortBy    string   // services sort: "severity" (default), "error_rate", "latency", "throughput"
	Limit     int      // max services; 0 → 100
	Tracker   *IncidentTracker
}

// overviewSnapshot is the cacheable portion of an Overview computation —
// the rows and any sparkline / top-error data fetched from the DB. Tracker
// state is applied on top per call and is not cached.
type overviewSnapshot struct {
	Rows       []overviewRow
	Sparklines map[string][]float64 // key: "<service>_traffic" or "<service>_err"
	TopErrors  map[string][]TopError
}

type overviewRow struct {
	name      string
	spans     int64
	p50       float64
	p95       float64
	errorRate float64
	score     float64
	status    string
}

// Overview returns a unified health overview, powering both the MCP overview
// tool (compact) and the UI Home page (rich). Sections are populated based on
// p.Include; lifecycle fields on incidents require a non-nil Tracker.
func (s *Service) Overview(ctx context.Context, p OverviewParams) (*OverviewResult, error) {
	window := p.Window
	if window <= 0 {
		window = 15
	}
	limit := p.Limit
	if limit <= 0 {
		limit = 100
	}

	includeAll := len(p.Include) == 0
	wantHealth := includeAll || containsStr(p.Include, "health")
	wantServices := includeAll || containsStr(p.Include, "services")
	wantIssues := includeAll || containsStr(p.Include, "issues")
	wantIncidents := containsStr(p.Include, "incidents")
	wantSparkline := containsStr(p.Include, "sparklines") || wantIncidents

	snapshot, err := s.overviewSnapshot(ctx, window, p.Namespace, limit, wantSparkline, wantIncidents)
	if err != nil {
		return nil, err
	}

	return buildOverviewResult(snapshot, p, window, wantHealth, wantServices, wantIssues, wantIncidents, wantSparkline), nil
}

// overviewSnapshot queries the rollup for per-service aggregates. If the
// caller asked for sparklines, it also queries per-minute rollup data; if it
// asked for incidents, it queries top errors for degraded/unhealthy services.
// Results are cached by (window, namespace, limit, wantSparkline, wantIncidents).
func (s *Service) overviewSnapshot(ctx context.Context, window int, namespace string, limit int, wantSparkline, wantIncidents bool) (*overviewSnapshot, error) {
	cacheKey := fmt.Sprintf("overview:%d:%s:%d:%t:%t", window, namespace, limit, wantSparkline, wantIncidents)
	if v, ok := query.GetCached(cacheKey); ok {
		if snap, ok := v.(*overviewSnapshot); ok {
			return snap, nil
		}
	}

	// Order by health severity first so unhealthy/degraded services are never
	// dropped by the LIMIT. Within each tier, sort by traffic descending.
	q := fmt.Sprintf(`
SELECT
  service,
  SUM(spans)::BIGINT AS span_cnt,
  AVG(CASE WHEN spans > 0 THEN p50_ms END) AS p50_ms,
  AVG(CASE WHEN spans > 0 THEN p95_ms END) AS p95_ms,
  AVG(CASE WHEN spans > 0 THEN error_rate END) AS error_rate
FROM service_rollup
WHERE bucket >= now() - INTERVAL %d MINUTE
  AND (? = '' OR namespace = ?)
GROUP BY service
ORDER BY
  CASE
    WHEN AVG(CASE WHEN spans > 0 THEN error_rate END) > 0.1
      OR AVG(CASE WHEN spans > 0 THEN p95_ms END) > 5000 THEN 0
    WHEN AVG(CASE WHEN spans > 0 THEN error_rate END) > 0.01
      OR AVG(CASE WHEN spans > 0 THEN p95_ms END) > 1000 THEN 1
    ELSE 2
  END,
  SUM(spans) DESC
LIMIT %d;
`, window, limit)

	rows, err := s.duck.DB.QueryContext(ctx, q, namespace, namespace)
	if err != nil {
		return nil, fmt.Errorf("overview query failed: %w", err)
	}
	defer rows.Close()

	var svcs []overviewRow
	for rows.Next() {
		var r overviewRow
		var p50null, p95null, errNull sql.NullFloat64
		if err := rows.Scan(&r.name, &r.spans, &p50null, &p95null, &errNull); err != nil {
			slog.Warn("scan failed", "method", "Overview", "err", err)
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
		r.score = HealthScore(r.errorRate, r.p95, r.spans)
		r.status = DeriveHealth(r.errorRate, r.p95, r.spans)
		svcs = append(svcs, r)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("rows iteration error", "method", "Overview", "err", err)
	}

	snap := &overviewSnapshot{
		Rows:       svcs,
		Sparklines: map[string][]float64{},
		TopErrors:  map[string][]TopError{},
	}

	if len(svcs) == 0 {
		query.SetCached(cacheKey, snap)
		return snap, nil
	}

	if wantSparkline {
		names := make([]string, 0, len(svcs))
		for _, r := range svcs {
			names = append(names, r.name)
		}
		sparks, err := s.overviewSparklines(ctx, window, namespace, names)
		if err != nil {
			slog.Error("sparklines query failed", "method", "Overview", "err", err)
		} else {
			snap.Sparklines = sparks
		}
	}

	if wantIncidents {
		var incidentNames []string
		for _, r := range svcs {
			if r.status == "degraded" || r.status == "unhealthy" {
				incidentNames = append(incidentNames, r.name)
			}
		}
		if len(incidentNames) > 0 {
			// Top errors uses raw spans — cap window at 5 min to avoid full table scans.
			errWindow := window
			if errWindow > 5 {
				errWindow = 5
			}
			topErrs, err := s.overviewTopErrors(ctx, errWindow, namespace, incidentNames)
			if err != nil {
				slog.Error("top errors query failed", "method", "Overview", "err", err)
			} else {
				snap.TopErrors = topErrs
			}
		}
	}

	query.SetCached(cacheKey, snap)
	return snap, nil
}

// overviewSparklines queries per-minute rollup buckets for the given services.
// Returns a map with keys "<svc>_traffic" and "<svc>_err".
func (s *Service) overviewSparklines(ctx context.Context, window int, namespace string, services []string) (map[string][]float64, error) {
	if len(services) == 0 {
		return map[string][]float64{}, nil
	}

	placeholders := make([]string, len(services))
	args := []interface{}{namespace, namespace}
	for i, svc := range services {
		placeholders[i] = "?"
		args = append(args, svc)
	}

	q := fmt.Sprintf(`
SELECT service, bucket, spans, error_rate
FROM service_rollup
WHERE bucket >= now() - INTERVAL %d MINUTE
  AND (? = '' OR namespace = ?)
  AND service IN (%s)
ORDER BY service, bucket;
`, window, strings.Join(placeholders, ", "))

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("sparklines query failed: %w", err)
	}
	defer rows.Close()

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

// overviewTopErrors queries top error messages for a set of services. The SQL
// LIMIT 20 is a shared fetch cap; the Go loop then keeps at most 5 per service
// (an earlier-returned service dominating the fetch can leave later services
// empty — acceptable since those services will still appear in Incidents).
func (s *Service) overviewTopErrors(ctx context.Context, window int, namespace string, services []string) (map[string][]TopError, error) {
	if len(services) == 0 {
		return map[string][]TopError{}, nil
	}

	placeholders := make([]string, len(services))
	args := make([]interface{}, 0, 2+len(services))
	args = append(args, namespace, namespace)
	for i, svc := range services {
		placeholders[i] = "?"
		args = append(args, svc)
	}

	q := fmt.Sprintf(`
SELECT
  service,
  COALESCE(NULLIF(status_message, ''), 'error') AS message,
  COUNT(*) AS cnt
FROM spans
WHERE start_time >= now() - INTERVAL %d MINUTE
  AND (? = '' OR namespace = ?)
  AND service IN (%s)
  AND status IN ('STATUS_CODE_ERROR', 'ERROR')
GROUP BY service, message
ORDER BY service, cnt DESC
LIMIT 20;
`, window, strings.Join(placeholders, ", "))

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("top errors query failed: %w", err)
	}
	defer rows.Close()

	out := make(map[string][]TopError)
	perSvc := make(map[string]int)

	for rows.Next() {
		var svcName, message string
		var cnt int64
		if err := rows.Scan(&svcName, &message, &cnt); err != nil {
			slog.Warn("top errors scan failed", "err", err)
			continue
		}
		if perSvc[svcName] >= 5 {
			continue
		}
		out[svcName] = append(out[svcName], TopError{Message: message, Count: cnt})
		perSvc[svcName]++
	}
	if err := rows.Err(); err != nil {
		slog.Warn("top errors rows error", "err", err)
	}

	return out, nil
}

func buildOverviewResult(snap *overviewSnapshot, p OverviewParams, window int, wantHealth, wantServices, wantIssues, wantIncidents, wantSparkline bool) *OverviewResult {
	out := &OverviewResult{}

	// Global aggregates.
	byStatus := map[string]int{"healthy": 0, "degraded": 0, "unhealthy": 0}
	var totalScore float64
	var totalSpans int64
	var wP95, wErr float64
	for _, r := range snap.Rows {
		totalScore += r.score
		totalSpans += r.spans
		wP95 += r.p95 * float64(r.spans)
		wErr += r.errorRate * float64(r.spans)
		byStatus[r.status]++
	}

	if wantHealth {
		health := &OverviewHealth{
			TotalServices:    len(snap.Rows),
			ByStatus:         byStatus,
			ThroughputPerMin: float64(totalSpans) / float64(window),
		}
		if len(snap.Rows) > 0 {
			health.Score = totalScore / float64(len(snap.Rows))
		}
		if totalSpans > 0 {
			health.GlobalP95Ms = wP95 / float64(totalSpans)
			health.GlobalErrorRate = wErr / float64(totalSpans)
		}
		out.Health = health
	}

	// Services & Issues share a sorted view of snap.Rows.
	services := append([]overviewRow(nil), snap.Rows...)
	sortOverviewRows(services, p.SortBy)

	if wantServices {
		out.Services = make([]OverviewService, 0, len(services))
		for _, r := range services {
			svc := OverviewService{
				Service:       r.name,
				Status:        r.status,
				HealthScore:   r.score,
				Requests:      r.spans,
				TrafficPerMin: float64(r.spans) / float64(window),
				ErrorRate:     r.errorRate,
				P50Ms:         r.p50,
				P95Ms:         r.p95,
			}
			if wantSparkline {
				svc.SparklineTraffic = cloneFloat64s(snap.Sparklines[r.name+"_traffic"])
				if svc.SparklineTraffic == nil {
					svc.SparklineTraffic = []float64{}
				}
			}
			out.Services = append(out.Services, svc)
		}
	}

	if wantIssues {
		out.Issues = make([]OverviewIssue, 0, 10)
		for _, r := range services {
			if len(out.Issues) >= 10 {
				break
			}
			if r.errorRate > 0.05 {
				out.Issues = append(out.Issues, OverviewIssue{
					Service:   r.name,
					Issue:     "high_error_rate",
					Value:     r.errorRate,
					Threshold: 0.05,
				})
			} else if r.p95 > 500 {
				out.Issues = append(out.Issues, OverviewIssue{
					Service:   r.name,
					Issue:     "p95_latency",
					Value:     r.p95,
					Threshold: 500,
				})
			}
		}
	}

	if wantIncidents {
		out.Incidents = buildIncidents(snap, p.Tracker, window, time.Now())
	}

	return out
}

// buildIncidents constructs rich UI-style incident entries for degraded/unhealthy
// services, integrating lifecycle state from the IncidentTracker.
func buildIncidents(snap *overviewSnapshot, tracker *IncidentTracker, window int, now time.Time) []OverviewIncident {
	if tracker != nil {
		for _, r := range snap.Rows {
			tracker.Tick(r.name, r.status, r.errorRate, r.p95, now)
		}
	}

	tracked := make(map[string]Incident)
	if tracker != nil {
		for _, inc := range tracker.Incidents() {
			tracked[inc.Service] = inc
		}
	}

	incidents := make([]OverviewIncident, 0)
	for _, r := range snap.Rows {
		if r.status != "degraded" && r.status != "unhealthy" {
			continue
		}
		inc := OverviewIncident{
			Service:          r.name,
			Status:           r.status,
			HealthScore:      r.score,
			ErrorRate:        r.errorRate,
			P95Ms:            r.p95,
			TrafficPerMin:    float64(r.spans) / float64(window),
			Lifecycle:        "open",
			SparklineErrRate: cloneFloat64s(snap.Sparklines[r.name+"_err"]),
			TopErrors:        cloneTopErrors(snap.TopErrors[r.name]),
			Related:          []string{},
		}
		if t, ok := tracked[r.name]; ok {
			inc.Lifecycle = t.Lifecycle
			if !t.StartedAt.IsZero() {
				inc.StartedAt = t.StartedAt.UTC().Format(time.RFC3339)
			}
		}
		if inc.SparklineErrRate == nil {
			inc.SparklineErrRate = []float64{}
		}
		if inc.TopErrors == nil {
			inc.TopErrors = []TopError{}
		}
		incidents = append(incidents, inc)
	}

	sort.SliceStable(incidents, func(i, j int) bool {
		return incidents[i].HealthScore < incidents[j].HealthScore
	})
	return incidents
}

// sortOverviewRows sorts service rows by the given field (worst first for severity, desc for others).
func sortOverviewRows(svcs []overviewRow, by string) {
	sort.SliceStable(svcs, func(i, j int) bool {
		a, b := svcs[i], svcs[j]
		switch by {
		case "error_rate":
			return a.errorRate > b.errorRate
		case "latency":
			return a.p95 > b.p95
		case "throughput":
			return a.spans > b.spans
		default: // "severity" or empty
			return a.score < b.score
		}
	})
}

func containsStr(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

func cloneFloat64s(in []float64) []float64 {
	if in == nil {
		return nil
	}
	out := make([]float64, len(in))
	copy(out, in)
	return out
}

func cloneTopErrors(in []TopError) []TopError {
	if in == nil {
		return nil
	}
	out := make([]TopError, len(in))
	copy(out, in)
	return out
}
