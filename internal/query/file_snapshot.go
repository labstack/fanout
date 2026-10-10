package query

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"unicode/utf8"

	"github.com/labstack/fanout/internal/queryrows"
	"github.com/labstack/fanout/internal/telemetry"
)

type snapshotSQL interface {
	QueryRowContext(context.Context, string, ...any) *sql.Row
	QueryContext(context.Context, string, ...any) (*sql.Rows, error)
}

func (d *Duck) CompletedBatchReads() bool { return d.repository != nil }

func batchTime(b telemetry.BatchMetadata, signal string) (telemetry.TimeRange, int) {
	switch signal {
	case "spans":
		return b.SpanTime, b.Spans
	case "logs":
		return b.LogTime, b.Logs
	default:
		return b.MetricTime, b.Metrics
	}
}
func overlapping(b telemetry.BatchMetadata, signal string, w queryrows.Window) bool {
	bounds, count := batchTime(b, signal)
	return count > 0 && (!bounds.Known || bounds.MaxNanos >= w.Start.UnixNano() && bounds.MinNanos < w.End.UnixNano())
}
func (d *Duck) physicalSource(signal string, batches []telemetry.BatchMetadata) string {
	sort.SliceStable(batches, func(i, j int) bool {
		a, _ := batchTime(batches[i], signal)
		b, _ := batchTime(batches[j], signal)
		if a.MaxNanos != b.MaxNanos {
			return a.MaxNanos > b.MaxNanos
		}
		return batches[i].ID < batches[j].ID
	})
	files := make([]string, 0, len(batches))
	for _, b := range batches {
		files = append(files, sqlLiteral(filepath.ToSlash(filepath.Join(d.repository.Parquet.Dir(), "batches", b.ID+".batch", signal+".parquet"))))
	}
	if len(files) == 0 {
		files = append(files, sqlLiteral(filepath.ToSlash(filepath.Join(d.repository.Parquet.Dir(), "batches", "_schema.batch", signal+".parquet"))))
	}
	projection := "*"
	if signal == "spans" {
		projection += " EXCLUDE (_trace_hash), TRY_CAST(attributes['messaging.system'] AS VARCHAR) AS messaging_system, TRY_CAST(attributes['messaging.destination.name'] AS VARCHAR) AS messaging_destination"
	}
	return "SELECT " + projection + " FROM read_parquet([" + strings.Join(files, ",") + "], union_by_name=false, hive_partitioning=false)"
}

// snapshotSource uses a glob when pruning saves little. The filename predicate
// admits exactly the captured IDs, including when ingest publishes a new file
// after metadata capture. Narrow subsets keep explicit lists and footer pruning.
func (d *Duck) snapshotSource(signal string, selected, active []telemetry.BatchMetadata) string {
	count := 0
	for _, b := range active {
		_, n := batchTime(b, signal)
		if n > 0 {
			count++
		}
	}
	if len(selected) < 64 || len(selected)*5 < count*4 {
		return d.physicalSource(signal, selected)
	}
	var ids []string
	for _, b := range selected {
		ids = append(ids, b.ID)
	}
	batchID := d.batchIDColumn(signal, "filename")
	projection := "*"
	if signal == "spans" {
		projection += " EXCLUDE (_trace_hash), TRY_CAST(attributes['messaging.system'] AS VARCHAR) AS messaging_system, TRY_CAST(attributes['messaging.destination.name'] AS VARCHAR) AS messaging_destination"
	}
	return "SELECT " + projection + " FROM read_parquet(" + sqlLiteral(d.repository.Parquet.Pattern(signal)) + ", union_by_name=false, hive_partitioning=false) WHERE " + batchID + " IN (" + idsSQL(ids) + ")"
}

func (d *Duck) batchIDColumn(signal, filename string) string {
	root := filepath.ToSlash(d.repository.Parquet.BatchesDir())
	suffix := ".batch/" + signal + ".parquet"
	return fmt.Sprintf("substr(%s,%d,length(%s)-%d)", filename, utf8.RuneCountInString(root)+2, filename, utf8.RuneCountInString(root)+1+len(suffix))
}

func cleanSource(signal, physical string) string {
	view := map[string]string{"spans": viewSpans, "logs": viewLogs, "metrics": viewMetrics}[signal]
	body := strings.SplitN(view, " AS\n", 2)[1]
	return strings.TrimSuffix(strings.Replace(body, "FROM telemetry."+signal, "FROM ("+physical+")", 1), ";")
}

// bindSnapshot rewrites table references through DuckDB's own AST. The caller
// holds the publication lease and a read transaction: batch markers, aggregate
// rows and immutable files all belong to the same snapshot. No session views
// survive a pooled connection, and parameter indices are preserved by the AST.
func (d *Duck) bindSnapshot(ctx context.Context, db snapshotSQL, query string, w queryrows.Window) (string, error) {
	if d.repository == nil {
		return query, nil
	}
	compiled, err := d.compileSnapshot(ctx, db, query)
	if err != nil {
		return "", err
	}
	batches := d.repository.Parquet.BatchMetadata()
	sources := map[string]string{}
	for _, signal := range []string{"spans", "logs", "metrics"} {
		if _, raw := compiled.names["telemetry."+signal]; !raw {
			if _, clean := compiled.names[signal]; !clean {
				continue
			}
		}
		selected := []telemetry.BatchMetadata{}
		for _, b := range batches {
			if overlapping(b, signal, w) {
				selected = append(selected, b)
			}
		}
		physical := d.snapshotSource(signal, selected, batches)
		sources["telemetry."+signal] = physical
		sources[signal] = cleanSource(signal, physical)
	}
	if w.Kind != queryrows.RawRead {
		if err := d.aggregateSources(ctx, db, batches, w, sources); err != nil {
			return "", err
		}
	}
	return bindCompiledSnapshot(compiled, sources)
}
func parseSQLTree(ctx context.Context, db snapshotSQL, query string) (map[string]any, error) {
	var serialized string
	if err := db.QueryRowContext(ctx, "SELECT json_serialize_sql(?::VARCHAR)::VARCHAR", query).Scan(&serialized); err != nil {
		return nil, err
	}
	var root map[string]any
	decoder := json.NewDecoder(strings.NewReader(serialized))
	decoder.UseNumber()
	if err := decoder.Decode(&root); err != nil {
		return nil, err
	}
	if root["error"] == true {
		return nil, fmt.Errorf("parse trusted SQL: %v", root["error_message"])
	}
	return root, nil
}
func compileRelations(ctx context.Context, db snapshotSQL, query string, sources map[string]string, used map[string]string) (string, error) {
	root, err := parseSQLTree(ctx, db, query)
	if err != nil {
		return "", err
	}
	// Conservatively retain unqualified references shadowed anywhere by a CTE.
	// Qualified telemetry references always refer to the physical relation.
	shadowed := map[string]bool{}
	var ctes func(any)
	ctes = func(value any) {
		switch v := value.(type) {
		case map[string]any:
			if entries, ok := v["cte_map"].(map[string]any); ok {
				if list, ok := entries["map"].([]any); ok {
					for _, entry := range list {
						if item, ok := entry.(map[string]any); ok {
							if name, ok := item["key"].(string); ok {
								shadowed[strings.ToLower(name)] = true
							}
						}
					}
				}
			}
			for _, child := range v {
				ctes(child)
			}
		case []any:
			for _, child := range v {
				ctes(child)
			}
		}
	}
	ctes(root)
	for name := range shadowed {
		if strings.HasPrefix(name, "__fanout_snapshot_") {
			return "", fmt.Errorf("reserved snapshot CTE name %s", name)
		}
	}
	// Resolve qualifiers in their original lexical scopes before table replacement.
	// Struct fields and aliases must retain the bindings used by DESCRIBE, and a
	// name a CTE shadows is never a physical relation.
	physical := make(map[string]string, len(sources))
	for key, body := range sources {
		if !shadowed[key] {
			physical[key] = body
		}
	}
	normalizeRelationQualifiers(root, physical)
	templates := map[string]map[string]any{}
	var walk func(any) error
	walk = func(value any) error {
		switch v := value.(type) {
		case map[string]any:
			if v["type"] == "BASE_TABLE" {
				catalog, _ := v["catalog_name"].(string)
				if catalog != "" {
					return nil
				}
				name, _ := v["table_name"].(string)
				schema, _ := v["schema_name"].(string)
				key := strings.ToLower(name)
				if schema != "" && schema != "main" {
					key = strings.ToLower(schema) + "." + key
				} else if shadowed[key] {
					return nil
				}
				if body, ok := sources[key]; ok {
					used[key] = strings.TrimPrefix(body, "SELECT * FROM ")
					template := templates[key]
					if template == nil {
						parsed, e := parseSQLTree(ctx, db, "SELECT * FROM ("+body+") AS snapshot_relation")
						if e != nil {
							return e
						}
						template = parsed["statements"].([]any)[0].(map[string]any)["node"].(map[string]any)["from_table"].(map[string]any)
						templates[key] = template
					}
					encoded, _ := json.Marshal(template)
					var replacement map[string]any
					decoder := json.NewDecoder(strings.NewReader(string(encoded)))
					decoder.UseNumber()
					if err := decoder.Decode(&replacement); err != nil {
						return err
					}
					alias, _ := v["alias"].(string)
					if alias == "" {
						alias = name
					}
					replacement["alias"] = alias
					for _, field := range []string{"sample", "column_name_alias", "query_location"} {
						if x, ok := v[field]; ok {
							replacement[field] = x
						}
					}
					for k := range v {
						delete(v, k)
					}
					for k, x := range replacement {
						v[k] = x
					}
					return nil
				}
			}
			for _, child := range v {
				if e := walk(child); e != nil {
					return e
				}
			}
		case []any:
			for _, child := range v {
				if e := walk(child); e != nil {
					return e
				}
			}
		}
		return nil
	}
	if err := walk(root); err != nil {
		return "", err
	}
	encoded, err := json.Marshal(root)
	if err != nil {
		return "", err
	}
	var rewritten string
	err = db.QueryRowContext(ctx, "SELECT json_deserialize_sql(?::JSON)::VARCHAR", string(encoded)).Scan(&rewritten)
	return rewritten, err
}

type compiledSnapshot struct {
	query string
	names map[string]string
}

// compileSnapshot compiles relation aliases once with the native parser. Per-read
// scopes become ordinary CTEs, avoiding repeated JSON SQL parse/deserialize work.
func (d *Duck) compileSnapshot(ctx context.Context, db snapshotSQL, query string) (compiledSnapshot, error) {
	d.snapshotMu.Lock()
	compiled, ok := d.snapshotQueries[query]
	d.snapshotMu.Unlock()
	if !ok {
		aliases := map[string]string{}
		for _, name := range []string{"telemetry.spans", "telemetry.logs", "telemetry.metrics", "spans", "logs", "metrics", "trace_candidates", "trace_tail"} {
			alias := "__fanout_snapshot_" + strings.ReplaceAll(name, ".", "_")
			aliases[name] = "SELECT * FROM " + alias
		}
		used := map[string]string{}
		rewritten, err := compileRelations(ctx, db, query, aliases, used)
		if err != nil {
			return compiledSnapshot{}, err
		}
		// The AST records only rewritten table references, never SQL literals.
		compiled = compiledSnapshot{query: rewritten, names: used}
		d.snapshotMu.Lock()
		if d.snapshotQueries == nil || len(d.snapshotQueries) >= 128 {
			d.snapshotQueries = map[string]compiledSnapshot{}
		}
		d.snapshotQueries[query] = compiled
		d.snapshotMu.Unlock()
	}
	return compiled, nil
}

func bindCompiledSnapshot(compiled compiledSnapshot, sources map[string]string) (string, error) {
	definitions := []string{}
	for name, alias := range compiled.names {
		body, ok := sources[name]
		if !ok {
			return "", fmt.Errorf("missing snapshot relation %s", name)
		}
		materialization := " AS ("
		if name == "trace_candidates" {
			// This relation is used by both the top-one and touched-trace branches.
			// Avoid copying every retained candidate into a temporary relation.
			// Mixed-scope aggregates retain DuckDB's required barriers.
			materialization = " AS NOT MATERIALIZED ("
		}
		definitions = append(definitions, alias+materialization+body+")")
	}
	if len(definitions) == 0 {
		return compiled.query, nil
	}
	sort.Strings(definitions)
	statement := strings.TrimSpace(compiled.query)
	upper := strings.ToUpper(statement)
	switch {
	case strings.HasPrefix(upper, "WITH RECURSIVE "):
		return "WITH RECURSIVE " + strings.Join(definitions, ",") + ", " + statement[len("WITH RECURSIVE "):], nil
	case strings.HasPrefix(upper, "WITH "):
		return "WITH " + strings.Join(definitions, ",") + ", " + statement[len("WITH "):], nil
	default:
		return "WITH " + strings.Join(definitions, ",") + " " + statement, nil
	}
}
