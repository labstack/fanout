package panel

import (
	"fmt"
	"regexp"
	"slices"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

type vizSpec struct {
	width   int
	height  string
	query   bool
	minBy   int
	maxBy   int
	bucket  bool
	reduces bool
}

var vizSpecs = map[string]vizSpec{
	"service_map":    {width: 12, height: "l", query: true},
	"health":         {width: 12, height: "m", query: true},
	"logs":           {width: 12, height: "l", query: true},
	"log_patterns":   {width: 12, height: "m", query: true, minBy: 1, maxBy: 1, bucket: true},
	"traces":         {width: 12, height: "m", query: true},
	"stat":           {width: 3, height: "s", query: true, reduces: true},
	"gauge":          {width: 3, height: "s", query: true, reduces: true},
	"timeseries":     {width: 6, height: "m", query: true, maxBy: 1, bucket: true},
	"bar":            {width: 6, height: "m", query: true, minBy: 1, maxBy: 2},
	"table":          {width: 12, height: "m", query: true, maxBy: 3},
	"text":           {width: 4, height: "s"},
	"heatmap":        {width: 6, height: "m", query: true, bucket: true},
	"histogram":      {width: 6, height: "m", query: true, maxBy: 1},
	"scatter":        {width: 6, height: "m", query: true, minBy: 1, maxBy: 2},
	"state_timeline": {width: 6, height: "m", query: true, minBy: 1, maxBy: 1, bucket: true},
}

var vizOrder = []string{"stat", "gauge", "timeseries", "bar", "table", "text", "heatmap", "histogram", "scatter", "state_timeline", "logs", "log_patterns", "traces", "service_map", "health"}

var (
	idPattern     = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
	varRefPattern = regexp.MustCompile(`\$([A-Za-z_][A-Za-z0-9_]*)`)
	ranges        = map[string]time.Duration{
		"5m": 5 * time.Minute, "15m": 15 * time.Minute, "1h": time.Hour, "3h": 3 * time.Hour,
		"6h": 6 * time.Hour, "12h": 12 * time.Hour, "24h": 24 * time.Hour, "2d": 48 * time.Hour,
		"7d": 7 * 24 * time.Hour, "30d": 30 * 24 * time.Hour,
	}
	rangeOrder = []string{"5m", "15m", "1h", "3h", "6h", "12h", "24h", "2d", "7d", "30d"}
	refreshes  = []string{"off", "10s", "30s", "1m", "5m"}
	buckets    = map[string]time.Duration{
		"10s": 10 * time.Second, "30s": 30 * time.Second, "1m": time.Minute, "5m": 5 * time.Minute,
		"10m": 10 * time.Minute, "15m": 15 * time.Minute, "30m": 30 * time.Minute, "1h": time.Hour,
		"3h": 3 * time.Hour, "6h": 6 * time.Hour, "12h": 12 * time.Hour, "1d": 24 * time.Hour,
	}
	reduces = []string{"window", "last", "mean", "min", "max", "sum"}
)

// maxSpan is the longest span ParseSpan accepts.
const maxSpan = 400 * 24 * time.Hour

// ParseSpan reads a span such as 90m, 6h or 2d.
func ParseSpan(s string) (time.Duration, bool) {
	s = strings.TrimSpace(s)
	if len(s) < 2 {
		return 0, false
	}
	n, err := strconv.Atoi(s[:len(s)-1])
	if err != nil || n <= 0 {
		return 0, false
	}
	var unit time.Duration
	switch s[len(s)-1] {
	case 's':
		unit = time.Second
	case 'm':
		unit = time.Minute
	case 'h':
		unit = time.Hour
	case 'd':
		unit = 24 * time.Hour
	default:
		return 0, false
	}
	// Bound the span before multiplying so the product cannot overflow.
	if n > int(maxSpan/unit) {
		return 0, false
	}
	return time.Duration(n) * unit, true
}

// Normalize trims text and fills defaults in place. It never rejects.
func Normalize(d *Dashboard) {
	if d.Version == 0 {
		d.Version = SpecVersion
	}
	d.Name = strings.TrimSpace(d.Name)
	d.Description = strings.TrimSpace(d.Description)
	if d.Time.Range == "" && d.Time.From == nil && d.Time.To == nil {
		d.Time.Range = "1h"
	}
	if d.Time.Refresh == "" {
		d.Time.Refresh = "30s"
	}
	for i := range d.Panels {
		p := &d.Panels[i]
		p.ID = strings.TrimSpace(p.ID)
		p.Title = strings.TrimSpace(p.Title)
		spec, ok := vizSpecs[p.Viz]
		if !ok {
			continue
		}
		if p.Width == 0 {
			p.Width = spec.width
		}
		if p.Height == "" {
			p.Height = spec.height
		}
		if p.Query != nil && spec.bucket && p.Query.Bucket == "" {
			p.Query.Bucket = "auto"
		}
		if spec.reduces && p.Reduce == "" {
			p.Reduce = "window"
		}
	}
}

// Validate checks everything that does not need the database. Filter
// expressions and SQL text are checked by Check.
func Validate(d *Dashboard) Problems {
	var problems Problems
	if d.Version != SpecVersion {
		problems.add("version", fmt.Sprintf("version must be %d", SpecVersion))
	}
	if n := utf8.RuneCountInString(d.Name); n == 0 || n > 80 {
		problems.add("name", "name must be 1 to 80 characters")
	}
	if utf8.RuneCountInString(d.Description) > 280 {
		problems.add("description", "description is limited to 280 characters")
	}
	validateTime(d.Time, &problems)
	vars := validateVariables(d.Variables, &problems)
	if len(d.Panels) == 0 || len(d.Panels) > MaxPanels {
		problems.add("panels", fmt.Sprintf("a dashboard has 1 to %d panels", MaxPanels))
	}
	ids := map[string]bool{}
	for i := range d.Panels {
		path := fmt.Sprintf("panels[%d]", i)
		p := &d.Panels[i]
		if ids[p.ID] {
			problems.add(path+".id", fmt.Sprintf("duplicate panel id %q", p.ID))
		}
		ids[p.ID] = true
		validatePanel(p, path, vars, &problems)
	}
	return problems
}

func validateTime(t Time, problems *Problems) {
	switch {
	case t.Range != "" && (t.From != nil || t.To != nil):
		problems.add("time", "use either range or from and to")
	case t.Range != "":
		if _, ok := ranges[t.Range]; !ok {
			problems.addHint("time.range", fmt.Sprintf("unsupported range %q", t.Range), "use one of "+strings.Join(rangeOrder, ", "))
		}
	case t.From == nil || t.To == nil:
		problems.add("time", "an absolute range needs both from and to")
	case !t.From.Before(*t.To):
		problems.add("time.from", "from must be before to")
	}
	if !slices.Contains(refreshes, t.Refresh) {
		problems.addHint("time.refresh", fmt.Sprintf("unsupported refresh %q", t.Refresh), "use one of "+strings.Join(refreshes, ", "))
	}
	if t.Compare != "" && t.Compare != "previous_period" {
		problems.add("time.compare", "compare must be previous_period or omitted")
	}
}

func validateVariables(variables []Variable, problems *Problems) map[string]Variable {
	out := map[string]Variable{}
	if len(variables) > 12 {
		problems.add("variables", "a dashboard has at most 12 variables")
	}
	for i, v := range variables {
		path := fmt.Sprintf("variables[%d]", i)
		if !idPattern.MatchString(v.Name) || strings.HasPrefix(v.Name, "__") {
			problems.add(path+".name", "variable names are lowercase letters, digits and underscores, starting with a letter")
		}
		if _, dup := out[v.Name]; dup {
			problems.add(path+".name", fmt.Sprintf("duplicate variable %q", v.Name))
		}
		switch v.Kind {
		case "query":
			sig, ok := lookupSignal(v.From)
			if !ok {
				problems.addHint(path+".from", fmt.Sprintf("unknown signal %q", v.From), "use spans, logs or metrics")
			} else if _, err := sig.field(v.Field); err != nil {
				problems.add(path+".field", err.Error())
			}
		case "custom":
			if len(v.Options) == 0 || len(v.Options) > 200 {
				problems.add(path+".options", "custom variables list 1 to 200 options")
			}
		case "constant":
			if v.Value == "" {
				problems.add(path+".value", "constant variables need a value")
			}
		case "text":
		default:
			problems.addHint(path+".kind", fmt.Sprintf("unknown kind %q", v.Kind), "use query, custom, constant or text")
		}
		if v.Default == AllValue && !v.IncludeAll {
			problems.add(path+".default", "default $__all needs include_all")
		}
		if v.Multi && v.Kind != "query" && v.Kind != "custom" {
			problems.add(path+".multi", "only query and custom variables can be multi-value")
		}
		if len(v.Where) > 16 {
			problems.add(path+".where", "at most 16 filters per variable")
		}
		for j, w := range v.Where {
			if n := utf8.RuneCountInString(w); n == 0 || n > 500 {
				problems.add(fmt.Sprintf("%s.where[%d]", path, j), "filters are 1 to 500 characters")
			}
		}
		for _, ref := range varRefs(strings.Join(v.Where, " ")) {
			if _, earlier := out[ref]; !earlier {
				problems.add(path+".where", fmt.Sprintf("$%s must be a variable declared earlier", ref))
			}
		}
		out[v.Name] = v
	}
	return out
}

func varRefs(text string) []string {
	var out []string
	for _, m := range varRefPattern.FindAllStringSubmatch(text, -1) {
		if !strings.HasPrefix(m[1], "__") && !slices.Contains(out, m[1]) {
			out = append(out, m[1])
		}
	}
	return out
}

func validatePanel(p *Panel, path string, vars map[string]Variable, problems *Problems) {
	if !idPattern.MatchString(p.ID) {
		problems.add(path+".id", "panel ids are lowercase letters, digits and underscores, starting with a letter")
	}
	if n := utf8.RuneCountInString(p.Title); n == 0 || n > 80 {
		problems.add(path+".title", "title must be 1 to 80 characters")
	}
	for _, ref := range varRefs(p.Title) {
		if _, ok := vars[ref]; !ok {
			problems.add(path+".title", fmt.Sprintf("$%s is not a dashboard variable", ref))
		}
	}
	if utf8.RuneCountInString(p.Description) > 280 {
		problems.add(path+".description", "description is limited to 280 characters")
	}
	spec, ok := vizSpecs[p.Viz]
	if !ok {
		problems.addHint(path+".viz", fmt.Sprintf("unsupported viz %q", p.Viz), "use one of "+strings.Join(vizOrder, ", "))
		return
	}
	if p.Width < 1 || p.Width > 12 {
		problems.add(path+".width", "width must be 1 to 12 columns")
	}
	if !slices.Contains([]string{"s", "m", "l"}, p.Height) {
		problems.add(path+".height", "height must be s, m or l")
	}
	if utf8.RuneCountInString(p.SQL) > 8000 {
		problems.add(path+".sql", "SQL is limited to 8000 characters")
	}
	validateDisplayOptions(p, path, vars, problems)
	if p.Viz == "text" {
		validateItems(p, path, problems)
		validateRows(p, path, problems)
		if p.Query != nil || p.SQL != "" {
			problems.add(path, "text panels have content, not a query")
		}
		if n := utf8.RuneCountInString(p.Content); n == 0 || n > 4000 {
			problems.add(path+".content", "content must be 1 to 4000 characters of Markdown")
		}
		return
	}
	isRow := p.Viz == "logs" || p.Viz == "traces" || p.Viz == "log_patterns"
	validateRows(p, path, problems)
	if (p.Query == nil) == (strings.TrimSpace(p.SQL) == "") {
		if !isRow {
			problems.add(path, "set exactly one of query or sql")
		}
		return
	}
	if p.Unit != "" && !isRow {
		if _, ok := unitFamilies[p.Unit]; !ok {
			problems.addHint(path+".unit", fmt.Sprintf("unknown unit %q", p.Unit), "use one of "+strings.Join(unitNames(), ", "))
		}
	}
	if spec.reduces {
		if !slices.Contains(reduces, p.Reduce) {
			problems.add(path+".reduce", "reduce must be window, last, mean, min, max or sum")
		}
	} else if p.Reduce != "" {
		problems.add(path+".reduce", "reduce applies only to stat and gauge panels")
	}
	if p.Viz == "gauge" && (p.Min == nil || p.Max == nil || *p.Min >= *p.Max) {
		problems.add(path, "gauge panels need min and max, with min below max")
	}
	if len(p.Thresholds) > 4 && !isRow {
		problems.add(path+".thresholds", "at most 4 thresholds")
	}
	for i, t := range p.Thresholds {
		if isRow {
			break
		}
		if !slices.Contains([]string{"ok", "warn", "bad"}, t.Status) {
			problems.add(fmt.Sprintf("%s.thresholds[%d].status", path, i), "status must be ok, warn or bad")
		}
	}
	if p.Better != "" && p.Better != "lower" && p.Better != "higher" {
		problems.add(path+".better", "better must be lower or higher")
	}
	if p.Drill != "" && p.Drill != "traces" && p.Drill != "logs" {
		problems.add(path+".drill", "drill must be traces or logs")
	}
	if p.Drill != "" && (p.Query == nil || p.Query.From == "metrics" || p.Drill == "logs" && p.Query.From != "logs") {
		problems.add(path+".drill", "drill requires structured span/log lineage; logs drill requires a logs source")
	}

	if p.Click != nil {
		v, ok := vars[p.Click.SetVariable]
		if !ok || (v.Kind != "query" && v.Kind != "custom") {
			problems.add(path+".click.set_variable", fmt.Sprintf("%q must name a query or custom variable", p.Click.SetVariable))
		}
	}
	if p.Time != nil {
		if p.Time.Range != "" {
			if _, ok := ranges[p.Time.Range]; !ok {
				problems.addHint(path+".time.range", fmt.Sprintf("unsupported range %q", p.Time.Range), "use one of "+strings.Join(rangeOrder, ", "))
			}
		}
		if p.Time.Shift != "" {
			if _, ok := ParseSpan(p.Time.Shift); !ok {
				problems.add(path+".time.shift", "shift is a span such as 1h or 1d")
			}
		}
	}
	if p.Options != nil {
		maximum := 20
		if p.categoricalSeries() {
			maximum = 6
		}
		if !isRow && p.Options.Top > maximum {
			problems.addHint(path+".options.top", fmt.Sprintf("top must be at most %d", maximum), fmt.Sprintf("set options.top to %d or less", maximum))
		}
		if !isRow && p.Options.Style != "" && !slices.Contains([]string{"line", "area", "bars", "stacked"}, p.Options.Style) {
			problems.add(path+".options.style", "style must be line, area, bars or stacked")
		}
		if !isRow && p.Options.Scale != "" && p.Options.Scale != "linear" && p.Options.Scale != "log" {
			problems.add(path+".options.scale", "scale must be linear or log")
		}
		if p.Options.Legend != "" && p.Options.Legend != "auto" && p.Options.Legend != "hidden" {
			problems.add(path+".options.legend", "legend must be auto or hidden")
		}
	}
	if (p.Viz == "heatmap" || p.Viz == "histogram") && p.Query == nil {
		problems.add(path+".query", "distribution panels require a structured query")
	}
	validateItems(p, path, problems)
	validateRollupPanel(p, path, problems)
	if p.Query != nil {
		validateQuery(p, spec, path+".query", problems)
	} else {
		for _, ref := range varRefs(p.SQL) {
			v, ok := vars[ref]
			if !ok {
				problems.add(path+".sql", fmt.Sprintf("$%s is not a dashboard variable", ref))
			} else if v.Multi {
				problems.add(path+".sql", fmt.Sprintf("SQL panels cannot use the multi-value variable $%s", ref))
			}
		}
	}
	for _, ref := range varRefs(strings.Join(queryWhere(p), " ")) {
		if _, ok := vars[ref]; !ok {
			problems.addHint(path+".query.where", fmt.Sprintf("$%s is not a dashboard variable", ref), suggest(ref, mapKeys(vars)))
		}
	}
}

func queryWhere(p *Panel) []string {
	if p.Query == nil {
		return nil
	}
	return p.Query.Where
}

func mapKeys(vars map[string]Variable) []string {
	out := make([]string, 0, len(vars))
	for name := range vars {
		out = append(out, name)
	}
	return out
}

func validateQuery(p *Panel, spec vizSpec, path string, problems *Problems) {
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
		return
	}
	if len(q.Where) > 16 {
		problems.add(path+".where", "at most 16 filters per query")
	}
	sig, ok := lookupSignal(q.From)
	if !ok {
		problems.addHint(path+".from", fmt.Sprintf("unknown signal %q", q.From), "use spans, logs or metrics")
		return
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
	measures := parseMeasures(sig, q.Measures, path+".measures", problems)
	if len(q.By) < spec.minBy {
		problems.add(path+".by", fmt.Sprintf("%s panels group by at least %d field", p.Viz, spec.minBy))
	}
	if len(q.By) > spec.maxBy {
		problems.add(path+".by", fmt.Sprintf("%s panels group by at most %d field(s)", p.Viz, spec.maxBy))
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
			problems.add(path+".bucket", "bucket must be auto or one of 10s, 30s, 1m, 5m, 10m, 15m, 30m, 1h, 3h, 6h, 12h, 1d")
		}
	}
	if p.Viz == "heatmap" || p.Viz == "histogram" {
		if q.Histogram == nil {
			problems.addHint(path+".histogram", "distribution panels require a histogram", "spans: duration_ms/log2; metrics: value/explicit")
		} else if (q.From != "spans" || q.Histogram.Field != "duration_ms" || q.Histogram.Buckets != "log2") &&
			(q.From != "metrics" || q.Histogram.Field != "value" || q.Histogram.Buckets != "explicit") {
			problems.add(path+".histogram", "use spans duration_ms/log2 or metrics value/explicit")
		}
		if q.Histogram != nil && q.Histogram.Temporality != "" && (q.From != "metrics" || (q.Histogram.Temporality != "cumulative" && q.Histogram.Temporality != "delta")) {
			problems.addHint(path+".histogram.temporality", "temporality applies to metric histograms and must be cumulative or delta", "omit for the OTel cumulative default")
		}
		if len(q.Measures) != 1 || q.Measures[0] != "count()" {
			problems.addHint(path+".measures", "distribution counts require count()", "use count() as the observation weight")
		}
		if p.SQL != "" {
			problems.add(path+".sql", "distribution panels use a structured histogram")
		}
	} else if q.Histogram != nil {
		problems.add(path+".histogram", "histogram applies only to heatmap and histogram")
	}
	if q.Limit < 0 || q.Limit > 1000 {
		problems.add(path+".limit", "limit must be 0 to 1000")
	}
	if q.Sort != "" && p.Viz != "logs" && p.Viz != "traces" {
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
	if len(families) > 1 && p.Viz != "table" && p.Viz != "scatter" {
		problems.add(path+".measures", "these measures have different units and would share one axis; split them into separate panels")
	}
	if p.Options != nil && p.Options.Style == "stacked" {
		for _, m := range measures {
			if !m.Additive {
				problems.add(strings.TrimSuffix(path, ".query")+".options.style", fmt.Sprintf("stacking needs additive measures such as count, rate or sum; %s is not", m.Alias))
				break
			}
		}
	}
	if spec.reduces && len(q.Measures) != 1 {
		problems.add(path+".measures", fmt.Sprintf("%s panels show one measure", p.Viz))
	}
}

// Infer only recognizable semantics; ambiguous measures remain neutral.
func inferBetter(p *Panel) string {
	if p.Query == nil || len(p.Query.Measures) != 1 {
		return ""
	}
	sig, ok := lookupSignal(p.Query.From)
	if !ok {
		return ""
	}
	m, message, _ := parseMeasure(sig, p.Query.Measures[0])
	if message != "" {
		return ""
	}
	if unitFamilies[p.Unit] == "duration" || m.Func == "error_rate" || (m.Field != nil && unitFamilies[m.Field.Unit] == "duration") {
		return "lower"
	}
	errorFiltered := false
	for _, filter := range p.Query.Where {
		if errorCountFilter.MatchString(filter) {
			errorFiltered = true
		}
	}
	switch m.Func {
	case "count":
		if errorFiltered {
			return "lower"
		}
		if sig.name == "spans" || sig.name == "logs" {
			return "higher"
		}
	case "rate":
		if !errorFiltered {
			return "higher"
		}
	}
	return ""
}

var errorCountFilter = regexp.MustCompile(`(?i)^\s*\(*\s*(?:status\s*=\s*'(?:STATUS_CODE_ERROR|ERROR)'|status\s+IN\s*\(\s*'(?:STATUS_CODE_ERROR|ERROR)'(?:\s*,\s*'(?:STATUS_CODE_ERROR|ERROR)')*\s*\)|(?:severity|upper\s*\(\s*severity\s*\))\s*=\s*'(?:ERROR|FATAL|CRITICAL)'|(?:severity|upper\s*\(\s*severity\s*\))\s+IN\s*\(\s*'(?:ERROR|FATAL|CRITICAL)'(?:\s*,\s*'(?:ERROR|FATAL|CRITICAL)')*\s*\)|severity_number\s*>=\s*17|http_status_code\s*>=\s*500)\s*\)*\s*$`)
