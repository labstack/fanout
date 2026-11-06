package query

import (
	"context"
	"time"

	"github.com/labstack/fanout/internal/engine"
)

// The physical format and approved relation schemas are fixed for an executor's
// lifetime. Cache only validated SQL/projection templates, never arguments,
// snapshots or result rows. Every execution still binds its current variables
// and window and takes a new pinned read transaction in the engine.
type sqlPanelPlan struct {
	canonical, prepared string
	serialized          []bool
	patterns            map[string]bool
}

func (e *Executor) sqlPanel(ctx context.Context, source string, interval time.Duration) (sqlPanelPlan, error) {
	expanded, err := expandMacros(source, interval)
	if err != nil {
		return sqlPanelPlan{}, err
	}
	e.mu.Lock()
	plan, ok := e.sqlPanels[expanded]
	e.mu.Unlock()
	if ok {
		return plan, nil
	}
	canonical, err := canonicalSQL(ctx, e.engine, expanded)
	if err != nil {
		return sqlPanelPlan{}, err
	}
	node, err := e.engine.ParseSQL(ctx, canonical)
	if err != nil {
		return sqlPanelPlan{}, err
	}
	query, describe, _, err := bindParams(canonical, func(name string) ([]any, bool, error) {
		if name == "__from" || name == "__to" {
			return []any{time.Time{}}, false, nil
		}
		return []any{""}, false, nil
	})
	if err != nil {
		return sqlPanelPlan{}, err
	}
	prepared, serialized, err := e.engine.PrepareTelemetrySQL(ctx, query, describe, sqlPanelRows+1)
	if err != nil {
		return sqlPanelPlan{}, err
	}
	plan = sqlPanelPlan{canonical: canonical, prepared: prepared, serialized: serialized, patterns: engine.PatternParams(node)}
	e.mu.Lock()
	if e.sqlPanels == nil || len(e.sqlPanels) >= 128 {
		e.sqlPanels = map[string]sqlPanelPlan{}
	}
	e.sqlPanels[expanded] = plan
	e.mu.Unlock()
	return plan, nil
}
