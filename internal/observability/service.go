package observability

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	appid "github.com/labstack/fanout/internal/id"
	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
)

const (
	defaultWindow    = time.Hour
	defaultMaxWindow = 30 * 24 * time.Hour
	defaultLimit     = 100
	maxLimit         = 500
)

var (
	ErrInvalidScope = errors.New("invalid observability query scope")
	ErrInvalidLimit = errors.New("invalid observability query limit")
)

type DB = queryrows.Queryer

type traceReader interface {
	Trace(context.Context, telemetry.TraceQuery) ([]telemetry.IndexedSpan, error)
}

type Service struct {
	db             DB
	repository     traceReader
	maxWindow      time.Duration
	now            func() time.Time
	endpointMature atomic.Bool
}

func New(db DB, repository traceReader, retentionDays int) *Service {
	if db == nil || repository == nil {
		panic("observability requires query engine and indexed trace reader")
	}
	maxWindow := defaultMaxWindow
	if retentionDays > 0 {
		maxWindow = time.Duration(retentionDays) * 24 * time.Hour
	}
	return &Service{db: db, repository: repository, maxWindow: maxWindow, now: time.Now}
}

// SQLDB adapts a standard database/sql queryer for tests and callers that do
// not need storage-engine row-lifetime hooks.
func SQLDB(db queryrows.SQLQueryer) DB { return queryrows.SQLAdapter{DB: db} }

func (s *Service) normalizeScope(scope Scope) (Scope, error) {
	now := s.now().UTC()
	if scope.End.IsZero() {
		scope.End = now
	}
	if scope.Start.IsZero() {
		scope.Start = scope.End.Add(-defaultWindow)
	}
	scope.Start = scope.Start.UTC()
	scope.End = scope.End.UTC()
	if !scope.Start.Before(scope.End) || scope.End.Sub(scope.Start) > s.maxWindow {
		return Scope{}, fmt.Errorf("%w: window must be positive and at most %s", ErrInvalidScope, s.maxWindow)
	}
	scope.Namespace = strings.TrimSpace(scope.Namespace)
	return scope, nil
}

func timelineBucketWidth(window time.Duration) string {
	switch {
	case window <= 24*time.Hour:
		return "5 minutes"
	case window <= 7*24*time.Hour:
		return "30 minutes"
	case window <= 30*24*time.Hour:
		return "4 hours"
	default:
		return "1 day"
	}
}

func normalizeLimit(limit int) (int, error) {
	if limit == 0 {
		return defaultLimit, nil
	}
	if limit < 0 || limit > maxLimit {
		return 0, fmt.Errorf("%w: must be between 1 and %d", ErrInvalidLimit, maxLimit)
	}
	return limit, nil
}

func (s *Service) provenance(scope Scope) Provenance {
	return s.provenanceFor(scope, "service_rollup")
}

func (s *Service) provenanceFor(scope Scope, source string) Provenance {
	return Provenance{
		QueryID:   appid.MustNew(),
		Window:    scope.Start.Format(time.RFC3339Nano) + "/" + scope.End.Format(time.RFC3339Nano),
		Generated: s.now().UTC(),
		// Complete is always true today; reserved for partial-scan results.
		Complete:   true,
		DataSource: source,
	}
}

func classify(errorRate, p95MS float64) Health {
	switch {
	case errorRate >= 0.05 || p95MS >= 2000:
		return HealthUnhealthy
	case errorRate >= 0.01 || p95MS >= 750:
		return HealthDegraded
	default:
		return HealthHealthy
	}
}

func overallHealth(counts HealthCounts) Health {
	if counts.Unhealthy > 0 {
		return HealthUnhealthy
	}
	if counts.Degraded > 0 {
		return HealthDegraded
	}
	return HealthHealthy
}
