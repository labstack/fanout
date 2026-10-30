package panel

import (
	"fmt"
	"github.com/labstack/fanout/internal/suggest"
	"slices"
	"strings"
)

// VizRequirement identifies panel configuration required beyond the query.
type VizRequirement string

const (
	RequiresMinMax     VizRequirement = "min_max"
	RequiresThresholds VizRequirement = "thresholds"
)

// VizCompat identifies a visualization accepting the query as written.
type VizCompat struct {
	Viz      string           `json:"viz"`
	Requires []VizRequirement `json:"requires"`
}

// QueryShape checks query-only structure without normalizing or using an engine.
// A nil vars map omits declaration checks. All problem paths are query-relative.
func QueryShape(q *Query, viz string, vars map[string]Variable, prepared ...[]Measure) (Problems, []VizRequirement) {
	requirements := []VizRequirement{}
	if viz == "gauge" {
		requirements = append(requirements, RequiresMinMax)
	}
	if viz == "state_timeline" {
		requirements = append(requirements, RequiresThresholds)
	}
	var problems Problems
	spec, ok := vizSpecs[viz]
	if !ok {
		problems.addHint("", fmt.Sprintf("unsupported viz %q", viz), "use one of "+strings.Join(vizOrder, ", "))
		return problems, requirements
	}
	if q == nil {
		problems.add("", "a structured query is required")
		return problems, requirements
	}
	if !spec.query {
		problems.add("", "text panels have content, not a query")
		return problems, requirements
	}
	p := &Panel{Viz: viz, Query: q}
	queryShapeRows(q, viz, "", &problems)
	queryShapeItems(q, viz, "", &problems)
	if measures, ok := queryShapeAggregate(p, spec, "", &problems, true, prepared...); ok {
		queryShapeAggregateTail(q, viz, "", measures, &problems)
		queryShapeReduce(q, spec, viz, "", &problems)
	}
	if vars != nil {
		queryShapeVariables(q, vars, "", &problems)
	}
	for i := range problems {
		problems[i].Path = strings.TrimPrefix(problems[i].Path, ".")
	}
	return problems, requirements
}

// CompatibleVizzes lists the query visualizations accepting the query unchanged.
func CompatibleVizzes(q *Query, vars map[string]Variable, prepared ...[]Measure) []VizCompat {
	out := []VizCompat{}
	for _, viz := range vizOrder {
		// Text and fixed rollup scopes are not FanQL query visualization targets.
		if viz == "text" || viz == "health" || viz == "service_map" {
			continue
		}
		problems, requires := QueryShape(q, viz, vars, prepared...)
		if len(problems) == 0 {
			out = append(out, VizCompat{Viz: viz, Requires: requires})
		}
	}
	return out
}

func queryShapeVariables(q *Query, vars map[string]Variable, path string, problems *Problems) {
	for _, ref := range varRefs(strings.Join(q.Where, " ")) {
		if _, ok := vars[ref]; !ok {
			problems.addHint(path+".where", fmt.Sprintf("$%s is not a dashboard variable", ref), suggest.Spelling(ref, mapKeys(vars)))
		}
	}
}

// Query shape and validation share the bounded measure parser.
func shapeMeasures(sig *signal, texts []string, path string, problems *Problems) []Measure {
	return parseMeasures(sig, texts, path, problems)
}

func queryShapeAggregate(p *Panel, spec vizSpec, path string, problems *Problems, shapeOnly bool, prepared ...[]Measure) ([]Measure, bool) {
	q := p.Query
	if p.Viz == "logs" || p.Viz == "traces" || p.Viz == "log_patterns" {
		// Fixed projections are checked field by field in validateRows; avoid
		// duplicate aggregate-query Problems for the same rejected field.
		if len(q.Where) > 16 {
			problems.addHint(path+".where", "at most 16 filters per query", "remove filters until at most 16 remain")
		}
		for i, w := range q.Where {
			if strings.TrimSpace(w) == "" || len(w) > 500 {
				problems.addHint(fmt.Sprintf("%s.where[%d]", path, i), "filters are 1 to 500 characters", "use a nonempty filter of at most 500 characters")
			}
		}
		return nil, false
	}
	if len(q.Where) > 16 {
		problems.add(path+".where", "at most 16 filters per query")
	}
	sig, ok := lookupSignal(q.From)
	if !ok {
		problems.addHint(path+".from", fmt.Sprintf("unknown signal %q", q.From), "use spans, logs or metrics")
		return nil, false
	}
	for i, w := range q.Where {
		if strings.TrimSpace(w) == "" || len(w) > 500 {
			problems.add(fmt.Sprintf("%s.where[%d]", path, i), "filters are 1 to 500 characters")
		}
	}
	if (len(q.Measures) == 0 && p.Viz != "logs" && p.Viz != "traces" && p.Viz != "health" && p.Viz != "service_map") || len(q.Measures) > 6 {
		problems.add(path+".measures", "a query has 1 to 6 measures; fixed logs and traces take none")
	}
	if len(q.Measures) > 1 && ((p.Viz == "timeseries" && len(q.By) > 0) || (p.Viz == "bar" && len(q.By) == 2)) {
		problems.addHint(path+".measures", "grouped panels show one measure", "use one panel per measure, or remove by")
	}
	var measures []Measure
	if len(prepared) > 0 {
		measures = finishMeasures(slices.Clone(prepared[0]), path+".measures", problems)
	} else if shapeOnly {
		measures = shapeMeasures(sig, q.Measures, path+".measures", problems)
	} else {
		measures = parseMeasures(sig, q.Measures, path+".measures", problems)
	}
	if len(q.By) < spec.minBy {
		problems.add(path+".by", fmt.Sprintf("%s panels group by at least %d field", p.Viz, spec.minBy))
	}
	if len(q.By) > spec.maxBy {
		if spec.maxBy == 0 {
			problems.add(path+".by", p.Viz+" panels take no group by")
		} else {
			problems.add(path+".by", fmt.Sprintf("%s panels group by at most %d field(s)", p.Viz, spec.maxBy))
		}
	}
	byAliases := map[string]bool{}
	for i, by := range q.By {
		ref, err := sig.field(by)
		if err != nil {
			problems.add(fmt.Sprintf("%s.by[%d]", path, i), err.Error())
			continue
		}
		if byAliases[ref.alias()] {
			problems.addHint(fmt.Sprintf("%s.by[%d]", path, i), fmt.Sprintf("two grouping fields produce the column %s", ref.alias()), "group by one of them")
		}
		byAliases[ref.alias()] = true
	}
	for _, m := range measures {
		if byAliases[m.Alias] {
			problems.addHint(path+".measures", fmt.Sprintf("measure %s has the same column name as a grouping field", m.Alias), "rename with as")
		}
	}
	switch {
	case spec.bucket && q.Bucket == "":
		problems.add(path+".bucket", "timeseries panels need a bucket; use auto")
	case !spec.bucket && q.Bucket != "":
		problems.add(path+".bucket", fmt.Sprintf("%s panels do not take a bucket", p.Viz))
	case q.Bucket != "" && q.Bucket != "auto":
		if _, ok := buckets[q.Bucket]; !ok {
			problems.add(path+".bucket", "bucket must be auto or one of "+strings.Join(BucketNames()[1:], ", "))
		}
	}
	if p.Viz == "heatmap" || p.Viz == "histogram" {
		if q.Histogram == nil {
			problems.addHint(path+".histogram", "distribution panels require a histogram", "spans: duration_ms/log2; metrics: value/explicit")
		} else if (q.From != "spans" || q.Histogram.Field != "duration_ms" || q.Histogram.Buckets != "log2") &&
			(q.From != "metrics" || q.Histogram.Field != "value" || q.Histogram.Buckets != "explicit") {
			problems.add(path+".histogram", "use spans duration_ms/log2 or metrics value/explicit")
		}
		if len(q.Measures) != 1 || q.Measures[0] != "count()" {
			problems.addHint(path+".measures", "distribution counts require count()", "use count() as the observation weight")
		}

	} else if q.Histogram != nil {
		problems.add(path+".histogram", "histogram applies only to heatmap and histogram")
	}
	return measures, true
}

func queryShapeAggregateTail(q *Query, viz, path string, measures []Measure, problems *Problems) {
	if q.Limit < 0 || q.Limit > 1000 {
		problems.add(path+".limit", "limit must be 0 to 1000")
	}
	if q.Sort != "" && viz != "logs" && viz != "traces" {
		alias := strings.TrimPrefix(q.Sort, "+")
		if !slices.ContainsFunc(measures, func(m Measure) bool { return m.Alias == alias }) {
			problems.add(path+".sort", fmt.Sprintf("sort %q must name a measure alias", q.Sort))
		}
	}
	families := map[string]bool{}
	for _, m := range measures {
		families[unitFamilies[m.Unit]] = true
	}
	delete(families, "none")
	if len(families) > 1 && viz != "table" && viz != "scatter" {
		problems.add(path+".measures", "these measures have different units and would share one axis; split them into separate panels")
	}

}

func queryShapeReduce(q *Query, spec vizSpec, viz, path string, problems *Problems) {
	if spec.reduces && len(q.Measures) != 1 {
		problems.add(path+".measures", fmt.Sprintf("%s panels show one measure", viz))
	}
}

func queryShapeRows(q *Query, viz, path string, problems *Problems) {
	if viz != "logs" && viz != "traces" && viz != "log_patterns" {
		return
	}
	if q.Limit < 0 || q.Limit > 1000 {
		problems.addHint(path+".limit", "limit must be 0 to 1000", "use a limit from 1 to 1000 or omit it for the default")
	}
	if q.Histogram != nil {
		problems.addHint(path+".histogram", "histogram applies only to heatmap and histogram", "remove histogram or use a heatmap or histogram panel")
	}
	if viz == "traces" {
		if q.From != "spans" {
			problems.addHint(path+".from", "traces reads spans", "use query {from: spans}")
		}
		if q.Sort != "" && q.Sort != "duration_ms" && q.Sort != "errors" && q.Sort != "+start" {
			problems.addHint(path+".sort", "traces sort is duration_ms, errors or +start", "use sort duration_ms, errors, +start or omit sort")
		}
	} else {
		if q.From != "logs" {
			problems.addHint(path+".from", "logs and log_patterns read logs", "use query {from: logs}")
		}
	}
	if viz == "logs" && q.Sort != "" && q.Sort != "time" && q.Sort != "+time" {
		problems.addHint(path+".sort", "logs sort is time or +time", "use sort time, +time or omit sort")
	}
	if viz == "log_patterns" {
		if len(q.By) != 1 || q.By[0] != "body_template" {
			problems.addHint(path+".by", "log_patterns groups by body_template", "use by [body_template]")
		}
		if len(q.Measures) != 1 || q.Measures[0] != "count()" {
			problems.addHint(path+".measures", "log_patterns requires count()", "use measures [count()]")
		}
		if q.Sort != "" && q.Sort != "count" {
			problems.addHint(path+".sort", "log_patterns sort is count", "use sort count or omit sort")
		}
		if q.Bucket != "" && q.Bucket != "auto" {
			if _, ok := buckets[q.Bucket]; !ok {
				problems.addHint(path+".bucket", "bucket must be auto or a supported interval", "use "+strings.Join(BucketNames()[:len(BucketNames())-1], ", ")+" or "+BucketNames()[len(BucketNames())-1])
			}
		}
	}
	if viz == "logs" || viz == "traces" {
		if len(q.Measures) > 0 {
			problems.addHint(path+".measures", "fixed row projections do not take aggregate measures", "remove measures; use log_patterns for counts or a table for aggregates")
		}
		if len(q.By) > 0 {
			problems.addHint(path+".by", "logs and traces panels do not group; use log_patterns or a table", "remove by or use log_patterns or a table")
		}
		if q.Bucket != "" {
			problems.addHint(path+".bucket", "logs and traces panels do not take a bucket", "remove bucket")
		}
	}
}

func queryShapeItems(q *Query, viz, path string, problems *Problems) {
	if q == nil {
		return
	}
	if viz == "scatter" && len(q.Measures) != 2 {
		problems.addHint(path+".measures", "scatter requires exactly two measures", "select x and y measures in that order")
	}
	if viz == "state_timeline" {
		if q != nil && q.Sort != "" {
			problems.add(path+".sort", "state_timeline has fixed chronological ordering")
		}
		if q != nil && len(q.Measures) != 1 {
			problems.add(path+".measures", "state_timeline requires one measure")
		}
	}
}
