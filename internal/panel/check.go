package panel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"

	duckdb "github.com/duckdb/duckdb-go/v2"
	"github.com/labstack/fanout/internal/queryrows"
)

// Parser is the slice of the query engine that checks SQL text. *query.Duck
// implements it.
type Parser interface {
	ParseSQL(ctx context.Context, query string) (map[string]any, error)
	RenderSQL(ctx context.Context, node map[string]any) (string, error)
	PrepareTelemetrySQL(ctx context.Context, query, describe string, maxRows int) (string, []bool, error)
}

// Checked is what the database-backed checks produced.
type Checked struct {
	AnnotationFilters map[string][]AnnotationFilter // panel id → projected annotation filters
	Filters           map[string][]Filter           // panel id → filters
	VarFilters        map[string][]Filter           // variable name → filters
	Measures          map[string][]Measure          // panel id → measures
}

// isOperational reports whether err is the engine's or the caller's failure
// (cancellation, deadline, interrupt, closed database) rather than a fault in
// the spec. The engine reports a cancelled query as a bare interrupt.
func isOperational(err error) bool {
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, sql.ErrConnDone) || (err != nil && strings.Contains(err.Error(), "database is closed")) {
		return true
	}
	var engineError *duckdb.Error
	if errors.As(err, &engineError) {
		switch engineError.Type {
		case duckdb.ErrorTypeIO, duckdb.ErrorTypeOutOfMemory, duckdb.ErrorTypeInterrupt,
			duckdb.ErrorTypeInternal, duckdb.ErrorTypeFatal, duckdb.ErrorTypeConnection,
			duckdb.ErrorTypeSerialization, duckdb.ErrorTypeTransaction:
			return true
		}
	}
	return false
}

// Check runs Validate and then the checks that need DuckDB's parser: every
// filter expression and every SQL panel. It reports all problems it finds.
// An operational failure (cancelled request, expired deadline, closed
// database) stops the check and is returned as the error, never as a Problem.
// The dashboard must already be normalized.
func Check(ctx context.Context, parser Parser, d *Dashboard) (*Checked, Problems, error) {
	problems := Validate(d)
	if len(problems) > 0 {
		return nil, problems, nil
	}
	checked := &Checked{AnnotationFilters: map[string][]AnnotationFilter{}, Filters: map[string][]Filter{}, VarFilters: map[string][]Filter{}, Measures: map[string][]Measure{}}
	earlier := map[string]Variable{}
	for i, v := range d.Variables {
		if v.Kind == "query" {
			if sig, ok := lookupSignal(v.From); ok {
				for j, expr := range v.Where {
					f, err := checkFilter(ctx, parser, sig, earlier, expr)
					if err != nil {
						if isOperational(err) {
							return nil, nil, err
						}
						problems.add(fmt.Sprintf("variables[%d].where[%d]", i, j), PresentError(err, SQLError))
						continue
					}
					checked.VarFilters[v.Name] = append(checked.VarFilters[v.Name], f)
				}
			}
		}
		earlier[v.Name] = v
	}
	for i := range d.Panels {
		p := &d.Panels[i]
		path := fmt.Sprintf("panels[%d]", i)
		switch {
		case p.Query != nil:
			sig, ok := lookupSignal(p.Query.From)
			if !ok {
				continue
			}
			var ignored Problems
			checked.Measures[p.ID] = parseMeasures(sig, p.Query.Measures, "", &ignored)
			for j := range checked.Measures[p.ID] {
				m := &checked.Measures[p.ID][j]
				expr, err := checkMeasureExpr(ctx, parser, sig, m.Expr)
				if err != nil {
					if isOperational(err) {
						return nil, nil, err
					}
					problems.add(fmt.Sprintf("%s.query.measures[%d]", path, j), PresentError(err, SQLError))
					continue
				}
				m.Expr = expr
				better := p.Better
				if better == "" {
					better = inferBetterExpr(m.Expr, p, sig)
				}
				m.Rank = rankRule(*m, sig, better)
			}

			for j, expr := range p.Query.Where {
				f, err := checkFilter(ctx, parser, sig, earlier, expr)
				if err != nil {
					if isOperational(err) {
						return nil, nil, err
					}
					problems.add(fmt.Sprintf("%s.query.where[%d]", path, j), PresentError(err, SQLError))
					continue
				}
				checked.Filters[p.ID] = append(checked.Filters[p.ID], f)
				if err := checkDeployScope(ctx, parser, p, f); err != nil {
					if isOperational(err) {
						return nil, nil, err
					}
					problems.addHint(path+".options.split", PresentError(err, SQLError), "use standalone service and optional namespace equalities")
				}

				if p.Viz == "timeseries" || p.Viz == "heatmap" || p.Viz == "state_timeline" || p.Viz == "health" {
					projected, err := projectAnnotationFilter(ctx, parser, sig, earlier, f)
					if err != nil {
						if isOperational(err) {
							return nil, nil, err
						}
						problems.addHint(fmt.Sprintf("%s.query.where[%d]", path, j), PresentError(err, SQLError), "use a supported service or namespace predicate")
					} else if projected != nil {
						checked.AnnotationFilters[p.ID] = append(checked.AnnotationFilters[p.ID], *projected)
					}
				}

				if p.Viz == "health" || p.Viz == "service_map" {
					sample := Scope{Vars: map[string]Value{}}
					for name := range earlier {
						sample.Vars[name] = Value{Values: []string{"check"}}
					}
					if _, _, err := rollupFilterValue(ctx, parser, f, sample); err != nil {
						if isOperational(err) {
							return nil, nil, err
						}
						problems.addHint(fmt.Sprintf("%s.query.where[%d]", path, j), PresentError(err, SQLError), "use namespace or service equality with a literal or single-value variable")
					}
				}

			}
			validateMetricName(p, checked.Filters[p.ID], earlier, path, &problems)
			if p.Options != nil && p.Options.Split == "deploy" {
				found := false
				for _, f := range checked.Filters[p.ID] {
					if f.EqField == "service" {
						found = true
					}
				}
				if !found {
					problems.addHint(path+".options.split", "deploy split requires a service equality filter", "add service = 'name' or service = $service")
				}
			}

		case p.SQL != "":
			if err := checkSQLPanel(ctx, parser, p, earlier); err != nil {
				if isOperational(err) {
					return nil, nil, err
				}
				problems = append(problems, sqlProblem(path+".sql", err))
			}
		}
	}
	return checked, problems, nil
}

func checkSQLPanel(ctx context.Context, parser Parser, p *Panel, vars map[string]Variable) error {
	expanded, err := expandMacros(p.SQL, time.Minute)
	if err != nil {
		return err
	}
	canonical, err := canonicalSQL(ctx, parser, expanded)
	if err != nil {
		return err
	}
	query, describe, _, err := bindParams(canonical, func(name string) ([]any, bool, error) {
		if name == "__from" || name == "__to" {
			return []any{time.Time{}}, false, nil
		}
		if _, ok := vars[name]; !ok {
			return nil, false, fmt.Errorf("$%s is not a dashboard variable", name)
		}
		return []any{""}, false, nil
	})
	if err != nil {
		return err
	}
	_, _, err = parser.PrepareTelemetrySQL(ctx, query, describe, 1)
	if err != nil || p.Viz != "state_timeline" {
		return err
	}
	reader, ok := parser.(queryrows.Queryer)
	if !ok {
		return fmt.Errorf("SQL timeline description unavailable")
	}
	// Describe the projected SELECT through a zero-row execution. This keeps
	// the normal snapshot wrapper (which only accepts SELECT) and read guards.
	prepared, serialized, err := parser.PrepareTelemetrySQL(ctx, describe, describe, 0)
	if err != nil {
		return err
	}
	rows, err := reader.QueryContext(ctx, prepared)
	if err != nil {
		return err
	}
	defer rows.Close()
	typed, ok := rows.(interface {
		ColumnTypes() ([]*sql.ColumnType, error)
	})
	if !ok {
		return fmt.Errorf("SQL timeline column types unavailable")
	}
	types, err := typed.ColumnTypes()
	if err != nil {
		return err
	}
	var columns []Column
	for i, t := range types {
		name, kind := t.Name(), t.DatabaseTypeName()
		c := Column{Name: name, Type: "string", Role: "dimension"}
		if i < len(serialized) && serialized[i] {
			c.Type = "other"
		} else if strings.HasPrefix(kind, "TIMESTAMP") {
			c.Type = "time"
			c.Role = "time"
		} else if kind == "DOUBLE" || kind == "FLOAT" || strings.Contains(kind, "INT") || strings.HasPrefix(kind, "DECIMAL") {
			c.Type = "number"
			c.Role = "measure"
		} else if kind != "VARCHAR" {
			c.Type = "other"
		}
		columns = append(columns, c)
	}
	if err := rows.Err(); err != nil {
		return err
	}
	return validateTimelineColumns(columns)
}

func validateTimelineColumns(columns []Column) error {
	if len(columns) < 3 || len(columns) > 4 || columns[0].Name != "time" || columns[0].Type != "time" || columns[1].Type != "string" || columns[2].Type != "number" || (len(columns) == 4 && (columns[3].Name != "state" || columns[3].Type != "string")) {
		return Problems{{Message: "SQL state_timeline requires timestamp time, a string item, one numeric measure and optional string state"}}
	}
	return nil
}

// checkMeasureExpr sends each conditional aggregate through the same WHERE
// allowlist as panel filters, with no dashboard variables in its population.
func checkMeasureExpr(ctx context.Context, parser Parser, sig *signal, e MeasureExpr) (MeasureExpr, error) {
	switch leaf := e.(type) {
	case MeasureCountIf:
		if varRefPattern.MatchString(stripQuoted(leaf.Filter)) {
			return nil, errors.New("variables are not allowed inside count_if")
		}
		f, err := checkFilter(ctx, parser, sig, nil, leaf.Filter)
		if err != nil {
			return nil, err
		}
		if len(f.Params) > 0 {
			return nil, errors.New("variables are not allowed inside count_if")
		}
		leaf.Expr = f.Expr
		return leaf, nil
	case MeasureBinary:
		l, err := checkMeasureExpr(ctx, parser, sig, leaf.L)
		if err != nil {
			return nil, err
		}
		r, err := checkMeasureExpr(ctx, parser, sig, leaf.R)
		if err != nil {
			return nil, err
		}
		leaf.L, leaf.R = l, r
		return leaf, nil
	case MeasureNeg:
		x, err := checkMeasureExpr(ctx, parser, sig, leaf.X)
		leaf.X = x
		return leaf, err
	}
	return e, nil
}
