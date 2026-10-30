package fanql

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/sqlscan"
	"github.com/labstack/fanout/internal/suggest"
	"github.com/labstack/fanout/internal/telemetry"
)

type Notice struct {
	Path    string          `json:"path"`
	Message string          `json:"message"`
	Span    *query.TextSpan `json:"span,omitempty"`
}

type resolved struct {
	Query      *queryTree
	Compatible []query.VizCompat
	Edits      []VizEdit
	Notices    []Notice
}
type VizEdit struct {
	Viz  string `json:"viz"`
	Text string `json:"text"`
}

// Vocabulary dependencies are snapshots supplied per compilation, so prospective
// catalog changes can be tested without mutating process-wide state.
type vocabulary struct {
	columns   func(string) ([]query.Field, bool)
	measures  func() []query.MeasureSpec
	functions func() []string
	buckets   func() []string
}

func currentVocabulary() vocabulary {
	return vocabulary{query.SignalColumns, query.MeasureSpecs, query.FilterFunctionNames, query.BucketNames}
}

type resolver struct {
	opts     Options
	vocab    vocabulary
	out      *resolved
	problems query.Problems
	countIf  bool
	path     string
}

func resolveWith(q *queryTree, opts Options, vocab vocabulary) (*resolved, query.Problems) {
	r := &resolver{opts: opts, vocab: vocab, out: &resolved{Query: q, Notices: []Notice{}, Compatible: []query.VizCompat{}, Edits: []VizEdit{}}}
	if q == nil {
		return nil, query.Problems{{Path: "text", Message: "a query is required"}}
	}
	for i, e := range q.Where {
		r.path = fmt.Sprintf("where[%d]", i)
		r.expr(e)
	}
	if s := q.Stage; s != nil {
		if s.Histogram != nil {
			r.path = "histogram.field"
			r.field(s.Histogram)
		}
		for i := range s.By {
			r.path = fmt.Sprintf("by[%d]", i)
			r.field(&s.By[i])
		}
		aliases := map[string]bool{}
		for i := range s.Measures {
			m := &s.Measures[i]
			r.path = fmt.Sprintf("measures[%d]", i)
			before := len(r.problems)
			expr := r.measure(m.Parsed.Expr, m.Info, m.Filters)
			if len(r.problems) == before {
				parsed, message, hint := query.MeasureFromExpr(q.Source, expr, m.Alias, r.vocab.measures())
				if message != "" {
					r.problems = append(r.problems, query.Problem{Path: r.path, Message: message, Hint: hint, Span: textSpan(m.At)})
				} else {
					m.Parsed = parsed
				}
			}
			if m.Alias != "" && !aliasName.MatchString(m.Alias) {
				r.issue(m.At, fmt.Sprintf("alias %q must be lowercase letters, digits and underscores", m.Alias))
			}
			name := measureAlias(*m, s.Measures)
			if aliases[name] {
				r.issue(m.At, "two measures are named "+name+"; add distinct aliases with as")
			}
			aliases[name] = true
		}
	}
	r.sort()
	if len(r.problems) == 0 {
		pq, ps := lower(r.out)
		r.problems = append(r.problems, ps...)
		vars := map[string]query.Variable(nil)
		if opts.Variables != nil {
			vars = map[string]query.Variable{}
			for _, v := range opts.Variables {
				vars[v.Name] = v
			}
		}
		if opts.Viz != "" && len(ps) == 0 {
			ps, _ = query.Shape(&pq, opts.Viz, vars, preparedMeasures(q))
			for _, p := range ps {
				p.Span = textSpan(queryPathSpan(q, p.Path))
				r.problems = append(r.problems, p)
			}
		}
		r.out.Compatible = query.CompatibleVizzes(&pq, vars, preparedMeasures(q))
		if pq.Bucket == "" && q.Stage != nil && q.Stage.Histogram == nil {
			pq.Bucket = "auto"
			if ps, _ := query.Shape(&pq, "timeseries", vars, preparedMeasures(q)); len(ps) == 0 {
				edit := *q
				stage := *q.Stage
				stage.Bucket = "auto"
				edit.Stage = &stage
				r.out.Edits = append(r.out.Edits, VizEdit{Viz: "timeseries", Text: print(&edit)})
			}
		}
	}
	sort.SliceStable(r.problems, func(i, j int) bool {
		return r.problems[i].Span != nil && r.problems[j].Span != nil && r.problems[i].Span.Start < r.problems[j].Span.Start
	})
	r.problems = presentProblems(r.problems)
	if len(r.problems) > maxProblems {
		r.problems = append(r.problems[:maxProblems], query.Problem{Path: "text", Message: message(diagMore), Span: r.problems[maxProblems].Span})
	}
	if len(r.problems) > 0 {
		return nil, r.problems
	}
	return r.out, nil
}

func textSpan(s span) *query.TextSpan { return &query.TextSpan{Start: s.Start, End: s.End} }
func (r *resolver) issue(at span, message string) {
	r.problems = append(r.problems, query.Problem{Path: r.path, Message: message, Span: textSpan(at)})
}
func (r *resolver) notice(at span, message string) {
	r.out.Notices = append(r.out.Notices, Notice{Path: r.path, Message: message, Span: textSpan(at)})
}

func (r *resolver) field(f *fieldName) {
	if f.Ref != nil {
		return
	}
	key := f.Key
	scope := f.Scope
	if f.Bracket && key == "" {
		r.issue(f.At, "attribute keys must not be empty")
	}
	if strings.HasPrefix(key, "$__") {
		r.issue(f.At, message(diagVariable))
	}
	if scope == "" && !f.Bracket && strings.EqualFold(key, "duration") {
		key = "duration_ms"
	}
	columns, _ := r.vocab.columns(r.out.Query.Source)
	if scope == "" && !f.Bracket && !strings.Contains(key, ".") {
		for _, c := range columns {
			if strings.EqualFold(key, c.Name) {
				if c.Type == query.TypeMap {
					r.issue(f.At, c.Name+" is a map; use "+c.Name+"['key']")
				}
				f.Ref = &fieldRef{Column: c.Name, Type: c.Type, Unit: c.Unit}
				return
			}
		}
	}
	if scope == "" {
		scope = "attributes"
	}
	if !f.Bracket && !identifierPath.MatchString(key) {
		r.issue(f.At, fmt.Sprintf("%q needs brackets", key))
	}
	f.Ref = &fieldRef{Column: scope, Key: key, Type: query.TypeString, Unit: "none"}
	if f.Scope == "" && !f.Bracket && !strings.Contains(key, ".") {
		message := fmt.Sprintf("%q is not a %s column; reading attributes['%s']", key, r.out.Query.Source, key)
		for _, m := range r.vocab.measures() {
			if strings.EqualFold(m.Name, key) {
				message = fmt.Sprintf("%q is a measure, not a %s column; reading attributes['%s']", key, r.out.Query.Source, key)
				break
			}
		}
		var names []string
		for _, c := range columns {
			names = append(names, c.Name)
		}
		if hint := suggest.Spelling(strings.ToLower(key), names); hint != "" {
			message += "; " + hint
		}
		r.notice(f.At, message)
	}
}

func (r *resolver) expr(e filterExpr) {
	switch x := e.(type) {
	case *andExpr:
		for _, v := range x.Terms {
			r.expr(v)
		}
	case *orExpr:
		for _, v := range x.Terms {
			r.expr(v)
		}
	case *notExpr:
		r.expr(x.X)
	case *comparison:
		r.field(&x.Field)
		r.value(&x.Field, &x.Value, x.Op, false)
		if x.Field.Ref.Column == "severity" && x.Field.Ref.Key == "" && isRange(x.Op) {
			x.Bands = r.bands(x.Field, x.Op, x.Value, nil)
		}
	case *inExpr:
		r.field(&x.Field)
		if x.Variable != "" {
			r.variable("$"+x.Variable, x.Field.Key, x.At, true)
		}
		for i := range x.Values {
			r.value(&x.Field, &x.Values[i], "in", true)
		}
	case *betweenExpr:
		r.field(&x.Field)
		r.value(&x.Field, &x.Lo, "between", false)
		r.value(&x.Field, &x.Hi, "between", false)
		if x.Field.Ref.Column == "severity" && x.Field.Ref.Key == "" {
			x.Bands = r.bands(x.Field, "between", x.Lo, &x.Hi)
		}
	case *likeExpr:
		r.field(&x.Field)
		x.Pattern.Stored = x.Pattern.Text
	case *existsExpr:
		r.field(&x.Field)
	case *nullExpr:
		r.field(&x.Field)
	case *filterCall:
		r.call(x)
	case *textExpr:
		x.Value.Stored = x.Value.Text
		x.column = "body"
		if r.out.Query.Source == "spans" {
			x.column = "operation"
		}
		if r.out.Query.Source == "metrics" {
			name := "http.server.duration"
			if r.opts.Schema != nil {
				var selected *query.MetricSchema
				for _, metric := range r.opts.Schema.Signals["metrics"].Metrics {
					if metric.Name != "" && (selected == nil || metric.Count > selected.Count || metric.Count == selected.Count && metric.Name < selected.Name) {
						copy := metric
						selected = &copy
					}
				}
				if selected != nil {
					name = selected.Name
				}
			}
			example := printValue(literalValue{Kind: stringValue, Text: name}, fieldName{Key: "name", Single: true}, false)
			r.issue(x.At, "free text needs a field on metrics; use name:"+example)
		}
		cols, _ := r.vocab.columns(r.out.Query.Source)
		for _, c := range cols {
			if strings.EqualFold(c.Name, x.Value.Text) {
				r.notice(x.At, fmt.Sprintf("%q is a field; did you mean %s:<value>?", x.Value.Text, c.Name))
				break
			}
		}
	case *rawEntry:
		for _, name := range sqlscan.Parameters(x.Text) {
			r.variable("$"+name, "", x.At, false)
		}
	}
}

func isRange(op string) bool {
	return op == ">" || op == ">=" || op == "<" || op == "<=" || op == "between"
}
func (r *resolver) value(f *fieldName, v *literalValue, op string, multi bool) {
	v.Stored = v.Text
	if !r.finiteValue(*v) {
		return
	}
	ref := f.Ref
	if ref.Column == "http_status_code" && ref.Key == "" && isRange(op) {
		ref.CompareAs = "integer"
	}
	if v.Kind == variableValue {
		r.variable(v.Text, f.Key, v.At, multi)
		return
	}
	if v.Kind == durationValue {
		if !strings.EqualFold(f.Key, "duration") || f.Scope != "" {
			text := v.Text
			for _, u := range []string{"us", "ms", "s", "m", "h"} {
				if strings.HasSuffix(strings.ToLower(text), u) {
					text = text[:len(text)-len(u)]
					break
				}
			}
			r.issue(v.At, "units apply to duration only; write "+f.Key+op+text)
			return
		}
		n, ok := durationMS(v.Text)
		if !ok {
			r.issue(v.At, "duration needs a unit; e.g. duration>500ms")
		} else if n < 0 {
			r.issue(v.At, "durations are not negative")
		}
		return
	}
	if strings.EqualFold(f.Key, "duration") && f.Scope == "" {
		r.issue(v.At, "duration needs a unit; e.g. duration>500ms")
		return
	}
	if ref.Type == query.TypeTime && ref.Key == "" {
		if !isRange(op) {
			r.issue(f.At, fmt.Sprintf("%q is a time column; compare it with > or <", ref.Column))
		} else if v.Kind != stringValue || !timestamp(v.Text) {
			r.issue(v.At, "time needs a quoted timestamp in RFC 3339 or YYYY-MM-DD HH:MM:SS form (optional fraction and offset)")
		}
		return
	}
	if ref.Type == query.TypeNumber && ref.Key == "" && v.Kind != numberValue {
		r.issue(v.At, ref.Column+" needs a number")
		return
	}
	if v.Kind == booleanValue {
		v.Stored = strings.ToLower(v.Text)
	}
	if v.Kind != wordValue || !(op == ":" || op == "=" || op == "!=" || op == "<>" || op == "in") || ref.Key != "" {
		return
	}
	vocabulary := query.FieldVocabularies()[ref.Column]
	table := vocabulary.Aliases
	if vocabulary.Signal != r.out.Query.Source {
		table = nil
	}
	choices := aliasChoices(ref.Column, table)
	switch ref.Column {
	case "severity":
		v.Stored = telemetry.SeverityFromText(v.Text)
		if _, ok := telemetry.SeverityRank(v.Stored); !ok && v.Stored != "UNSPECIFIED" && r.opts.Schema != nil {
			for _, c := range r.opts.Schema.Signals["logs"].Columns {
				if c.Name == "severity" {
					var vals []string
					for _, v := range c.Values {
						vals = append(vals, v.Value)
					}
					if len(vals) > 0 {
						r.notice(v.At, "not a standard level; stored levels in this window: "+strings.Join(vals, ", "))
					}
				}
			}
		}
		return
	}
	if table != nil {
		for a, s := range table {
			if strings.EqualFold(v.Text, a) || strings.EqualFold(v.Text, s) {
				v.Stored = s
				return
			}
		}
		r.issue(v.At, fmt.Sprintf("unknown %s %q; use %s", ref.Column, v.Text, choices))
	}
}

func (r *resolver) finiteValue(v literalValue) bool {
	var n float64
	if v.Kind == numberValue {
		n, _ = strconv.ParseFloat(v.Text, 64)
	} else if v.Kind == durationValue {
		n, _ = durationMS(v.Text)
	}
	if math.IsInf(n, 0) || math.IsNaN(n) {
		r.issue(v.At, "number must be finite")
		return false
	}
	return true
}

func (r *resolver) bands(f fieldName, op string, lo literalValue, hi *literalValue) []string {
	level := telemetry.SeverityFromText(lo.Text)
	rank, ok := telemetry.SeverityRank(level)
	if !ok {
		r.issue(lo.At, fmt.Sprintf("%q has no severity rank; use trace, debug, info, warn, error or fatal", lo.Text))
		return nil
	}
	start, end := 0, len(telemetry.SeverityBands)-1
	switch op {
	case ">":
		start = rank + 1
	case ">=":
		start = rank
	case "<":
		end = rank - 1
	case "<=":
		end = rank
	case "between":
		start = rank
		var good bool
		end, good = telemetry.SeverityRank(telemetry.SeverityFromText(hi.Text))
		if !good {
			r.issue(hi.At, fmt.Sprintf("%q has no severity rank; use trace, debug, info, warn, error or fatal", hi.Text))
			return nil
		}
		if start > end {
			r.issue(lo.At, "between needs the lower level first: severity between "+strings.ToLower(hi.Text)+" and "+strings.ToLower(lo.Text))
			return nil
		}
	}
	if start > end {
		r.issue(lo.At, fmt.Sprintf("severity%s%s matches no level; use severity:%s", op, lo.Text, strings.ToLower(level)))
		return nil
	}
	return slices.Clone(telemetry.SeverityBands[start : end+1])
}

func (r *resolver) variable(name, field string, at span, multi bool) {
	if strings.HasPrefix(name, "$__") {
		r.issue(at, message(diagVariable))
		return
	}
	if r.countIf {
		r.issue(at, "variables are not allowed inside count_if")
		return
	}
	if r.opts.Variables == nil {
		r.notice(at, "declared variables are needed to check "+name)
		return
	}
	var names []string
	for _, v := range r.opts.Variables {
		names = append(names, v.Name)
		if "$"+v.Name == name {
			if v.Multi && !multi {
				r.issue(at, name+" holds several values; use "+field+" in "+name)
			}
			return
		}
	}
	r.issue(at, "undeclared variable "+name+"; declared variables: "+strings.Join(names, ", "))
}

func (r *resolver) call(c *filterCall) {
	if !slices.Contains(r.vocab.functions(), c.Name) {
		r.issue(c.At, "function "+c.Name+" is not allowed in a filter")
		return
	}
	predicate := c.Name == "starts_with" || c.Name == "contains" || c.Name == "regexp_matches"
	want := 1
	if predicate {
		want = 2
	}
	if len(c.Args) != want {
		r.issue(c.At, fmt.Sprintf("%s takes %d argument(s), got %d", c.Name, want, len(c.Args)))
	}
	for i := range c.Args {
		a := &c.Args[i]
		if i == 0 {
			if a.Field != nil {
				r.field(a.Field)
			} else if a.Call != nil {
				r.call(a.Call)
			} else {
				r.issue(c.At, c.Name+" needs a field")
			}
		} else if a.Value == nil || a.Value.Kind != stringValue {
			r.issue(c.At, c.Name+" needs a quoted string")
		} else {
			a.Value.Stored = a.Value.Text
		}
	}
	if predicate && c.Cmp != nil {
		r.issue(c.At, "predicates are not compared; use "+c.Name+"(...) alone")
	}
	if !predicate && c.Cmp == nil {
		r.issue(c.At, c.Name+"(...) needs a comparison")
	}
	if c.Cmp != nil {
		c.Cmp.Value.Stored = c.Cmp.Value.Text
		r.finiteValue(c.Cmp.Value)
		if c.Cmp.Value.Kind == variableValue {
			r.variable(c.Cmp.Value.Text, "", c.Cmp.Value.At, false)
		}
	}
}

func (r *resolver) measure(e query.MeasureExpr, infos map[query.MeasureExpr]*measureSource, filters map[string]filterExpr) query.MeasureExpr {
	info := infos[e]
	if info == nil {
		return e
	}
	at := info.at
	args := info.args
	value := e
	if conditional, ok := e.(*query.MeasureCountIf); ok {
		value = *conditional
	}
	switch x := value.(type) {
	case query.MeasureCall:
		var spec *query.MeasureSpec
		for _, s := range r.vocab.measures() {
			if s.Name == x.Func {
				s := s
				spec = &s
				break
			}
		}
		if spec == nil {
			r.issue(at, "unknown function "+x.Func)
			return e
		}
		if len(spec.Signals) > 0 && !slices.Contains(spec.Signals, r.out.Query.Source) {
			r.issue(at, x.Func+" is not available for "+r.out.Query.Source)
			return e
		}
		argc := len(args)
		if x.Func == "count" && argc == 1 && args[0].Star {
			argc = 0
		}
		if argc != spec.Args {
			r.issue(at, fmt.Sprintf("%s takes %d argument(s), got %d; e.g. %s(duration)", x.Func, spec.Args, argc, x.Func))
			return e
		}
		for i := range args {
			a := &args[i]
			if a.Field != nil {
				r.field(a.Field)
				if spec.Numeric && a.Field.Ref.Key == "" && a.Field.Ref.Type != query.TypeNumber {
					r.issue(a.At, fmt.Sprintf("%s needs a numeric field; %s is %s", x.Func, a.Field.Key, a.Field.Ref.Type))
				}
			}
		}
		x.Field = nil
		if len(args) > 0 && args[0].Field != nil {
			f := args[0].Field
			x.Field = &query.FieldRef{Text: lowerField(*f), Column: f.Ref.Column, Key: f.Ref.Key, Type: f.Ref.Type, Unit: f.Ref.Unit}
		}
		x.Q = spec.Quantile
		if x.Func == "quantile" && len(args) > 1 && args[1].Number != nil {
			x.Q = *args[1].Number
		}
		if x.Func == "quantile" && (len(args) < 2 || args[1].Number == nil || *args[1].Number <= 0 || *args[1].Number >= 1) {
			r.issue(at, "quantile q must be a number between 0 and 1")
		}
		return x
	case query.MeasureCountIf:
		if r.out.Query.Source == "metrics" {
			r.issue(at, "measure arithmetic is not available for metrics")
			return e
		}
		r.countIf = true
		r.expr(info.filter)
		r.countIf = false
		x.Filter = lowerCountFilter(info.filter)
		filters[x.Filter] = info.filter
		return x
	case query.MeasureBinary:
		if r.out.Query.Source == "metrics" {
			r.issue(at, "measure arithmetic is not available for metrics")
			return e
		}
		x.L = r.measure(x.L, infos, filters)
		x.R = r.measure(x.R, infos, filters)
		return x
	case query.MeasureNeg:
		if r.out.Query.Source == "metrics" {
			r.issue(at, "measure arithmetic is not available for metrics")
			return e
		}
		x.X = r.measure(x.X, infos, filters)
		return x
	}
	return e
}

func measureAlias(m measureNode, all []measureNode) string {
	if m.Alias != "" {
		return m.Alias
	}
	name := m.Parsed.Func
	if c, ok := m.Parsed.Expr.(query.MeasureCall); ok {
		name = c.Func
	}
	if _, ok := m.Parsed.Expr.(*query.MeasureCountIf); ok {
		name = "count_if"
	}
	n := 0
	for _, v := range all {
		if c, ok := v.Parsed.Expr.(query.MeasureCall); ok && c.Func == name && v.Alias == "" {
			n++
		}
	}
	if n > 1 && m.Parsed.Field != nil {
		key := m.Parsed.Field.Column
		if m.Parsed.Field.Key != "" {
			key = m.Parsed.Field.Key
		}
		var b strings.Builder
		for _, ch := range strings.ToLower(key) {
			if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' {
				b.WriteRune(ch)
			} else {
				b.WriteByte('_')
			}
		}
		return name + "_" + strings.Trim(b.String(), "_")
	}
	return name
}
func preparedMeasures(q *queryTree) []query.Measure {
	out := []query.Measure{}
	if q.Stage != nil && q.Stage.Histogram == nil {
		for _, m := range q.Stage.Measures {
			out = append(out, m.Parsed)
		}
	}
	return out
}
func sortIsMeasureAlias(q *queryTree) bool {
	if q.Sort != nil && q.Stage != nil {
		for _, m := range q.Stage.Measures {
			if measureAlias(m, q.Stage.Measures) == q.Sort.Key {
				return true
			}
		}
	}
	return false
}

func (r *resolver) sort() {
	q := r.out.Query
	r.path = "sort"
	if q.Sort != nil {
		s := q.Sort
		switch r.opts.Viz {
		case "traces":
			if !(s.Key == "duration" && !s.Ascending || s.Key == "duration_ms" && !s.Ascending || s.Key == "errors" && !s.Ascending || s.Key == "start" && s.Ascending) {
				r.issue(s.At, "traces sort descending by duration, by errors, or ascending by start only")
			}
		case "logs":
			if s.Key != "time" {
				r.issue(s.At, "logs sort by time or +time only")
			}
		case "log_patterns":
			if s.Key != "count" || s.Ascending {
				r.issue(s.At, "pattern lists sort by count only")
			}
		default:
			if q.Stage != nil && q.Stage.Histogram == nil {
				var aliases []string
				for _, m := range q.Stage.Measures {
					aliases = append(aliases, measureAlias(m, q.Stage.Measures))
				}
				if !slices.Contains(aliases, s.Key) {
					r.issue(s.At, fmt.Sprintf("sort %q must name a measure alias; aliases here: %s", s.Key, strings.Join(aliases, ", ")))
				}
			}
		}
		if s.Key == "errors" && q.Source == "spans" {
			r.notice(s.At, "sort errors also filters to error spans; new queries: write status:error … sort duration")
		}
	}
	stored := query.Spec{}
	if q.Stage != nil {
		stored.Bucket = q.Stage.Bucket
	}
	if q.Sort != nil {
		stored.Sort = q.Sort.Key
	}
	if q.Limit != nil {
		stored.Limit = q.Limit.N
	}
	for _, notice := range ignoredClauseNotices(stored, r.opts.Viz) {
		r.path = notice.Path
		r.notice(queryPathSpan(q, notice.Path), notice.Message)
	}

}

func queryPathSpan(q *queryTree, path string) span {
	var i int
	if _, err := fmt.Sscanf(path, "where[%d]", &i); err == nil && i >= 0 && i < len(q.Where) {
		return q.Where[i].Span()
	}
	if q.Stage != nil {
		if _, err := fmt.Sscanf(path, "measures[%d]", &i); err == nil && i >= 0 && i < len(q.Stage.Measures) {
			return q.Stage.Measures[i].At
		}
		if _, err := fmt.Sscanf(path, "by[%d]", &i); err == nil && i >= 0 && i < len(q.Stage.By) {
			return q.Stage.By[i].At
		}
		if path == "bucket" {
			return q.Stage.At
		}
	}
	if path == "sort" && q.Sort != nil {
		return q.Sort.At
	}
	if path == "limit" && q.Limit != nil {
		return q.Limit.At
	}
	if path == "from" {
		return q.SourceSpan
	}
	return q.Span
}

func aliasChoices(field string, aliases map[string]string) string {
	keys := sortedCompletionKeys(aliases)
	// Preserve the documented order for diagnostic suggestions.
	if order := query.FieldVocabularies()[field].Order; len(order) == len(keys) {
		keys = order
	}
	var choices []string
	for _, key := range keys {
		choices = append(choices, field+":"+key)
	}
	if len(choices) < 2 {
		return strings.Join(choices, "")
	}
	return strings.Join(choices[:len(choices)-1], ", ") + " or " + choices[len(choices)-1]
}

func ignoredClauseNotices(q query.Spec, viz string) []Notice {
	notices := []Notice{}
	bucketed := q.Bucket != "" && slices.Contains([]string{"timeseries", "heatmap", "state_timeline"}, viz)
	if bucketed && q.Sort != "" {
		notices = append(notices, Notice{Path: "sort", Message: "sort does not apply to bucketed series; series are chosen by top (options)"})
	}
	if bucketed && q.Limit != 0 {
		notices = append(notices, Notice{Path: "limit", Message: "limit does not apply to bucketed series; series are chosen by top (options)"})
	}
	if q.Limit != 0 && (viz == "stat" || viz == "gauge") {
		notices = append(notices, Notice{Path: "limit", Message: "limit does not apply to " + viz + " panels; the whole window produces one value"})
	}
	return notices
}

func presentProblems(problems query.Problems) query.Problems {
	for i := range problems {
		problems[i].Message = diagnosticVizNames(problems[i].Message)
		problems[i].Hint = diagnosticVizNames(problems[i].Hint)
	}
	return problems
}

var diagnosticVizReplacer = strings.NewReplacer("log_patterns", "pattern lists", "state_timeline", "state timelines", "service_map", "service maps")

func diagnosticVizNames(text string) string {
	return diagnosticVizReplacer.Replace(text)
}
