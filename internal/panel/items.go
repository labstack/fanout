package panel

import (
	"fmt"
	"strings"
)

func compileItems(p *Panel, measures []Measure, filters []Filter, scope Scope) (Compiled, error) {
	sig, _ := lookupSignal(p.Query.From)
	where, args, err := buildWhere(sig, filters, scope)
	if err != nil {
		return Compiled{}, err
	}
	item, err := sig.field(p.Query.By[0])
	if err != nil {
		return Compiled{}, err
	}
	itemSQL := "coalesce(" + item.stringSQL() + ",'')"
	selects, groups := []string{}, []string{}
	columns := []Column{}
	if p.Viz == "state_timeline" {
		expr := fmt.Sprintf("epoch_ms(time_bucket(INTERVAL '%d seconds',%s::TIMESTAMP_NS))::BIGINT", int64(scope.Interval.Seconds()), quoteIdent(sig.time))
		selects = append(selects, expr+" AS time")
		groups = append(groups, expr)
		columns = append(columns, Column{Name: "time", Type: "time", Role: "time"})
	}
	selects = append(selects, itemSQL+" AS "+quoteIdent(item.alias()))
	groups = append(groups, itemSQL)
	columns = append(columns, Column{Name: item.alias(), Type: "string", Role: "dimension"})
	if len(p.Query.By) == 2 {
		ref, err := sig.field(p.Query.By[1])
		if err != nil {
			return Compiled{}, err
		}
		expr := "coalesce(" + ref.stringSQL() + ",'')"
		selects = append(selects, expr+" AS "+quoteIdent(ref.alias()))
		groups = append(groups, expr)
		columns = append(columns, Column{Name: ref.alias(), Type: "string", Role: "dimension"})
	}
	seconds := scope.End.Sub(scope.Start).Seconds()
	if p.Viz == "state_timeline" {
		seconds = scope.Interval.Seconds()
	}
	partition := ""
	if p.Viz == "state_timeline" {
		partition = "PARTITION BY " + groups[0]
	}
	for i, m := range measures {
		unit := m.Unit
		if unit == "none" {
			unit = ""
		}
		if p.Viz == "scatter" && i == 0 && p.XUnit != "" {
			unit = p.XUnit
		}
		if p.Unit != "" && (p.Viz != "scatter" || i == 1) {
			unit = p.Unit
		}
		selects = append(selects, measureSQL(m, sig, seconds, partition)+" AS "+quoteIdent(m.Alias))
		columns = append(columns, Column{Name: m.Alias, Type: "number", Role: "measure", Unit: unit})
	}
	text := "WITH base AS (SELECT * FROM " + structuredSource(sig.name) + " WHERE " + where + ")"
	if p.Viz == "state_timeline" {
		text += fmt.Sprintf(", items AS (SELECT %s AS item FROM base GROUP BY 1 ORDER BY count(*) DESC,item LIMIT %d)", itemSQL, p.Top())
	}
	aggregates := "SELECT " + strings.Join(selects, ",") + " FROM base GROUP BY " + strings.Join(groups, ",")
	if p.Viz == "state_timeline" {
		// Compute bucket shares across all items before filtering to the top N.
		text += ", bucket_items AS (" + aggregates + ") SELECT * FROM bucket_items WHERE " + quoteIdent(item.alias()) + " IN (SELECT item FROM items)"
	} else {
		text += " " + aggregates
	}
	n := distributionRows(p, len(columns))
	if p.Viz == "scatter" {
		n = min(n, 1000)
	}
	if p.Viz == "state_timeline" {
		text += fmt.Sprintf(" ORDER BY time DESC,2 LIMIT %d", n+1)
	} else {
		name, direction := measures[0].Alias, "DESC"
		if p.Query.Sort != "" {
			name = strings.TrimPrefix(p.Query.Sort, "+")
			if strings.HasPrefix(p.Query.Sort, "+") {
				direction = "ASC"
			}
		}
		text += fmt.Sprintf(" ORDER BY %s %s,1 LIMIT %d", quoteIdent(name), direction, n+1)
	}
	return Compiled{SQL: text, Args: args, Columns: columns}, nil
}
func validateItems(p *Panel, path string, problems *Problems) {
	if p.Options != nil {
		for name, value := range map[string]string{"x_scale": p.Options.XScale, "y_scale": p.Options.YScale} {
			if value != "" && (p.Viz != "scatter" || (value != "linear" && value != "log")) {
				problems.add(path+".options."+name, "axis scales apply only to scatter and must be linear or log")
			}
		}
	}
	if p.XUnit != "" {
		if p.Viz != "scatter" {
			problems.addHint(path+".x_unit", "x_unit applies only to scatter", "remove x_unit or use scatter")
		}
		if _, ok := unitFamilies[p.XUnit]; !ok {
			problems.addHint(path+".x_unit", "unknown x unit", "use one of "+strings.Join(unitNames(), ", "))
		}
	}
	if p.Viz != "scatter" && p.Viz != "state_timeline" {
		return
	}
	if p.Query == nil {
		problems.add(path+".query", "item panels require structured queries")
		return
	}
	if p.Viz == "scatter" && len(p.Query.Measures) != 2 {
		problems.addHint(path+".query.measures", "scatter requires exactly two measures", "select x and y measures in that order")
	}
	if p.Viz == "state_timeline" {
		if p.Query.Sort != "" {
			problems.add(path+".query.sort", "state_timeline has fixed chronological ordering")
		}
		if len(p.Query.Measures) != 1 {
			problems.add(path+".query.measures", "state_timeline requires one measure")
		}
		if len(p.Thresholds) == 0 {
			problems.addHint(path+".thresholds", "state_timeline requires thresholds", "set a warn or bad boundary for the selected measure")
		}
	}
	if p.Options != nil && p.Options.Style != "" {
		problems.add(path+".options.style", "item panels cannot stack or set line styles")
	}
}
