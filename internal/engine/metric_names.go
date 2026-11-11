package engine

import (
	"context"

	"github.com/labstack/pitot/internal/telemetry"
)

// Recognize a single direct metrics scan with a literal name predicate. No
// propagation through joins, aliases, subqueries or OR: a missed opportunity is
// harmless; excluding a file without proof is not. CTE shadowing has already
// been excluded by compileRelations.
func singleMetricNames(root any) []string {
	scans := 0
	var names []string
	var walk func(any)
	walk = func(v any) {
		switch n := v.(type) {
		case map[string]any:
			if n["type"] == "BASE_TABLE" && n["table_name"] == "metrics" {
				scans++
			}
			if from, ok := n["from_table"].(map[string]any); ok && from["type"] == "BASE_TABLE" && from["table_name"] == "metrics" && from["alias"] == "" {
				schema, _ := from["schema_name"].(string)
				if schema == "" || schema == "main" {
					names = metricNamePredicate(n["where_clause"])
				}
			}
			for _, child := range n {
				walk(child)
			}
		case []any:
			for _, child := range n {
				walk(child)
			}
		}
	}
	walk(root)
	if scans != 1 {
		return nil
	}
	return names
}

func metricNamePredicate(value any) []string {
	n, ok := value.(map[string]any)
	if !ok {
		return nil
	}
	if n["type"] == "CONJUNCTION_AND" {
		children, _ := n["children"].([]any)
		for _, child := range children {
			if names := metricNamePredicate(child); len(names) > 0 {
				return names
			}
		}
	}
	column := func(v any) bool {
		c, ok := v.(map[string]any)
		if !ok || c["class"] != "COLUMN_REF" {
			return false
		}
		cols, _ := c["column_names"].([]any)
		return len(cols) == 1 && cols[0] == "name"
	}
	literal := func(v any) (string, bool) {
		c, ok := v.(map[string]any)
		if !ok || c["class"] != "CONSTANT" {
			return "", false
		}
		l, _ := c["literal"].(map[string]any)
		text, ok := l["text"].(string)
		return text, ok && l["kind"] == "STRING"
	}
	if n["type"] == "COMPARE_EQUAL" {
		for _, sides := range [][2]any{{n["left"], n["right"]}, {n["right"], n["left"]}} {
			if column(sides[0]) {
				if text, ok := literal(sides[1]); ok {
					return []string{text}
				}
			}
		}
	}
	if n["type"] == "COMPARE_IN" {
		children, _ := n["children"].([]any)
		if len(children) < 2 || !column(children[0]) {
			return nil
		}
		names := []string{}
		for _, child := range children[1:] {
			text, ok := literal(child)
			if !ok {
				return nil
			}
			names = append(names, text)
		}
		return names
	}
	return nil
}

// Markers and name summaries are read in the caller's pinned read transaction.
// Missing markers keep the raw file visible immediately, regardless of lag.
func pruneMetricNames(ctx context.Context, db snapshotSQL, batches []telemetry.BatchMetadata, names []string) ([]telemetry.BatchMetadata, error) {
	if len(batches) == 0 {
		return batches, nil
	}
	ids := make([]string, 0, len(batches))
	for _, b := range batches {
		ids = append(ids, b.ID)
	}
	rows, err := db.QueryContext(ctx, `SELECT batch_id FROM read_batches b WHERE batch_id IN (`+idsSQL(ids)+`) AND NOT EXISTS (SELECT 1 FROM read_metric_names m WHERE m.batch_id=b.batch_id AND m.name IN (`+idsSQL(names)+`))`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	excluded := map[string]bool{}
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			return nil, err
		}
		excluded[id] = true
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	selected := make([]telemetry.BatchMetadata, 0, len(batches))
	for _, b := range batches {
		if !excluded[b.ID] {
			selected = append(selected, b)
		}
	}
	return selected, nil
}
