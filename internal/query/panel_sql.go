package query

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ParseSQL parses one statement with DuckDB's parser and returns its
// serialized node. It reads no telemetry and takes no snapshot.
func (d *Duck) ParseSQL(ctx context.Context, query string) (map[string]any, error) {
	return parseStatement(ctx, d.DB, query)
}

// PrepareTelemetrySQL checks one untrusted SELECT (both query and describe,
// since binding describe can invoke table functions) against the telemetry SQL
// rules and wraps it in the projection that keeps nested values away from the
// Go driver. describe is the same statement with every placeholder replaced
// by NULL, which DESCRIBE can bind; the returned statement keeps query's
// placeholders and is run with QueryContext under the panel's read window.
func (d *Duck) PrepareTelemetrySQL(ctx context.Context, query, describe string, maxRows int) (string, []bool, error) {
	if err := d.lockParquetRead(ctx, readerQuery); err != nil {
		return "", nil, err
	}
	defer d.parquetMu.RUnlock()
	conn, err := d.acquireRead(ctx)
	if err != nil {
		return "", nil, err
	}
	defer conn.Close()
	if err := validateSQLAST(ctx, conn, query); err != nil {
		return "", nil, err
	}
	if err := validateSQLAST(ctx, conn, describe); err != nil {
		return "", nil, err
	}
	trim := func(s string) string { return strings.TrimSpace(strings.TrimSuffix(strings.TrimSpace(s), ";")) }
	projection, aliases, serialized, err := describeProjection(ctx, conn, trim(describe))
	if err != nil {
		return "", nil, err
	}
	return fmt.Sprintf("SELECT %s FROM (%s) AS _result(%s) LIMIT %d", strings.Join(projection, ","), trim(query), strings.Join(aliases, ","), maxRows), serialized, nil
}

// RenderSQL renders a statement node (as returned by ParseSQL) back to SQL
// text with DuckDB's own serializer. The text is canonical: comments, dollar
// quotes and other lexical forms the author used cannot survive it, so a
// caller that checked the node can bind and run the rendering instead of the
// author's text.
func (d *Duck) RenderSQL(ctx context.Context, node map[string]any) (string, error) {
	wrapped, err := json.Marshal(map[string]any{"error": false, "statements": []any{map[string]any{"node": withoutLocations(node)}}})
	if err != nil {
		return "", err
	}
	var text string
	if err := d.DB.QueryRowContext(ctx, "SELECT json_deserialize_sql(?::JSON)", string(wrapped)).Scan(&text); err != nil {
		return "", fmt.Errorf("SQL rendering failed: %w", err)
	}
	return text, nil
}

// withoutLocations copies a node without its source locations. Some are the
// maximum uint64, which a float64 decode cannot hold and DuckDB's
// deserializer rejects; the rendering does not need them.
func withoutLocations(n any) any {
	switch v := n.(type) {
	case map[string]any:
		out := make(map[string]any, len(v))
		for key, child := range v {
			if key == "query_location" || key == "query_location_length" {
				continue
			}
			out[key] = withoutLocations(child)
		}
		return out
	case []any:
		out := make([]any, len(v))
		for i, child := range v {
			out[i] = withoutLocations(child)
		}
		return out
	}
	return n
}
