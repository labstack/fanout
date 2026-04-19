package service

import (
	"github.com/labstack/fanout/internal/env"
	"github.com/labstack/fanout/internal/query"
)

// Service provides shared business logic for MCP tools and web UI.
type Service struct {
	duck *query.Duck
	cfg  env.Config
}

// New creates a new Service instance.
func New(duck *query.Duck, cfg env.Config) *Service {
	return &Service{duck: duck, cfg: cfg}
}

// ResolveNamespace returns the effective namespace (empty means all).
func (s *Service) ResolveNamespace(namespace string) string {
	return namespace
}
