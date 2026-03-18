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
	Signal             string          `json:"signal"`
	Attributes         []AttributeInfo `json:"attributes"`
	ResourceAttributes []AttributeInfo `json:"resource_attributes"`
	TotalRows          int64           `json:"total_rows"`
	Warnings           []string        `json:"warnings,omitempty"`
}

// AttributeInfo describes a discovered attribute key.
type AttributeInfo struct {
	Key             string   `json:"key"`
	Count           int64    `json:"count"`
	Cardinality     int64    `json:"cardinality"`
	Samples         []string `json:"samples"`
	DiscoveryMethod string   `json:"discovery_method,omitempty"`
}

// spanAttrColumns maps pre-extracted span column names to their OTel attribute keys.
var spanAttrColumns = []struct {
	Column string
	Key    string
}{
	{"http_method", "http.method"},
	{"http_status_code", "http.status_code"},
	{"http_route", "http.route"},
	{"db_system", "db.system"},
	{"rpc_method", "rpc.method"},
	{"rpc_service", "rpc.service"},
	{"peer_service", "peer.service"},
	{"exception_type", "exception.type"},
	{"exception_message", "exception.message"},
}

// spanResourceColumns maps pre-extracted resource column names to their OTel keys.
var spanResourceColumns = []struct {
	Column string
	Key    string
}{
	{"service_version", "service.version"},
	{"deployment_env", "deployment.environment"},
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
	case "spans":
		return s.attributesFromColumns(ctx, p)
	case "logs", "metrics":
		return s.attributesFromJSON(ctx, p)
	default:
		return nil, fmt.Errorf("invalid signal %q: use spans, logs, or metrics", p.Signal)
	}
}

// attributesFromColumns discovers span attributes using pre-extracted Parquet columns.
// Uses UNPIVOT to efficiently scan all columns in a single query — no JSON parsing.
func (s *Service) attributesFromColumns(ctx context.Context, p AttributeParams) (*AttributesResult, error) {
	out := &AttributesResult{
		Signal:             "spans",
		Attributes:         []AttributeInfo{},
		ResourceAttributes: []AttributeInfo{},
	}

	// Build WHERE clause
	var clauses []string
	var args []any
	clauses = append(clauses, fmt.Sprintf("start_time >= now() - INTERVAL %d MINUTE", p.Window))
	if p.Service != "" {
		clauses = append(clauses, "service = ?")
		args = append(args, p.Service)
	}
	if p.Operation != "" {
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
	where := "WHERE " + strings.Join(clauses, " AND ")

	// Get total row count
	countQ := fmt.Sprintf("SELECT COUNT(*) FROM spans %s", where)
	var totalRows int64
	if err := s.duck.DB.QueryRowContext(ctx, countQ, args...).Scan(&totalRows); err != nil {
		slog.Warn("attributes count query failed", "err", err)
	}
	out.TotalRows = totalRows

	// Discover span attributes via UNPIVOT on pre-extracted columns.
	// Build column list for UNPIVOT.
	var cols []string
	colToKey := map[string]string{}
	for _, c := range spanAttrColumns {
		cols = append(cols, c.Column)
		colToKey[c.Column] = c.Key
	}

	q := fmt.Sprintf(`
WITH data AS (
  SELECT %s FROM spans %s
)
SELECT key, COUNT(*) AS count,
       COUNT(DISTINCT val) AS cardinality,
       to_json(list_slice(list(DISTINCT val), 1, 5))::VARCHAR AS samples
FROM (UNPIVOT data ON %s INTO NAME key VALUE val)
WHERE val IS NOT NULL AND val != ''
GROUP BY key
ORDER BY count DESC`,
		strings.Join(cols, ", "), where,
		strings.Join(cols, ", "))

	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		return nil, fmt.Errorf("attributes query: %w", err)
	}
	defer rows.Close()

	for rows.Next() {
		var info AttributeInfo
		var samplesJSON sql.NullString
		if err := rows.Scan(&info.Key, &info.Count, &info.Cardinality, &samplesJSON); err != nil {
			slog.Warn("attributes scan failed", "err", err)
			continue
		}
		// Map column name back to OTel key
		if otelKey, ok := colToKey[info.Key]; ok {
			info.Key = otelKey
		}
		info.DiscoveryMethod = "column"
		if samplesJSON.Valid {
			if err := json.Unmarshal([]byte(samplesJSON.String), &info.Samples); err != nil {
				slog.Warn("samples parse failed", "key", info.Key, "err", err)
			}
		}
		if info.Samples == nil {
			info.Samples = []string{}
		}
		out.Attributes = append(out.Attributes, info)
	}
	if err := rows.Err(); err != nil {
		return out, fmt.Errorf("attributes iteration: %w", err)
	}

	// Discover resource attributes from pre-extracted columns
	var resCols []string
	resColToKey := map[string]string{}
	for _, c := range spanResourceColumns {
		resCols = append(resCols, c.Column)
		resColToKey[c.Column] = c.Key
	}

	resQ := fmt.Sprintf(`
WITH data AS (
  SELECT %s FROM spans %s
)
SELECT key, COUNT(*) AS count,
       COUNT(DISTINCT val) AS cardinality,
       to_json(list_slice(list(DISTINCT val), 1, 5))::VARCHAR AS samples
FROM (UNPIVOT data ON %s INTO NAME key VALUE val)
WHERE val IS NOT NULL AND val != ''
GROUP BY key
ORDER BY count DESC`,
		strings.Join(resCols, ", "), where,
		strings.Join(resCols, ", "))

	resRows, err := s.duck.DB.QueryContext(ctx, resQ, args...)
	if err != nil {
		slog.Warn("resource attributes query failed", "err", err)
		out.Warnings = append(out.Warnings, fmt.Sprintf("resource attribute discovery failed: %s", err))
	} else {
		defer resRows.Close()
		for resRows.Next() {
			var info AttributeInfo
			var samplesJSON sql.NullString
			if err := resRows.Scan(&info.Key, &info.Count, &info.Cardinality, &samplesJSON); err != nil {
				slog.Warn("resource attributes scan failed", "err", err)
				continue
			}
			if otelKey, ok := resColToKey[info.Key]; ok {
				info.Key = otelKey
			}
			info.DiscoveryMethod = "column"
			if samplesJSON.Valid {
				if err := json.Unmarshal([]byte(samplesJSON.String), &info.Samples); err != nil {
					slog.Warn("resource samples parse failed", "key", info.Key, "err", err)
				}
			}
			if info.Samples == nil {
				info.Samples = []string{}
			}
			out.ResourceAttributes = append(out.ResourceAttributes, info)
		}
		if err := resRows.Err(); err != nil {
			slog.Warn("resource attributes iteration error", "err", err)
		}
	}

	return out, nil
}

// attributesFromJSON discovers attributes by sampling the JSON blob.
// Used for logs and metrics which don't have pre-extracted columns.
func (s *Service) attributesFromJSON(ctx context.Context, p AttributeParams) (*AttributesResult, error) {
	// Both logs and metrics use "time" as their timestamp column and table name matches signal.
	table := p.Signal
	if table != "logs" && table != "metrics" {
		return nil, fmt.Errorf("attributesFromJSON: unsupported signal %q", p.Signal)
	}

	out := &AttributesResult{
		Signal:             p.Signal,
		Attributes:         []AttributeInfo{},
		ResourceAttributes: []AttributeInfo{},
		Warnings:           []string{"Counts are approximate — based on 1000-row sample"},
	}

	clauses := []string{fmt.Sprintf("time >= now() - INTERVAL %d MINUTE", p.Window)}
	var args []any
	if p.Service != "" {
		clauses = append(clauses, "service = ?")
		args = append(args, p.Service)
	}
	if p.Namespace != "" {
		clauses = append(clauses, "namespace = ?")
		args = append(args, p.Namespace)
	}
	if p.TenantID != "" {
		clauses = append(clauses, "tenant = ?")
		args = append(args, p.TenantID)
	}
	where := "WHERE " + strings.Join(clauses, " AND ")

	countQ := fmt.Sprintf("SELECT COUNT(*) FROM %s %s", table, where)
	var totalRows int64
	if err := s.duck.DB.QueryRowContext(ctx, countQ, args...).Scan(&totalRows); err != nil {
		slog.Warn("attributes count query failed", "signal", p.Signal, "err", err)
		out.Warnings = append(out.Warnings, fmt.Sprintf("Count query failed: %s — results may be incomplete", err))
	}
	out.TotalRows = totalRows

	attrs, attrsWarns := s.discoverJSONKeys(ctx, table, "attributes_json", where, args, p.Limit)
	out.Attributes = attrs
	out.Warnings = append(out.Warnings, attrsWarns...)

	resAttrs, resWarns := s.discoverJSONKeys(ctx, table, "resource_json", where, args, p.Limit)
	out.ResourceAttributes = resAttrs
	out.Warnings = append(out.Warnings, resWarns...)

	return out, nil
}

// discoverJSONKeys samples a JSON column and returns discovered attribute keys with counts.
// Returns the discovered attributes and any warnings for the caller to surface.
func (s *Service) discoverJSONKeys(ctx context.Context, table, jsonCol, where string, args []any, limit int) ([]AttributeInfo, []string) {
	// Use placeholder replacement to avoid repeating jsonCol 5 times in Sprintf.
	q := strings.ReplaceAll(fmt.Sprintf(`
WITH sample AS (
  SELECT {col} FROM %s %s AND {col} IS NOT NULL AND {col} != '' LIMIT 1000
),
kv AS (
  SELECT k AS key, json_extract_string({col}::JSON, '$.' || k) AS val
  FROM sample, UNNEST(json_keys({col}::JSON)) AS t(k)
)
SELECT key, COUNT(*) AS count, COUNT(DISTINCT val) AS cardinality
FROM kv
GROUP BY key
ORDER BY count DESC
LIMIT %d`, table, where, limit), "{col}", jsonCol)

	var warnings []string
	rows, err := s.duck.DB.QueryContext(ctx, q, args...)
	if err != nil {
		slog.Warn("JSON attribute discovery failed", "table", table, "col", jsonCol, "err", err)
		warnings = append(warnings, fmt.Sprintf("Discovery failed for %s.%s: %s", table, jsonCol, err))
		return []AttributeInfo{}, warnings
	}
	defer rows.Close()

	attrs := []AttributeInfo{}
	for rows.Next() {
		var info AttributeInfo
		if err := rows.Scan(&info.Key, &info.Count, &info.Cardinality); err != nil {
			slog.Warn("JSON attribute scan failed", "err", err)
			continue
		}
		info.Samples = []string{}
		info.DiscoveryMethod = "sample"
		attrs = append(attrs, info)
	}
	if err := rows.Err(); err != nil {
		slog.Warn("JSON attribute iteration error", "err", err)
		warnings = append(warnings, fmt.Sprintf("Partial results for %s.%s: %s", table, jsonCol, err))
	}
	return attrs, warnings
}
