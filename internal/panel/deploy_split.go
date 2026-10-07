package panel

import (
	"context"
	"fmt"

	"github.com/labstack/fanout/internal/annotations"
)

func (e *Executor) runDeploySplit(ctx context.Context, p *Panel, checked *Checked, scope Scope) (*Frame, string, error) {
	copyPanel := *p
	copyOptions := *p.Options
	copyOptions.Split = ""
	copyPanel.Options = &copyOptions
	request := annotations.Request{From: scope.Start, To: scope.End}
	seen := map[string]string{}
	for _, f := range checked.Filters[p.ID] {
		if scope.dropped(f) {
			if f.EqField == "service" {
				frame, text, err := e.runScope(ctx, &copyPanel, checked, scope)
				if frame != nil {
					frame.Note = "Service is All; showing the unsplit whole-window frame."
				}
				return frame, text, err
			}
			continue
		}
		if f.EqField == "service" || f.EqField == "namespace" {
			field, value, err := rollupFilterValue(ctx, e.engine, f, scope)
			if err != nil {
				return nil, "", err
			}
			if old, ok := seen[field]; ok && old != value {
				return nil, "", fmt.Errorf("deploy split scope equalities disagree")
			}
			seen[field] = value
			if field == "namespace" {
				request.Namespace = value
			} else {
				request.Services = []string{value}
			}
		}
	}
	if len(request.Services) != 1 {
		return nil, "", fmt.Errorf("deploy split requires one active service equality")
	}
	history, err := annotations.New(e.engine).Read(ctx, request)
	if err != nil {
		return nil, "", err
	}
	var latest *annotations.Deploy
	for i := range history.Deploys {
		at := history.Deploys[i].At
		if at.After(scope.Start) && at.Before(scope.End) && (latest == nil || at.After(latest.At)) {
			latest = &history.Deploys[i]
		}
	}
	if latest == nil {
		f, text, err := e.runScope(ctx, &copyPanel, checked, scope)
		if f != nil {
			f.Note = "No deploy in this panel's time range; showing the whole window."
		}
		return f, text, err
	}
	before, after := scope, scope
	before.End = latest.At
	after.Start = latest.At
	a, sqlA, err := e.runScope(ctx, &copyPanel, checked, before)
	if err != nil {
		return nil, sqlA, err
	}
	b, sqlB, err := e.runScope(ctx, &copyPanel, checked, after)
	if err != nil {
		return nil, sqlB, err
	}
	columns := append([]Column{a.Columns[0], {Name: "period", Type: "string", Role: "dimension"}}, a.Columns[1:]...)
	f := newFrame(columns)
	f.Periods = map[string]Time{"Before deploy": {From: &before.Start, To: &before.End}, "Since deploy": {From: &after.Start, To: &after.End}}
	f.Truncated = a.Truncated || b.Truncated || history.Truncated
	for _, part := range []struct {
		frame *Frame
		label string
	}{{a, "Before deploy"}, {b, "Since deploy"}} {
		for r := 0; r < part.frame.Rows; r++ {
			row := []any{part.frame.Values[0][r], part.label}
			for i := 1; i < len(part.frame.Columns); i++ {
				row = append(row, part.frame.Values[i][r])
			}
			appendPanelRow(f, row...)
		}
	}
	f.Note = "Split at " + latest.At.Format("2006-01-02T15:04:05.999999999Z07:00") + " · " + latest.Service + " " + latest.Version
	return f, sqlA + "; " + sqlB, nil
}
func validateDisplayOptions(p *Panel, path string, vars map[string]Variable, problems *Problems) {
	if p.Options == nil {
		return
	}
	if p.Options.Split != "" && (p.Options.Split != "deploy" || p.Viz != "bar" || p.Query == nil || len(p.Query.By) != 1 || len(p.Query.Measures) != 1) {
		problems.addHint(path+".options.split", "deploy split needs a structured bar with one category and one measure", "use a separate panel per measure")
	}
	if len(p.Options.Columns) > 30 {
		problems.add(path+".options.columns", "at most 30 column formats")
	}
	if len(p.Options.Columns) > 0 && p.Viz != "table" {
		problems.add(path+".options.columns", "column formats apply to table panels")
	}
	seen := map[string]bool{}
	for _, c := range p.Options.Columns {
		if c.Field == "" || seen[c.Field] {
			problems.add(path+".options.columns", "column fields must be nonempty and unique")
		}
		seen[c.Field] = true
		switch c.Format {
		case "unit", "bar", "status", "sparkline", "trace_link", "service_link", "log_template":
		default:
			problems.add(path+".options.columns", "unknown column format")
		}
		if c.Unit != "" {
			if _, ok := unitFamilies[c.Unit]; !ok {
				problems.add(path+".options.columns", "unknown column unit")
			}
		}
		if c.Format == "service_link" {
			v, ok := vars[c.Variable]
			if !ok || (v.Kind != "query" && v.Kind != "custom") {
				problems.addHint(path+".options.columns", "service_link requires a query or custom variable", "set variable to the name of a query or custom variable")
			}
		}
		if p.Query != nil {
			sig, ok := lookupSignal(p.Query.From)
			if !ok {
				continue
			}
			columns := map[string]bool{}
			for _, by := range p.Query.By {
				ref, err := sig.field(by)
				if err == nil {
					columns[ref.alias()] = true
				}
			}
			var ignored Problems
			measureAliases := map[string]bool{}
			for _, m := range parseMeasures(sig, p.Query.Measures, "", &ignored) {
				columns[m.Alias] = true
				measureAliases[m.Alias] = true
			}
			if c.Format == "sparkline" {
				if !measureAliases[c.Field] {
					problems.addHint(path+".options.columns", "structured sparklines require a measure column", "choose a measure field; the executor attaches its trend")
				}
			}
			if !columns[c.Field] {
				problems.add(path+".options.columns", "formatted field does not exist in the structured frame")
			}
		}
	}
}

func checkDeployScope(ctx context.Context, parser Parser, p *Panel, f Filter) error {
	if p.Options == nil || p.Options.Split != "deploy" {
		return nil
	}
	tree, err := parser.ParseSQL(ctx, "SELECT 1 FROM spans WHERE ("+f.Source+")")
	if err != nil {
		return err
	}
	scoped := false
	var walk func(any)
	walk = func(n any) {
		switch v := n.(type) {
		case map[string]any:
			if v["class"] == "COLUMN_REF" && (fieldText(v) == "service" || fieldText(v) == "namespace") {
				scoped = true
			}
			for _, child := range v {
				walk(child)
			}
		case []any:
			for _, child := range v {
				walk(child)
			}
		}
	}
	walk(tree["where_clause"])
	if !scoped {
		return nil
	}
	if f.EqField != "service" && f.EqField != "namespace" {
		return Problems{{Path: "options.split", Message: "deploy split requires standalone service/namespace equalities"}}
	}
	sample := Scope{Vars: map[string]Value{}}
	for _, name := range f.Params {
		sample.Vars[name] = Value{Values: []string{"check"}}
	}
	_, _, err = rollupFilterValue(ctx, parser, f, sample)
	return err
}
