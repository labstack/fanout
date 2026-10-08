package panel

import (
	"context"
	"fmt"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

type Option struct {
	Value string `json:"value"`
	Count int64  `json:"count,omitempty"`
}

type ResolveRequest struct {
	Dashboard Dashboard        `json:"dashboard" jsonschema:"Complete v1 dashboard specification; every panel is executed"`
	Time      *Time            `json:"time,omitempty" jsonschema:"Optional dashboard time override: exact from/to or a relative range"`
	Vars      map[string]Value `json:"vars,omitempty" jsonschema:"Resolved variable values: strings or lists; preserves $__all and empty lists"`
}

// ResolveVariables lists the options of every query, custom and constant
// variable, resolving them in order so a variable that filters on an earlier
// one sees that variable's current value.
func (e *Executor) ResolveVariables(ctx context.Context, req ResolveRequest) (map[string][]Option, error) {
	now := e.now()
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	d := req.Dashboard
	Normalize(&d)
	checked, err := e.check(ctx, &d)
	if err != nil {
		return nil, err
	}
	t := d.Time
	if req.Time != nil {
		t = *req.Time
	}
	start, end, err := resolveWindow(t, nil, now, e.maxWindow)
	if err != nil {
		return nil, Problems{{Path: "time", Message: err.Error()}}
	}
	options := map[string][]Option{}
	values := map[string]Value{}
	for _, v := range d.Variables {
		opts, err := e.optionsFor(ctx, v, checked, start, end, values)
		if err != nil {
			return nil, err
		}
		if v.Kind != "text" {
			options[v.Name] = opts
		}
		provided, ok := req.Vars[v.Name]
		values[v.Name] = chooseValue(v, provided, ok, opts)
	}
	return options, nil
}

// values resolves the current value of every variable for a run. Options are
// queried only for a query variable with no provided value, no default and no
// All, which is the one case that needs them.
func (e *Executor) values(ctx context.Context, d *Dashboard, checked *Checked, start, end time.Time, provided map[string]Value) (map[string]Value, error) {
	values := map[string]Value{}
	for _, v := range d.Variables {
		given, ok := provided[v.Name]
		if ok && !v.Multi && !given.All && len(given.Values) == 0 {
			ok = false
		}
		var opts []Option
		if v.Kind == "query" && (!ok || given.All) && v.Default == "" && !v.IncludeAll {
			var err error
			if opts, err = e.optionsFor(ctx, v, checked, start, end, values); err != nil {
				return nil, err
			}
		}
		if v.Kind == "custom" {
			opts, _ = e.optionsFor(ctx, v, checked, start, end, values)
		}
		values[v.Name] = chooseValue(v, given, ok, opts)
	}
	return values, nil
}

func (e *Executor) optionsFor(ctx context.Context, v Variable, checked *Checked, start, end time.Time, values map[string]Value) ([]Option, error) {
	switch v.Kind {
	case "constant":
		return []Option{{Value: v.Value}}, nil
	case "custom":
		out := make([]Option, len(v.Options))
		for i, o := range v.Options {
			out[i] = Option{Value: o}
		}
		return out, nil
	case "query":
		sig, _ := lookupSignal(v.From)
		ref, err := sig.field(v.Field)
		if err != nil {
			return nil, err
		}
		scope := Scope{Start: start, End: end, Vars: values}
		where, args, err := buildWhere(sig, checked.VarFilters[v.Name], scope)
		if err != nil {
			return nil, err
		}
		expr := ref.stringSQL()
		ctx = queryrows.WithWindow(ctx, queryrows.Window{Start: start, End: end})
		rows, err := e.engine.QueryContext(ctx, fmt.Sprintf("SELECT %s AS v, count(*) AS n FROM %s WHERE %s AND %s IS NOT NULL AND %s <> '' GROUP BY 1 ORDER BY 2 DESC, 1 LIMIT 500", expr, structuredSource(sig.name), where, expr, expr), args...)
		if err != nil {
			return nil, fmt.Errorf("list values for $%s: %w", v.Name, err)
		}
		defer rows.Close()
		out := []Option{}
		for rows.Next() {
			var o Option
			if err := rows.Scan(&o.Value, &o.Count); err != nil {
				return nil, err
			}
			out = append(out, o)
		}
		return out, rows.Err()
	}
	return nil, nil
}

func chooseValue(v Variable, provided Value, ok bool, opts []Option) Value {
	if v.Kind == "constant" {
		return Value{Values: []string{v.Value}}
	}
	if ok && !(provided.All && !v.IncludeAll) && (provided.All || v.Multi || len(provided.Values) > 0) {
		return provided
	}
	switch {
	case v.Default == AllValue:
		return Value{All: true}
	case v.Default != "":
		return Value{Values: []string{v.Default}}
	case v.IncludeAll:
		return Value{All: true}
	case len(opts) > 0:
		return Value{Values: []string{opts[0].Value}}
	}
	return Value{}
}
