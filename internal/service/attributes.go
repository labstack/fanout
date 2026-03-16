package service

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"log/slog"
	"strings"
)

// AttributeParams contains parameters for attribute discovery.
type AttributeParams struct {
	Signal    string // "spans", "logs", "metrics"
	Service   string
	Operation string // spans only
	Window    int    // minutes
	Namespace string
	TenantID  string
	Limit     int
}

// AttributesResult holds discovered attributes with metadata.
type AttributesResult struct {
	Signal             string           `json:"signal"`
	Attributes         []AttributeInfo  `json:"attributes"`
	ResourceAttributes []AttributeInfo  `json:"resource_attributes"`
	TotalSpans         int64            `json:"total_rows"`
}

// AttributeInfo describes a discovered attribute key.
type AttributeInfo struct {
	Key         string   `json:"key"`
	Count       int64    `json:"count"`
	Cardinality int64    `json:"cardinality"`
	Samples     []string `json:"samples"`
}

// Attributes discovers attribute keys present in the data for a signal.
func (s *Service) Attributes(ctx context.Context, p AttributeParams) (*AttributesResult, error) {
	if p.Signal == "" {
		p.Signal = "spans"
	}
	if p.Window == 0 {
		p.Window = 60
	}
	if p.Limit == 0 {
		p.Limit = 50
	}
	p.Namespace, p.TenantID = s.defaults(p.Namespace, p.TenantID)

	switch p.Signal {
	case "spans", "logs", "metrics":
	default:
		return nil, fmt.Errorf("invalid signal %q: use spans, logs, or metrics", p.Signal)
	}

	out := &AttributesResult{
		Signal:             p.Signal,
		Attributes:         []AttributeInfo{},
		ResourceAttributes: []AttributeInfo{},
	}

	// Build filters
	var clauses []string
	var args []any
	clauses = append(clauses, fmt.Sprintf("time >= now() - INTERVAL %d MINUTE", p.Window))
	if p.Service != "" {
		clauses = append(clauses, "service = ?")
		args = append(args, p.Service)
	}
	if p.Operation != "" && p.Signal == "spans" {
		clauses = append(clauses, "operation = ?")
		args = append(args, p.Operation)
	}
	if p.Namespace != "" {
		clauses = append(clauses, "namespace = ?")
		args = append(args, p.Namespace)
	}
	if p.TenantID != "" {
		clauses = append(clauses, "tenant = ?")
		args = append(args, p.TenantID)
	}

	// Time column differs by signal
	timeCol := "start_time"
	if p.Signal == "logs" || p.Signal == "metrics" {
		timeCol = "time"
	}
	clauses[0] = fmt.Sprintf("%s >= now() - INTERVAL %d MINUTE", timeCol, p.Window)

	where := "WHERE " + strings.Join(clauses, " AND ")

	// Query attribute keys from attributes_json
	attrs, totalRows, err := s.discoverKeys(ctx, p.Signal, "attributes_json", where, args, p.Limit)
	if err != nil {
		return nil, fmt.Errorf("discover attributes: %w", err)
	}
	out.Attributes = attrs
	out.TotalSpans = totalRows

	// Query resource attribute keys from resource_json
	resAttrs, _, err := s.discoverKeys(ctx, p.Signal, "resource_json", where, args, p.Limit)
	if err != nil {
		slog.Warn("discover resource attributes failed", "signal", p.Signal, "err", err)
	} else {
		out.ResourceAttributes = resAttrs
	}

	return out, nil
}

// discoverKeys extracts JSON keys from a column, counting occurrences,
// cardinality, and sample values in a single query pass.
func (s *Service) discoverKeys(ctx context.Context, signal, jsonCol, where string, args []any, limit int) ([]AttributeInfo, int64, error) {
	// Single-pass: unnest json_keys, extract each key's value, and aggregate
	// counts + distinct values together. Avoids N+1 per-key queries.
	q := fmt.Sprintf(`
WITH total AS (
  SELECT COUNT(*) AS cnt FROM %s %s
),
kv AS (
  SELECT
    unnest(json_keys(%s)) AS key,
    %s AS doc
  FROM %s
  %s
  AND %s IS NOT NULL AND %s != ''
)
SELECT
  key,
  COUNT(*) AS count,
  COUNT(DISTINCT json_extract_string(doc, '$.' || key)) AS cardinality,
  to_json(list_slice(list(DISTINCT json_extract_string(doc, '$.' || key) ORDER BY random()), 1, 5)) AS samples,
  (SELECT cnt FROM total) AS total_rows
FROM kv
GROUP BY key
ORDER BY count DESC
LIMIT %d`,
		signal, where,
		jsonCol, jsonCol, signal, where, jsonCol, jsonCol,
		limit)

	rows, err := s.duck.DB.QueryContext(ctx, q, append(args, args...)...)
	if err != nil {
		return nil, 0, fmt.Errorf("keys query: %w", err)
	}
	defer rows.Close()

	var result []AttributeInfo
	var totalRows int64
	for rows.Next() {
		var info AttributeInfo
		var samplesJSON sql.NullString
		if err := rows.Scan(&info.Key, &info.Count, &info.Cardinality, &samplesJSON, &totalRows); err != nil {
			slog.Warn("keys scan failed", "err", err)
			continue
		}
		if samplesJSON.Valid {
			if err := json.Unmarshal([]byte(samplesJSON.String), &info.Samples); err != nil {
				slog.Debug("samples parse failed", "key", info.Key, "err", err)
			}
		}
		if info.Samples == nil {
			info.Samples = []string{}
		}
		result = append(result, info)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("keys iteration error", "err", err)
	}

	if result == nil {
		result = []AttributeInfo{}
	}
	return result, totalRows, nil
}
