package fanql

import (
	"fmt"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/telemetry"
)

type Notice struct {
	Path    string          `json:"path"`
	Message string          `json:"message"`
	Span    *panel.TextSpan `json:"span,omitempty"`
}
type ResolveOptions struct {
	Viz       string
	Variables []panel.Variable
	Schema    *panel.Schema
}
type Resolved struct {
	Query      *Query
	Compatible []panel.VizCompat
	Edits      []VizEdit
	Notices    []Notice
}
type VizEdit struct {
	Viz  string `json:"viz"`
	Text string `json:"text"`
}

// Installed by the lowering layer. Resolution itself never executes SQL.
var resolveLower func(*Resolved) (panel.Query, panel.Problems)
var resolvePrint func(*Query) string

// Catalog reads remain private and return snapshots. Tests can substitute a
// prospective registry to detect accepted text that would need a version bump.
var resolveColumns = panel.SignalColumns
var resolveMeasures = panel.MeasureSpecs
var resolveFilterFunctions = panel.FilterFunctionNames

type resolver struct {
	opts     ResolveOptions
	out      *Resolved
	problems panel.Problems
	countIf  bool
	path     string
}

// Resolve annotates fields and values in place, retaining entry boundaries.
func Resolve(q *Query, opts ResolveOptions) (*Resolved, panel.Problems) {
	r := &resolver{opts: opts, out: &Resolved{Query: q, Notices: []Notice{}, Compatible: []panel.VizCompat{}, Edits: []VizEdit{}}}
	if q == nil {
		return nil, panel.Problems{{Path: "text", Message: "a query is required"}}
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
			r.measure(m.Expr)
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
	if len(r.problems) == 0 && resolveLower != nil {
		pq, ps := resolveLower(r.out)
		r.problems = append(r.problems, ps...)
		vars := map[string]panel.Variable(nil)
		if opts.Variables != nil {
			vars = map[string]panel.Variable{}
			for _, v := range opts.Variables {
				vars[v.Name] = v
			}
		}
		if opts.Viz != "" && len(ps) == 0 {
			ps, _ = panel.QueryShape(&pq, opts.Viz, vars)
			for _, p := range ps {
				p.Span = textSpan(queryPathSpan(q, p.Path))
				r.problems = append(r.problems, p)
			}
		}
		r.out.Compatible = panel.CompatibleVizzes(&pq, vars)
		if pq.Bucket == "" && q.Stage != nil && q.Stage.Histogram == nil {
			pq.Bucket = "auto"
			if ps, _ := panel.QueryShape(&pq, "timeseries", vars); len(ps) == 0 && resolvePrint != nil {
				edit := *q
				stage := *q.Stage
				stage.Bucket = "auto"
				edit.Stage = &stage
				r.out.Edits = append(r.out.Edits, VizEdit{Viz: "timeseries", Text: resolvePrint(&edit)})
			}
		}
	}
	sort.SliceStable(r.problems, func(i, j int) bool {
		return r.problems[i].Span != nil && r.problems[j].Span != nil && r.problems[i].Span.Start < r.problems[j].Span.Start
	})
	if len(r.problems) > MaxProblems {
		r.problems = append(r.problems[:MaxProblems], panel.Problem{Path: "text", Message: messages["more"], Span: r.problems[MaxProblems].Span})
	}
	if len(r.problems) > 0 {
		return nil, r.problems
	}
	return r.out, nil
}

func textSpan(s Span) *panel.TextSpan { return &panel.TextSpan{Start: s.Start, End: s.End} }
func (r *resolver) issue(at Span, message string) {
	r.problems = append(r.problems, panel.Problem{Path: r.path, Message: message, Span: textSpan(at)})
}
func (r *resolver) notice(at Span, message string) {
	r.out.Notices = append(r.out.Notices, Notice{Path: r.path, Message: message, Span: textSpan(at)})
}

func (r *resolver) field(f *Field) {
	if f.Ref != nil {
		return
	}
	key := f.Key
	scope := f.Scope
	if f.Bracket && key == "" {
		r.issue(f.At, "attribute keys must not be empty")
	}
	if strings.HasPrefix(key, "$__") {
		r.issue(f.At, messages["variable"])
	}
	if scope == "" && !f.Bracket && strings.EqualFold(key, "duration") {
		key = "duration_ms"
	}
	columns, _ := resolveColumns(r.out.Query.Source)
	if scope == "" && !f.Bracket && !strings.Contains(key, ".") {
		for _, c := range columns {
			if strings.EqualFold(key, c.Name) {
				if c.Type == panel.TypeMap {
					r.issue(f.At, c.Name+" is a map; use "+c.Name+"['key']")
				}
				f.Ref = &FieldRef{Column: c.Name, Type: c.Type, Unit: c.Unit}
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
	f.Ref = &FieldRef{Column: scope, Key: key, Type: panel.TypeString, Unit: "none"}
	if f.Scope == "" && !f.Bracket && !strings.Contains(key, ".") {
		message := fmt.Sprintf("%q is not a %s column; reading attributes['%s']", key, r.out.Query.Source, key)
		for _, m := range resolveMeasures() {
			if strings.EqualFold(m.Name, key) {
				message = fmt.Sprintf("%q is a measure, not a %s column; reading attributes['%s']", key, r.out.Query.Source, key)
				break
			}
		}
		var names []string
		for _, c := range columns {
			names = append(names, c.Name)
		}
		if hint := panel.Suggest(strings.ToLower(key), names); hint != "" {
			message += "; " + hint
		}
		r.notice(f.At, message)
	}
}

func (r *resolver) expr(e Expr) {
	switch x := e.(type) {
	case *And:
		for _, v := range x.Terms {
			r.expr(v)
		}
	case *Or:
		for _, v := range x.Terms {
			r.expr(v)
		}
	case *Not:
		r.expr(x.X)
	case *Compare:
		r.field(&x.Field)
		r.value(&x.Field, &x.Value, x.Op, false)
		if x.Field.Ref.Column == "severity" && x.Field.Ref.Key == "" && isRange(x.Op) {
			x.Bands = r.bands(x.Field, x.Op, x.Value, nil)
		}
	case *In:
		r.field(&x.Field)
		if x.Variable != "" {
			r.variable("$"+x.Variable, x.Field.Key, x.At, true)
		}
		for i := range x.Values {
			r.value(&x.Field, &x.Values[i], "in", true)
		}
	case *Between:
		r.field(&x.Field)
		r.value(&x.Field, &x.Lo, "between", false)
		r.value(&x.Field, &x.Hi, "between", false)
		if x.Field.Ref.Column == "severity" && x.Field.Ref.Key == "" {
			x.Bands = r.bands(x.Field, "between", x.Lo, &x.Hi)
		}
	case *Like:
		r.field(&x.Field)
		x.Pattern.Stored = x.Pattern.Text
	case *Exists:
		r.field(&x.Field)
	case *IsNull:
		r.field(&x.Field)
	case *Call:
		r.call(x)
	case *FreeText:
		x.Value.Stored = x.Value.Text
		x.column = "body"
		if r.out.Query.Source == "spans" {
			x.column = "operation"
		}
		if r.out.Query.Source == "metrics" {
			r.issue(x.At, "free text needs a field on metrics; use name:http.server.duration")
		}
		cols, _ := resolveColumns(r.out.Query.Source)
		for _, c := range cols {
			if strings.EqualFold(c.Name, x.Value.Text) {
				r.notice(x.At, fmt.Sprintf("%q is a field; did you mean %s:<value>?", x.Value.Text, c.Name))
				break
			}
		}
	case *RawExpr:
		// SQL is retained verbatim; the panel gate checks its syntax at execution.
		for _, tok := range Lex(x.Text) {
			if tok.Kind == Word && strings.HasPrefix(tok.Text, "$") {
				r.variable(tok.Text, "", x.At, false)
			}
		}
	}
}

func isRange(op string) bool {
	return op == ">" || op == ">=" || op == "<" || op == "<=" || op == "between"
}
func (r *resolver) value(f *Field, v *Value, op string, multi bool) {
	v.Stored = v.Text
	if !r.finiteValue(*v) {
		return
	}
	ref := f.Ref
	if ref.Column == "http_status_code" && ref.Key == "" && isRange(op) {
		ref.CompareAs = "integer"
	}
	if v.Kind == VariableValue {
		r.variable(v.Text, f.Key, v.At, multi)
		return
	}
	if v.Kind == DurationValue {
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
	if ref.Type == panel.TypeTime && ref.Key == "" {
		if !isRange(op) {
			r.issue(f.At, fmt.Sprintf("%q is a time column; compare it with > or <", ref.Column))
		} else if v.Kind != StringValue || !timestamp(v.Text) {
			r.issue(v.At, "time needs a quoted timestamp in RFC 3339 or YYYY-MM-DD HH:MM:SS form (optional fraction and offset)")
		}
		return
	}
	if ref.Type == panel.TypeNumber && ref.Key == "" && v.Kind != NumberValue {
		r.issue(v.At, ref.Column+" needs a number")
		return
	}
	if v.Kind == BooleanValue {
		v.Stored = strings.ToLower(v.Text)
	}
	if v.Kind != WordValue || !(op == ":" || op == "=" || op == "!=" || op == "<>" || op == "in") || ref.Key != "" {
		return
	}
	var table map[string]string
	var choices string
	switch ref.Column {
	case "status":
		if r.out.Query.Source == "spans" {
			table = statusAliases
			choices = "status:error, status:ok or status:unset"
		}
	case "kind":
		if r.out.Query.Source == "spans" {
			table = kindAliases
			choices = "kind:server, kind:client, kind:internal, kind:producer, kind:consumer or kind:unspecified"
		}
	case "type":
		if r.out.Query.Source == "metrics" {
			table = typeAliases
			choices = "type:gauge, type:counter, type:updown, type:histogram or type:summary"
		}
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

func (r *resolver) finiteValue(v Value) bool {
	var n float64
	if v.Kind == NumberValue {
		n, _ = strconv.ParseFloat(v.Text, 64)
	} else if v.Kind == DurationValue {
		n, _ = durationMS(v.Text)
	}
	if math.IsInf(n, 0) || math.IsNaN(n) {
		r.issue(v.At, "number must be finite")
		return false
	}
	return true
}

func (r *resolver) bands(f Field, op string, lo Value, hi *Value) []string {
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

func (r *resolver) variable(name, field string, at Span, multi bool) {
	if r.countIf {
		r.issue(at, "variables are not allowed inside count_if")
		return
	}
	if strings.HasPrefix(name, "$__") {
		r.issue(at, messages["variable"])
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

func (r *resolver) call(c *Call) {
	if !slices.Contains(resolveFilterFunctions(), c.Name) {
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
		} else if a.Value == nil || a.Value.Kind != StringValue {
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
		if c.Cmp.Value.Kind == VariableValue {
			r.variable(c.Cmp.Value.Text, "", c.Cmp.Value.At, false)
		}
	}
}

func (r *resolver) measure(e MExpr) {
	switch x := e.(type) {
	case *MCall:
		var spec *panel.MeasureSpec
		for _, s := range resolveMeasures() {
			if s.Name == x.Name {
				s := s
				spec = &s
				break
			}
		}
		if spec == nil {
			r.issue(x.At, "unknown function "+x.Name)
			return
		}
		if len(spec.Signals) > 0 && !slices.Contains(spec.Signals, r.out.Query.Source) {
			r.issue(x.At, x.Name+" is not available for "+r.out.Query.Source)
			return
		}
		args := len(x.Args)
		if x.Name == "count" && args == 1 && x.Args[0].Star {
			args = 0
		}
		if args != spec.Args {
			r.issue(x.At, fmt.Sprintf("%s takes %d argument(s), got %d; e.g. %s(duration)", x.Name, spec.Args, args, x.Name))
			return
		}
		for i := range x.Args {
			a := &x.Args[i]
			if a.Field != nil {
				r.field(a.Field)
				if spec.Numeric && a.Field.Ref.Key == "" && a.Field.Ref.Type != panel.TypeNumber {
					r.issue(a.At, fmt.Sprintf("%s needs a numeric field; %s is %s", x.Name, a.Field.Key, a.Field.Ref.Type))
				}
			}
		}
		if x.Name == "quantile" && (len(x.Args) < 2 || x.Args[1].Number == nil || *x.Args[1].Number <= 0 || *x.Args[1].Number >= 1) {
			r.issue(x.At, "quantile q must be a number between 0 and 1")
		}
	case *MCountIf:
		if r.out.Query.Source == "metrics" {
			r.issue(x.At, "measure arithmetic is not available for metrics")
			return
		}
		r.countIf = true
		r.expr(x.Filter)
		r.countIf = false
	case *MBinary:
		if r.out.Query.Source == "metrics" {
			r.issue(x.At, "measure arithmetic is not available for metrics")
			return
		}
		r.measure(x.L)
		r.measure(x.R)
	case *MNeg:
		if r.out.Query.Source == "metrics" {
			r.issue(x.At, "measure arithmetic is not available for metrics")
			return
		}
		r.measure(x.X)
	}
}

func measureAlias(m Measure, all []Measure) string {
	if m.Alias != "" {
		return m.Alias
	}
	if _, ok := m.Expr.(*MCountIf); ok {
		return "count_if"
	}
	c, ok := m.Expr.(*MCall)
	if !ok {
		return ""
	}
	n := 0
	for _, v := range all {
		if x, ok := v.Expr.(*MCall); ok && x.Name == c.Name && v.Alias == "" {
			n++
		}
	}
	if n > 1 && len(c.Args) > 0 && c.Args[0].Field != nil {
		f := c.Args[0].Field
		key := f.Key
		if f.Ref != nil {
			key = f.Ref.Column
			if f.Ref.Key != "" {
				key = f.Ref.Key
			}
		}
		var b strings.Builder
		for _, ch := range strings.ToLower(key) {
			if ch >= 'a' && ch <= 'z' || ch >= '0' && ch <= '9' {
				b.WriteRune(ch)
			} else {
				b.WriteByte('_')
			}
		}
		return c.Name + "_" + strings.Trim(b.String(), "_")
	}
	return c.Name
}

func sortIsMeasureAlias(q *Query) bool {
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
				r.issue(s.At, "log_patterns sort by count only")
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
	if (q.Sort != nil || q.Limit != nil) && slices.Contains([]string{"timeseries", "heatmap", "state_timeline"}, r.opts.Viz) || q.Limit != nil && slices.Contains([]string{"stat", "gauge"}, r.opts.Viz) {
		at := q.Span
		if q.Sort != nil {
			at = q.Sort.At
		} else if q.Limit != nil {
			at = q.Limit.At
		}
		r.notice(at, "sort does not apply to bucketed series; series are chosen by top (options)")
	}
}

func queryPathSpan(q *Query, path string) Span {
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
