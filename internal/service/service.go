package service

import (
	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/query"
)

// Service provides shared business logic for MCP tools and web UI.
type Service struct {
	duck *query.Duck
	cfg  config.Config
}

// New creates a new Service instance.
func New(duck *query.Duck, cfg config.Config) *Service {
	return &Service{duck: duck, cfg: cfg}
}

// defaults returns namespace and tenantID with defaults applied.
// Queries are always scoped to a single partition.
func (s *Service) defaults(namespace, tenantID string) (string, string) {
	if namespace == "" {
		namespace = s.cfg.DefaultNS
	}
	if tenantID == "" {
		tenantID = s.cfg.TenantID.String()
	}
	return namespace, tenantID
}

