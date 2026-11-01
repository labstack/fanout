package engine

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
)

// runTelemetrySQL runs one untrusted statement the way SQL panels and MCP
// query_telemetry do: PrepareTelemetrySQL, then QueryContext under the
// caller's window. Serialized columns stay JSON text, as panels return them.
func runTelemetrySQL(ctx context.Context, d *Duck, statement string, maxRows int) ([]map[string]any, error) {
	prepared, _, err := d.PrepareTelemetrySQL(ctx, statement, statement, maxRows)
	if err != nil {
		return nil, err
	}
	rows, err := d.QueryContext(ctx, prepared)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	out := []map[string]any{}
	for rows.Next() {
		values := make([]any, len(columns))
		pointers := make([]any, len(columns))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		row := map[string]any{}
		for i, name := range columns {
			row[name] = values[i]
		}
		out = append(out, row)
	}
	return out, rows.Err()
}

func mustRunTelemetrySQL(t testing.TB, ctx context.Context, d *Duck, statement string, maxRows int) []map[string]any {
	t.Helper()
	rows, err := runTelemetrySQL(ctx, d, statement, maxRows)
	if err != nil {
		t.Fatalf("%s: %v", statement, err)
	}
	return rows
}

// jsonValue decodes a serialized column as a panel frame delivers it.
func jsonValue(t testing.TB, value any) any {
	t.Helper()
	text, ok := value.(string)
	if !ok {
		t.Fatalf("serialized column is %T, not JSON text", value)
	}
	decoder := json.NewDecoder(strings.NewReader(NormalizeJSON(text)))
	decoder.UseNumber()
	var out any
	if err := decoder.Decode(&out); err != nil {
		t.Fatalf("decode %q: %v", text, err)
	}
	return out
}
