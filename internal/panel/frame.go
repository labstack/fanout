package panel

import (
	"database/sql"
	"fmt"
	"math"
	"math/big"
	"strings"
	"time"

	"github.com/labstack/fanout/internal/observability"
	"github.com/labstack/fanout/internal/queryrows"
)

// Frame is a columnar query result: one array of values per column.
type Frame struct {
	Trends    map[string][][]any `json:"trends,omitempty"`
	Note      string             `json:"note,omitempty"`
	Health    *HealthFrame       `json:"health,omitempty"`
	Columns   []Column           `json:"columns"`
	Values    [][]any            `json:"values"`
	Rows      int                `json:"rows"`
	Totals    []any              `json:"totals,omitempty"`
	Truncated bool               `json:"truncated,omitempty"`
	Trend     *Trend             `json:"trend,omitempty"`
	// Only structured time buckets are exempt from the row cap. A SQL time
	// column is still subject to the SQL panel cap.
	bucketed bool
}

// Trend locates each point in a row panel's dense trend array.
type Trend struct {
	StartMS int64 `json:"start_ms"`
	StepMS  int64 `json:"step_ms"`
}

const (
	maxFrameRows = 1000
	sqlPanelRows = 1000
)

func newFrame(columns []Column) *Frame {
	f := &Frame{Columns: columns, Values: make([][]any, len(columns))}
	for i := range f.Values {
		f.Values[i] = []any{}
	}
	return f
}

func (f *Frame) addNote(note string) {
	if strings.Contains(f.Note, note) {
		return
	}
	if f.Note != "" {
		f.Note += " "
	}
	f.Note += note
}

// scanFrame reads a compiled query whose column types are known.
func scanFrame(rows queryrows.Rows, columns []Column, maxRows int) (*Frame, error) {
	defer rows.Close()
	f := newFrame(columns)
	dest := make([]any, len(columns))
	for rows.Next() {
		if maxRows > 0 && f.Rows >= maxRows {
			f.Truncated = true
			break
		}
		for i, c := range columns {
			switch c.Type {
			case "time":
				dest[i] = new(sql.NullInt64)
			case "number":
				dest[i] = new(sql.NullFloat64)
			default:
				dest[i] = new(sql.NullString)
			}
		}
		if err := rows.Scan(dest...); err != nil {
			return nil, err
		}
		for i := range columns {
			f.Values[i] = append(f.Values[i], nullValue(dest[i]))
		}
		f.Rows++
	}
	return f, rows.Err()
}

func nullValue(v any) any {
	switch n := v.(type) {
	case *sql.NullInt64:
		if n.Valid {
			return n.Int64
		}
	case *sql.NullFloat64:
		if n.Valid && !math.IsNaN(n.Float64) && !math.IsInf(n.Float64, 0) {
			return n.Float64
		}
	case *sql.NullString:
		if n.Valid {
			return n.String
		}
	}
	return nil
}

// scanDynamicFrame reads an SQL panel, inferring each column's type from its
// first non-null value: timestamps become the time column, numbers become
// measures, everything else a dimension. serialized columns hold JSON text.
func scanDynamicFrame(rows queryrows.Rows, serialized []bool, maxRows int) (*Frame, error) {
	defer rows.Close()
	names, err := rows.Columns()
	if err != nil {
		return nil, err
	}
	var raw [][]any
	truncated := false
	for rows.Next() {
		if len(raw) >= maxRows {
			truncated = true
			break
		}
		values := make([]any, len(names))
		pointers := make([]any, len(names))
		for i := range values {
			pointers[i] = &values[i]
		}
		if err := rows.Scan(pointers...); err != nil {
			return nil, err
		}
		raw = append(raw, values)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	f := &Frame{Rows: len(raw), Truncated: truncated, Values: make([][]any, len(names))}
	timeTaken := false
	for i, name := range names {
		column := Column{Name: name, Type: "string", Role: "dimension"}
		if i < len(serialized) && serialized[i] {
			column.Type = "json"
		}
		for _, row := range raw {
			if row[i] != nil {
				column.Type = dynamicType(row[i], column.Type)
				break
			}
		}
		switch {
		case column.Type == "time" && !timeTaken:
			column.Role, timeTaken = "time", true
		case column.Type == "number":
			column.Role = "measure"
		}
		f.Columns = append(f.Columns, column)
		values := make([]any, len(raw))
		for r, row := range raw {
			values[r] = convertDynamic(row[i], column.Type)
		}
		f.Values[i] = values
	}
	return f, nil
}

type floater interface{ Float64() float64 }

func dynamicType(v any, fallback string) string {
	switch v.(type) {
	case time.Time:
		return "time"
	case int, int8, int16, int32, int64, uint, uint8, uint16, uint32, uint64, float32, float64, *big.Int, floater:
		return "number"
	}
	if fallback == "json" {
		return "json"
	}
	return "string"
}

func convertDynamic(v any, kind string) any {
	if v == nil {
		return nil
	}
	switch kind {
	case "time":
		if t, ok := v.(time.Time); ok {
			return t.UnixMilli()
		}
	case "number":
		var f float64
		switch n := v.(type) {
		case int:
			f = float64(n)
		case int8:
			f = float64(n)
		case int16:
			f = float64(n)
		case int32:
			f = float64(n)
		case int64:
			f = float64(n)
		case uint:
			f = float64(n)
		case uint8:
			f = float64(n)
		case uint16:
			f = float64(n)
		case uint32:
			f = float64(n)
		case uint64:
			f = float64(n)
		case float32:
			f = float64(n)
		case float64:
			f = n
		case *big.Int:
			f, _ = new(big.Float).SetInt(n).Float64()
		case floater:
			f = n.Float64()
		}
		if math.IsNaN(f) || math.IsInf(f, 0) {
			return nil
		}
		return f
	}
	switch s := v.(type) {
	case string:
		return s
	case []byte:
		return string(s)
	default:
		return fmt.Sprint(s)
	}
}

// totalsOf turns a one-row aggregate into per-column totals.
func totalsOf(f *Frame) []any {
	out := make([]any, len(f.Columns))
	if f.Rows == 0 {
		return out
	}
	for i := range f.Columns {
		out[i] = f.Values[i][0]
	}
	return out
}

type HealthFrame struct {
	Health       string                     `json:"health"`
	Counts       observability.HealthCounts `json:"counts"`
	TotalSpans   int64                      `json:"total_spans"`
	ErrorRate    float64                    `json:"error_rate"`
	ServiceCount int                        `json:"service_count"`
	ErrorTrend   []float64                  `json:"error_trend"`
}
