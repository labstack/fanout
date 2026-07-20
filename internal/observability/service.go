package observability

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
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

// DB is the narrow database surface used by the query kernel. *sql.DB
// satisfies it, while tests can use sqlmock without a storage-specific fake.
type DB interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
}

type Service struct {
	db               DB
	defaultNamespace string
	now              func() time.Time
}

func New(db DB, defaultNamespace string) *Service {
	return &Service{db: db, defaultNamespace: defaultNamespace, now: time.Now}
}

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
	if strings.TrimSpace(scope.Namespace) == "" {
		scope.Namespace = s.defaultNamespace
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

func provenance(scope Scope) Provenance {
	return provenanceFor(scope, "service_rollup")
}

func provenanceFor(scope Scope, source string) Provenance {
	return Provenance{
		QueryID:    uuid.NewString(),
		Window:     scope.Start.Format(time.RFC3339Nano) + "/" + scope.End.Format(time.RFC3339Nano),
		Generated:  time.Now().UTC(),
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
