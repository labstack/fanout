package panel

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
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
	Filters    map[string][]Filter  // panel id → filters
	VarFilters map[string][]Filter  // variable name → filters
	Measures   map[string][]Measure // panel id → measures
}

// isOperational reports whether err is the engine's or the caller's failure
// (cancellation, deadline, closed database) rather than a fault in the spec.
func isOperational(err error) bool {
	return errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) ||
		errors.Is(err, sql.ErrConnDone) || (err != nil && strings.Contains(err.Error(), "database is closed"))
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
	checked := &Checked{Filters: map[string][]Filter{}, VarFilters: map[string][]Filter{}, Measures: map[string][]Measure{}}
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
						problems.add(fmt.Sprintf("variables[%d].where[%d]", i, j), SafeError(err))
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
			for j, expr := range p.Query.Where {
				f, err := checkFilter(ctx, parser, sig, earlier, expr)
				if err != nil {
					if isOperational(err) {
						return nil, nil, err
					}
					problems.add(fmt.Sprintf("%s.query.where[%d]", path, j), SafeError(err))
					continue
				}
				checked.Filters[p.ID] = append(checked.Filters[p.ID], f)
			}
		case p.SQL != "":
			if err := checkSQLPanel(ctx, parser, p, earlier); err != nil {
				if isOperational(err) {
					return nil, nil, err
				}
				problems.add(path+".sql", SafeError(err))
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
	return err
}
