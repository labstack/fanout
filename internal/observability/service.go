package observability

import (
	"errors"
	"fmt"
	"strings"
	"sync/atomic"
	"time"

	appid "github.com/labstack/fanout/internal/id"
	"github.com/labstack/fanout/internal/queryrows"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
)

const (
	defaultWindow = time.Hour
	maxWindow     = 24 * time.Hour
	defaultLimit  = 100
	maxLimit      = 500
)

var (
	ErrInvalidScope = errors.New("invalid observability query scope")
	ErrInvalidLimit = errors.New("invalid observability query limit")
)

type DB = queryrows.Queryer

type Service struct {
	db             DB
	repository     *telemetrystore.Repository
	now            func() time.Time
	endpointMature atomic.Bool
}

func New(db DB, repository *telemetrystore.Repository) *Service {
	if db == nil || repository == nil {
		panic("observability requires query engine and telemetry repository")
	}
	return &Service{db: db, repository: repository, now: time.Now}
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
	if !scope.Start.Before(scope.End) || scope.End.Sub(scope.Start) > maxWindow {
		return Scope{}, fmt.Errorf("%w: window must be positive and at most %s", ErrInvalidScope, maxWindow)
	}
	scope.Namespace = strings.TrimSpace(scope.Namespace)
	return scope, nil
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
