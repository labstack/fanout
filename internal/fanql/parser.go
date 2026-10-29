package fanql

import (
	"regexp"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/labstack/fanout/internal/panel"
)

const (
	MaxTextRunes = 4000
	MaxTextBytes = 8192
	MaxTokens    = 1024
	MaxNodes     = 512
	MaxDepth     = 16
	MaxListItems = 100
	MaxProblems  = 20
)

var numberPattern = regexp.MustCompile(`^-?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
var durationPattern = regexp.MustCompile(`(?i)^-?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?(?:us|ms|s|m|h)$`)

type parser struct {
	tokens          Tokens
	runes           []rune
	i               int
	problems        panel.Problems
	bounded         bool
	nodes, depth    int
	heights         map[any]int
	completion      bool
	expected        []Expect
	completionQuery *Query
	completionUsed  map[string]bool
}

// Completion uses the same productions and alternatives as Parse. Unwinding at
// the first unfinished production preserves its context without error recovery
// inventing a different position for the cursor.
type completionEnd struct{}

func (p *parser) expectNext(alternatives ...Expect) {
	if p.completion && p.peek().Kind == EOF {
		p.expected = append(p.expected, alternatives...)
		panic(completionEnd{})
	}
}

func (p *parser) expectTerm(nested bool) {
	alternatives := []Expect{{Class: "field"}, {Class: "free_text"}, {Class: "operator", Scope: "term"}, {Class: "variable"}}
	if !nested {
		alternatives = append([]Expect{{Class: "clause"}}, alternatives...)
	}
	p.expectNext(alternatives...)
}

// A private sentinel unwinds only a bound failure, before further recursion or
// allocation. Other panics remain visible to callers and tests.
type parseBound struct{}

// Parse returns no query whenever a problem is present. source is empty for a
// whole query, or the page-supplied source for a search-bar filter.
func Parse(text string, source string) (q *Query, problems panel.Problems) {
	p := &parser{runes: []rune(text), bounded: true, heights: make(map[any]int)}
	defer func() {
		if v := recover(); v != nil {
			if _, ok := v.(parseBound); !ok {
				panic(v)
			}
			q = nil
		}
		problems = p.finish()
		if len(problems) > 0 {
			q = nil
		}
	}()
	if len(p.runes) > MaxTextRunes || len(text) > MaxTextBytes {
		p.bound(Span{0, len(p.runes)}, "textBound")
	}
	p.tokens = Lex(text)
	if len(p.tokens)-1 > MaxTokens {
		p.bound(p.tokens[MaxTokens].Span, "tokensBound")
	}
	q = p.query(source)
	return q, nil
}

// parseEntry is the unbounded internal entry parser used by the lift gate.
func parseEntry(text string) (Expr, panel.Problems) {
	p := &parser{runes: []rune(text), tokens: Lex(text), heights: make(map[any]int)}
	terms := p.filters(false)
	for p.peek().Kind != EOF {
		p.unexpected()
	}
	x := p.conjunction(terms)
	problems := p.finish()
	if len(problems) > 0 {
		return nil, problems
	}
	return x, nil
}

func (p *parser) peek() Token       { return p.tokens[p.i] }
func (p *parser) ahead(n int) Token { return p.tokens[min(p.i+n, len(p.tokens)-1)] }
func (p *parser) take() Token {
	t := p.peek()
	if t.Kind != EOF {
		p.i++
	}
	return t
}
func (p *parser) lower() string { return strings.ToLower(p.peek().Text) }
func (p *parser) is(s string) bool {
	t := p.peek()
	return (t.Kind == Word || t.Kind == Ident || t.Kind == Punct) && strings.EqualFold(t.Text, s)
}
func (p *parser) eat(s string) bool {
	if p.is(s) {
		p.take()
		return true
	}
	return false
}
func (p *parser) previousEnd() int {
	if p.i == 0 {
		return 0
	}
	return p.tokens[p.i-1].Span.End
}
func (p *parser) text(at Span) string { return string(p.runes[at.Start:at.End]) }
func (p *parser) issue(at Span, message, hint string) {
	if p.completion {
		return
	}
	p.problems = append(p.problems, panel.Problem{Path: "text", Message: message, Hint: hint, Span: &panel.TextSpan{Start: at.Start, End: at.End}})
}
func (p *parser) report(at Span, code string, args ...any) {
	p.issue(at, diagnostic(code, args...), hints[code])
}
func (p *parser) bound(at Span, code string) {
	p.problems = nil
	p.report(at, code)
	panic(parseBound{})
}
func (p *parser) enter(at Span) {
	if p.bounded && p.depth >= MaxDepth {
		p.bound(at, "depthBound")
	}
	p.depth++
}
func (p *parser) leave() { p.depth-- }
func (p *parser) node(x any, children ...any) {
	p.nodes++
	if p.bounded && p.nodes > MaxNodes {
		p.bound(x.(interface{ Span() Span }).Span(), "nodesBound")
	}
	height := 0
	for _, child := range children {
		if child != nil {
			height = max(height, p.heights[child]+1)
		}
	}
	p.heights[x] = height
	if p.bounded && height+p.depth > MaxDepth {
		p.bound(x.(interface{ Span() Span }).Span(), "depthBound")
	}
}
func (p *parser) finish() panel.Problems {
	sort.SliceStable(p.problems, func(i, j int) bool { return p.problems[i].Span.Start < p.problems[j].Span.Start })
	if len(p.problems) > MaxProblems {
		at := *p.problems[MaxProblems].Span
		p.problems = p.problems[:MaxProblems]
		p.issue(Span{at.Start, at.End}, diagnostic("more"), "")
	}
	return p.problems
}

func (p *parser) query(source string) *Query {
	q := &Query{Span: Span{0, len(p.runes)}}
	p.completionQuery = q
	if source != "" {
		q.Source = strings.ToLower(source)
		if !sources[q.Source] {
			p.report(Span{}, "source", source)
		}
		if p.is("where") {
			p.report(p.take().Span, "pageWhere")
		}
		q.Where = p.filters(false)
		for p.peek().Kind != EOF {
			p.report(p.peek().Span, "pageFilter")
			p.skipRun()
		}
		return q
	}
	p.expectNext(Expect{Class: "source"})
	first := p.take()
	q.Source = strings.ToLower(first.Text)
	q.SourceSpan = first.Span
	if first.Kind != Word || !sources[q.Source] {
		switch q.Source {
		case "from", "index", "source", "search":
			p.report(first.Span, "from", q.Source)
		default:
			hint := hints["source"]
			if q.Source == "traces" {
				hint = hints["traces"]
			}
			p.issue(first.Span, diagnostic("source", first.Text), hint)
		}
	}
	q.WhereWord = p.eat("where")
	whereAt := p.i - 1
	q.Where = p.filters(false)
	if q.WhereWord && len(q.Where) == 0 && len(p.problems) == 0 {
		word := p.lower()
		hint := ""
		if clauses[word] {
			hint = whereHint(word)
		}
		p.issue(p.tokens[whereAt].Span, diagnostic("whereEmpty"), hint)
	}
	for p.peek().Kind != EOF {
		switch {
		case p.is("|"):
			pipe := p.take()
			if p.is("=") || p.is("~") {
				op := p.take()
				p.report(Span{pipe.Span.Start, op.Span.End}, "pipeOp", "|"+op.Text)
				p.skipRun()
				continue
			}
			if p.is("sort") && strings.EqualFold(p.ahead(1).Text, "by") {
				p.report(p.take().Span, "foreignStage", "sort")
				p.skipStage()
				continue
			}
			if q.Stage != nil {
				if !p.is("sort") && !p.is("limit") {
					p.report(pipe.Span, "secondStage")
					p.skipStage()
					continue
				}
			} else if !p.is("sort") && !p.is("limit") {
				q.Stage = p.stage(pipe.Span.Start)
				continue
			}
		case p.is("where"):
			p.report(p.take().Span, "whereTwice")
			q.Where = append(q.Where, p.filters(false)...)
		case p.is("sort"):
			if q.Limit != nil {
				p.report(p.peek().Span, "limitBeforeSort")
			}
			if q.Sort != nil {
				p.report(p.peek().Span, "duplicate", "sort")
			}
			q.Sort = p.parseSort()
		case p.is("limit"):
			if q.Limit != nil {
				p.report(p.peek().Span, "duplicate", "limit")
			}
			q.Limit = p.parseLimit()
		case p.is("by"), p.is("every"):
			if q.Stage == nil {
				code := "byFirst"
				if p.is("every") {
					code = "everyFirst"
				}
				p.report(p.take().Span, code)
				p.skipRun()
			} else {
				p.report(p.take().Span, "order", p.tokens[p.i-1].Text)
				p.skipRun()
			}
		default:
			p.unexpected()
		}
	}
	return q
}

func (p *parser) filterStop(nested bool) bool {
	t := p.peek()
	return t.Kind == EOF || p.is(")") || p.is("|") || (!nested && t.Kind == Word && clauses[p.lower()])
}

// filters keeps conjunction entries separate. Only an explicit group or an OR
// chain creates a single entry around multiple terms.
func (p *parser) filters(nested bool) []Expr {
	var terms []Expr
	var ops []string
	mode := ""
	mixed := -1
	p.expectTerm(nested)
	for !p.filterStop(nested) {
		before := p.i
		if len(terms) > 0 {
			op := "and"
			if p.is("and") || p.is("or") {
				tok := p.take()
				op = strings.ToLower(tok.Text)
				if p.filterStop(nested) {
					p.expectTerm(nested)
					p.report(tok.Span, "sides", op)
					break
				}
			}
			if mode == "" {
				mode = op
			} else if mode != op && mixed < 0 {
				mixed = len(ops)
			}
			ops = append(ops, op)
		} else if p.is("and") || p.is("or") {
			p.report(p.take().Span, "sides", p.tokens[p.i-1].Text)
			continue
		}
		x := p.term(nested)
		if x != nil {
			terms = append(terms, x)
		} else if len(ops) >= len(terms) && len(ops) > 0 {
			ops = ops[:len(ops)-1]
		}
		if p.i == before {
			p.unexpected()
		}
	}
	if p.completion && !nested {
		p.completionQuery.Where = terms
	}
	p.expectTerm(nested)
	if !p.completion && mixed >= 0 && len(terms) >= 3 && len(ops) == len(terms)-1 {
		at := Span{terms[0].Span().Start, terms[len(terms)-1].Span().End}
		first := p.groupingEdit(terms, ops, ops[mixed])
		second := p.groupingEdit(terms, ops, ops[0])
		p.issue(at, diagnostic("mixed"), groupingHint(first, second))
		span := panel.TextSpan{Start: at.Start, End: at.End}
		p.problems[len(p.problems)-1].FixIts = []panel.FixIt{{Label: first, Span: span, Text: first}, {Label: second, Span: span, Text: second}}
	}
	if mode == "or" && len(terms) > 1 {
		x := &Or{Terms: terms, At: Span{terms[0].Span().Start, terms[len(terms)-1].Span().End}}
		p.node(x)
		return []Expr{x}
	}
	return terms
}

func (p *parser) conjunction(terms []Expr) Expr {
	if len(terms) == 0 {
		return nil
	}
	if len(terms) == 1 {
		return terms[0]
	}
	x := &And{Terms: terms, At: Span{terms[0].Span().Start, terms[len(terms)-1].Span().End}}
	p.node(x)
	return x
}

// Each alternative gives one operator kind priority explicitly, grouping every
// run of that kind. Both edits are valid even when a longer list mixes repeatedly.
func (p *parser) groupingEdit(terms []Expr, ops []string, grouped string) string {
	var out strings.Builder
	for i := 0; i < len(terms); {
		j := i
		for j < len(ops) && ops[j] == grouped {
			j++
		}
		if j > i {
			out.WriteByte('(')
		}
		out.WriteString(p.text(Span{terms[i].Span().Start, terms[j].Span().End}))
		if j > i {
			out.WriteByte(')')
		}
		if j+1 < len(terms) {
			out.WriteString(p.text(Span{terms[j].Span().End, terms[j+1].Span().Start}))
		}
		i = j + 1
	}
	return out.String()
}

func setExprSpan(x Expr, at Span) {
	switch x := x.(type) {
	case *And:
		x.At = at
	case *Or:
		x.At = at
	case *Not:
		x.At = at
	case *Compare:
		x.At = at
	case *In:
		x.At = at
	case *Between:
		x.At = at
	case *Like:
		x.At = at
	case *IsNull:
		x.At = at
	case *Exists:
		x.At = at
	case *Call:
		x.At = at
	case *FreeText:
		x.At = at
	case *RawExpr:
		x.At = at
	}
}

func setMeasureSpan(x MExpr, at Span) {
	switch x := x.(type) {
	case *MCall:
		x.At = at
	case *MCountIf:
		x.At = at
	case *MBinary:
		x.At = at
	case *MNeg:
		x.At = at
	case *MConst:
		x.At = at
	}
}

func isFieldWordOperator(op string) bool {
	switch op {
	case "in", "between", "like", "ilike":
		return true
	}
	return false
}

func (p *parser) term(nested bool) Expr {
	p.expectTerm(nested)
	if p.is("-") || p.is("not") {
		t := p.take()
		p.enter(t.Span)
		x := p.term(nested)
		p.leave()
		if x == nil {
			return nil
		}
		n := &Not{X: x, At: Span{t.Span.Start, x.Span().End}}
		p.node(n, x)
		return n
	}
	return p.primary(nested)
}

func (p *parser) primary(nested bool) Expr {
	t := p.peek()
	if p.eat("(") {
		p.enter(t.Span)
		terms := p.filters(true)
		p.leave()
		closed := p.eat(")")
		if !closed {
			p.report(t.Span, "unclosedGroup")
		}
		x := p.conjunction(terms)
		if x != nil && closed {
			setExprSpan(x, Span{t.Span.Start, p.previousEnd()})
		}
		return x
	}
	if t.Kind == Raw {
		p.take()
		p.checkQuoted(t)
		x := &RawExpr{Text: t.Text, At: t.Span}
		p.node(x)
		return x
	}
	if t.Kind == String {
		p.take()
		p.checkQuoted(t)
		v := Value{Kind: StringValue, Text: t.Text, At: t.Span}
		x := &FreeText{Value: v, At: t.Span}
		p.node(x)
		return x
	}
	if t.Kind == Bad {
		p.badTerm()
		return nil
	}
	if t.Kind != Word {
		if t.Kind != EOF {
			p.unexpected()
		} else {
			p.report(t.Span, "sides", "not")
		}
		return nil
	}
	word := strings.ToLower(t.Text)
	if p.foreignTerm() {
		return nil
	}
	next := strings.ToLower(p.ahead(1).Text)
	fieldOperator := isComparison(next) || next == ":" || next == "~" || next == "!~" || next == "is" || isFieldWordOperator(next) || next == "not" && isFieldWordOperator(strings.ToLower(p.ahead(2).Text))
	if reserved[word] && !fieldOperator {
		p.report(t.Span, "reserved", word)
		p.take()
		return nil
	}
	if p.ahead(1).Text == "(" {
		return p.call()
	}
	field, ok := p.field()
	if !ok {
		p.skipRun()
		return nil
	}
	op := p.lower()
	p.expectNext(Expect{Class: "operator", Field: &field})
	if isComparison(op) || op == ":" || op == "~" || op == "!~" {
		if numberPattern.MatchString(field.Key) || durationPattern.MatchString(field.Key) {
			p.report(field.At, "fieldBefore", op)
			p.skipRun()
			return nil
		}
		oper := p.take()
		if (op == "=" && (p.is("=") || p.is("~"))) || op == ":" && p.is(":") {
			code := "eqeq"
			if p.is("~") {
				code = "regex"
			}
			if op == ":" {
				code = "cast"
			}
			end := p.take().Span.End
			p.report(Span{oper.Span.Start, end}, code)
			p.skipRun()
			return nil
		}
		if op == ":" && isComparison(p.lower()) {
			op = p.take().Text
		}
		p.expectNext(Expect{Class: "value", Field: &field, Scope: op})
		v, valid := p.value()
		if !valid {
			example := "api"
			if field.Key == "duration" {
				example = "500ms"
			}
			p.report(Span{field.At.Start, oper.Span.End}, "value", field.Key, op, field.Key, op, example)
			p.skipRun()
			return nil
		}
		at := Span{field.At.Start, v.At.End}
		if p.is(":") {
			p.colonProblem(field, v, at)
			return nil
		}
		if p.peek().Text == "*" && p.peek().Span.Start == v.At.End {
			p.wildcard(v)
			return nil
		}
		if op == ":" && v.Kind == StarValue {
			x := &Exists{Field: field, At: at}
			p.node(x)
			return x
		}
		if v.Kind == StarValue {
			p.issue(at, diagnostic("wildcard"), wildcardHint(v.Text))
			return nil
		}
		x := &Compare{Field: field, Op: op, Value: v, At: at}
		p.node(x)
		return x
	}
	neg := false
	if expressionWords[op] && op == "not" && isFieldWordOperator(strings.ToLower(p.ahead(1).Text)) {
		neg = true
		p.take()
		op = p.lower()
	}
	switch op {
	case "in":
		return p.in(field, neg)
	case "between":
		p.take()
		p.expectNext(Expect{Class: "value", Field: &field, Scope: "between"})
		lo, ok := p.value()
		if !ok || !p.eat("and") {
			p.report(Span{field.At.Start, p.previousEnd()}, "between")
			return nil
		}
		p.expectNext(Expect{Class: "value", Field: &field, Scope: "between"})
		hi, ok := p.value()
		if !ok {
			p.report(Span{field.At.Start, p.previousEnd()}, "between")
			return nil
		}
		x := &Between{Field: field, Lo: lo, Hi: hi, Negated: neg, At: Span{field.At.Start, hi.At.End}}
		p.node(x)
		return x
	case "is":
		p.take()
		neg = p.eat("not")
		if !p.eat("null") {
			p.report(Span{field.At.Start, p.previousEnd()}, "null")
			return nil
		}
		x := &IsNull{Field: field, Negated: neg, At: Span{field.At.Start, p.previousEnd()}}
		p.node(x)
		return x
	case "like", "ilike":
		p.take()
		p.expectNext(Expect{Class: "value", Field: &field})
		v, ok := p.value()
		if !ok || v.Kind != StringValue {
			p.report(Span{field.At.Start, p.previousEnd()}, "like")
			return nil
		}
		x := &Like{Field: field, Pattern: v, Insensitive: op == "ilike", Negated: neg, At: Span{field.At.Start, v.At.End}}
		p.node(x)
		return x
	}
	if neg {
		p.report(p.tokens[p.i-1].Span, "unexpected", "not")
		return nil
	}
	if !nested && clauses[word] {
		p.report(t.Span, "unexpected", t.Text)
		return nil
	}
	if p.peek().Text == "*" && p.peek().Span.Start == field.At.End {
		p.wildcard(Value{Text: t.Text, At: t.Span})
		return nil
	}
	x := &FreeText{Value: Value{Kind: WordValue, Text: p.text(field.At), At: field.At}, At: field.At}
	p.node(x)
	return x
}

func isComparison(s string) bool {
	switch s {
	case "=", "!=", "<>", ">", ">=", "<", "<=":
		return true
	}
	return false
}

func (p *parser) field() (Field, bool) {
	t := p.peek()
	if t.Kind != Word && t.Kind != Ident && t.Kind != Path {
		p.report(t.Span, "field")
		return Field{}, false
	}
	p.take()
	f := Field{Key: t.Text, Single: !strings.Contains(t.Text, "."), At: t.Span}
	if p.is("[") && (strings.EqualFold(t.Text, "attributes") || strings.EqualFold(t.Text, "resource")) {
		p.take()
		p.expectNext(Expect{Class: "bracket_key", Scope: strings.ToLower(t.Text)})
		key := p.peek()
		if key.Kind != BracketKey && key.Kind != String {
			p.report(key.Span, "bracket")
			return f, false
		}
		p.take()
		p.checkQuoted(key)
		f.Scope = strings.ToLower(t.Text)
		f.Key = key.Text
		f.Bracket = true
		f.Single = false
		f.At.End = key.Span.End
		if !p.eat("]") {
			p.report(key.Span, "bracket")
			return f, false
		}
		f.At.End = p.previousEnd()
	} else if prefix, key, ok := strings.Cut(t.Text, "."); ok && (strings.EqualFold(prefix, "attributes") || strings.EqualFold(prefix, "resource")) {
		f.Scope = strings.ToLower(prefix)
		f.Key = key
	}
	return f, true
}

func classify(t Token) Value {
	v := Value{Text: t.Text, At: t.Span}
	switch {
	case t.Kind == String || t.Kind == BracketKey:
		v.Kind = StringValue
	case strings.HasPrefix(t.Text, "$"):
		v.Kind = VariableValue
	case t.Text == "*":
		v.Kind = StarValue
	case durationPattern.MatchString(t.Text):
		v.Kind = DurationValue
	case numberPattern.MatchString(t.Text):
		v.Kind = NumberValue
	case strings.EqualFold(t.Text, "true") || strings.EqualFold(t.Text, "false"):
		v.Kind = BooleanValue
	default:
		v.Kind = WordValue
	}
	return v
}

func (p *parser) value() (Value, bool) {
	for p.is("+") {
		p.report(p.take().Span, "plus")
	}
	t := p.peek()
	if p.is("-") {
		next := p.ahead(1)
		v := classify(next)
		if (next.Kind == Word || next.Kind == Number) && (v.Kind == NumberValue || v.Kind == DurationValue) && !strings.HasPrefix(next.Text, "-") {
			p.take()
			p.take()
			v.Text = "-" + v.Text
			v.At.Start = t.Span.Start
			return v, true
		}
		return Value{}, false
	}
	if t.Kind != Word && t.Kind != String && t.Kind != Number && !(t.Kind == Punct && t.Text == "*") {
		return Value{}, false
	}
	p.take()
	p.checkQuoted(t)
	v := classify(t)
	if v.Kind == VariableValue && strings.HasPrefix(v.Text, "$__") {
		p.report(v.At, "variable")
	}
	return v, true
}

func (p *parser) in(f Field, neg bool) Expr {
	p.take()
	p.expectNext(Expect{Class: "value", Field: &f, Scope: "in"})
	x := &In{Field: f, Negated: neg, At: Span{f.At.Start, p.previousEnd()}}
	if p.peek().Kind == Word && strings.HasPrefix(p.peek().Text, "$") {
		v, _ := p.value()
		x.Variable = strings.TrimPrefix(v.Text, "$")
		x.At.End = v.At.End
		p.node(x)
		return x
	}
	if !p.eat("(") {
		p.report(p.peek().Span, "list")
		return nil
	}
	p.expectNext(Expect{Class: "value", Field: &f, Scope: "in"})
	for p.peek().Kind != EOF && !p.is(")") {
		v, ok := p.value()
		if !ok {
			p.report(p.peek().Span, "listValue")
			p.skipRun()
			break
		}
		if p.bounded && len(x.Values) >= MaxListItems {
			p.bound(v.At, "listBound")
		}
		x.Values = append(x.Values, v)
		if p.completion {
			p.completionUsed[strings.ToLower(v.Text)] = true
		}
		if v.Kind == StarValue {
			p.issue(v.At, diagnostic("wildcard"), wildcardHint(v.Text))
		}
		if !p.eat(",") {
			break
		}
		p.expectNext(Expect{Class: "value", Field: &f, Scope: "in"})
		if p.is(")") {
			p.report(p.peek().Span, "listValue")
		}
	}
	if len(x.Values) == 0 {
		p.report(p.peek().Span, "listValue")
	}
	if !p.eat(")") {
		p.report(p.peek().Span, "list")
	}
	x.At.End = p.previousEnd()
	p.node(x)
	return x
}

func (p *parser) call() *Call {
	t := p.take()
	x := &Call{Name: strings.ToLower(t.Text), At: t.Span}
	open := p.take()
	if open.Span.Start != t.Span.End {
		p.report(Span{t.Span.Start, open.Span.End}, "callSpace", x.Name)
	}
	p.enter(open.Span)
	for p.peek().Kind != EOF && !p.is(")") && !p.is("|") {
		before := p.i
		arg := CallArg{}
		switch t := p.peek(); {
		case t.Kind == Word && p.ahead(1).Text == "(":
			arg.Call = p.call()
		case t.Kind == String || t.Kind == Number || t.Kind == Word && numberPattern.MatchString(t.Text) || p.is("-"):
			v, ok := p.value()
			if ok {
				arg.Value = &v
			} else {
				p.report(t.Span, "callArg", x.Name)
				p.take()
			}
		case t.Kind == Word:
			f, ok := p.field()
			if ok {
				arg.Field = &f
			}
		default:
			p.report(t.Span, "callArg", x.Name)
			p.take()
		}
		x.Args = append(x.Args, arg)
		if p.i == before {
			p.take()
		}
		if !p.eat(",") {
			break
		}
	}
	if !p.eat(")") {
		p.report(open.Span, "callClose")
	}
	p.leave()
	if isComparison(p.lower()) {
		op := p.take()
		v, ok := p.value()
		if ok {
			x.Cmp = &Comparison{Op: op.Text, Value: v}
		} else {
			p.report(op.Span, "value", x.Name, op.Text, x.Name, op.Text, "1000")
		}
	}
	x.At.End = p.previousEnd()
	var children []any
	for _, a := range x.Args {
		if a.Call != nil {
			children = append(children, a.Call)
		}
	}
	p.node(x, children...)
	return x
}

func (p *parser) checkQuoted(t Token) {
	if !t.Unclosed {
		return
	}
	if t.Kind == Raw {
		p.report(t.Span, "raw")
	} else {
		p.report(t.Span, "quote", t.Quote)
	}
}

func (p *parser) colonProblem(f Field, v Value, at Span) {
	run := p.runSpan(at.Start)
	value := p.text(Span{v.At.Start, run.End})
	prefix := p.text(Span{f.At.Start, v.At.Start})
	after := v.Text
	if strings.HasPrefix(value, "//") {
		value = p.text(run)
		prefix = ""
		after = f.Key
	}
	p.issue(run, diagnostic("colon", after), quoteValueHint(prefix, value))
	p.skipRun()
}

func (p *parser) wildcard(v Value) {
	at := p.runSpan(v.At.Start)
	p.issue(at, diagnostic("wildcard"), wildcardHint(p.text(at)))
	p.skipRun()
}

func (p *parser) runSpan(start int) Span {
	end := start
	for end < len(p.runes) && !unicode.IsSpace(p.runes[end]) {
		end++
	}
	return Span{start, end}
}

// Recovery skips a malformed run, respecting delimiters and whitespace at the
// term's depth. Clause words and pipes remain available to the outer parser.
func (p *parser) skipRun() {
	if p.peek().Kind == EOF {
		return
	}
	first := p.take()
	end := first.Span.End
	depth := 0
	if first.Kind == Punct && first.Text == "(" {
		depth = 1
	}
	for p.peek().Kind != EOF {
		t := p.peek()
		if depth == 0 && (t.Span.Start > end || p.is("|") || p.is(")")) {
			break
		}
		if p.is("(") {
			depth++
		}
		if p.is(")") {
			depth--
		}
		end = p.take().Span.End
	}
}

func (p *parser) badTerm() {
	t := p.take()
	switch t.Text {
	case "{":
		p.report(t.Span, "selector")
	case "@":
		word := p.peek()
		p.report(Span{t.Span.Start, word.Span.End}, "at", "@"+word.Text, word.Text)
	case "!":
		at := p.runSpan(t.Span.Start)
		p.report(at, "bang", strings.TrimPrefix(p.text(at), "!"))
	default:
		p.issue(t.Span, t.Message, t.Hint)
	}
	if p.peek().Span.Start == t.Span.End {
		p.skipRun()
	}
}

func (p *parser) foreignTerm() bool {
	t := p.peek()
	word := p.lower()
	if word == "since" || word == "until" {
		p.report(t.Span, "time", word)
		p.take()
		if p.peek().Kind == Word {
			p.take()
		}
		return true
	}
	if (word == "ago" || word == "now") && p.ahead(1).Text == "(" {
		p.report(t.Span, "timeCall", word)
		p.skipRun()
		return true
	}
	if word == "earliest" && p.ahead(1).Text == "=" || word == "_time" && p.ahead(1).Text == ":" {
		p.report(t.Span, "time", word)
		p.skipRun()
		return true
	}
	if word == "facet" || (word == "order" || word == "group") && strings.EqualFold(p.ahead(1).Text, "by") {
		p.report(t.Span, "foreignClause", word)
		p.skipRun()
		if p.is("by") {
			p.take()
		}
		return true
	}
	return false
}

func (p *parser) unexpected() {
	t := p.peek()
	if t.Kind == Bad {
		p.badTerm()
		return
	}
	if p.eat(")") {
		p.report(t.Span, "unmatched")
		return
	}
	if isComparison(t.Text) || t.Text == ":" || t.Text == "~" || t.Text == "!~" {
		p.report(t.Span, "fieldBefore", t.Text)
		p.skipRun()
		return
	}
	if p.eat("+") {
		p.report(t.Span, "plus")
		return
	}
	if p.is("*") {
		p.wildcard(Value{Text: t.Text, At: t.Span})
		return
	}
	if !p.foreignTerm() {
		p.report(t.Span, "unexpected", t.Text)
		p.take()
	}
}

func (p *parser) skipStage() {
	for p.peek().Kind != EOF && !p.is("sort") && !p.is("limit") && !p.is("|") {
		p.take()
	}
}

func (p *parser) parseSort() *Sort {
	start := p.take()
	p.expectNext(Expect{Class: "sort_key"})
	x := &Sort{At: start.Span}
	signed := p.is("+") || p.is("-")
	if signed {
		x.Ascending = p.take().Text == "+"
	}
	p.expectNext(Expect{Class: "sort_key"})
	t := p.peek()
	if t.Kind != Word && t.Kind != Ident {
		p.report(t.Span, "sortKey")
		return x
	}
	x.Key = strings.ToLower(p.take().Text)
	if p.is("asc") || p.is("desc") {
		if signed {
			p.report(p.peek().Span, "direction")
		}
		x.Ascending = !strings.EqualFold(p.take().Text, "desc")
	}
	x.At.End = p.previousEnd()
	return x
}

func (p *parser) parseLimit() *Limit {
	start := p.take()
	p.expectNext(Expect{Class: "integer"})
	t := p.peek()
	x := &Limit{At: start.Span}
	n, err := strconv.Atoi(t.Text)
	if t.Kind != Word || err != nil || n < 1 || n > 1000 || strings.IndexFunc(t.Text, func(r rune) bool { return !digit(r) }) >= 0 {
		p.report(t.Span, "limit")
		if t.Kind != EOF {
			p.take()
		}
	} else {
		x.N = n
		p.take()
	}
	x.At.End = p.previousEnd()
	return x
}

func (p *parser) stage(start int) *Stage {
	s := &Stage{At: Span{Start: start}}
	if p.completion {
		p.completionQuery.Stage = s
	}
	p.expectNext(Expect{Class: "measure"})
	for p.peek().Kind != EOF && !p.is("|") && !clauses[p.lower()] {
		before := p.i
		if p.peek().Kind == Ident && foreignStages[p.lower()] {
			t := p.take()
			p.report(t.Span, "foreignStage", strings.ToLower(t.Text))
			for p.peek().Kind != EOF && !p.is("|") && !p.is(",") && !clauses[p.lower()] && !foreignStages[p.lower()] && p.ahead(1).Text != "(" {
				p.take()
			}
			continue
		}
		if p.peek().Kind == Ident && foreignCalls[p.lower()] && p.ahead(1).Text == "(" {
			p.report(p.peek().Span, "foreignCall", p.lower())
			p.skipRun()
			continue
		}
		expr := p.mexpr(0)
		if expr != nil {
			m := Measure{Expr: expr, At: expr.Span()}
			if modifiers[p.lower()] && p.eat("as") {
				t := p.peek()
				if t.Kind == Ident {
					m.Alias = p.take().Text
					m.At.End = t.Span.End
				} else {
					p.report(t.Span, "alias")
				}
			}
			leaves, share, distribution := measureLeaves(expr)
			if leaves == 0 {
				p.report(expr.Span(), "aggregate")
			}
			_, call := expr.(*MCall)
			_, countIf := expr.(*MCountIf)
			if distribution && !call {
				p.report(expr.Span(), "histogram")
			}
			if !call && !countIf {
				if m.Alias == "" {
					p.report(expr.Span(), "measureName")
				}
				if share {
					p.report(expr.Span(), "share")
				}
			}
			if call, ok := expr.(*MCall); ok && call.Name == "histogram" {
				if s.Histogram != nil || len(s.Measures) > 0 {
					p.report(call.At, "histogram")
				}
				if m.Alias != "" {
					p.report(m.At, "histogramAlias")
				}
				if len(call.Args) != 1 || call.Args[0].Field == nil {
					p.report(call.At, "histogramField")
				} else {
					s.Histogram = call.Args[0].Field
				}
			} else {
				if s.Histogram != nil {
					p.report(m.At, "histogram")
				}
				s.Measures = append(s.Measures, m)
			}
		}
		if p.eat(",") {
			p.expectNext(Expect{Class: "measure"})
			if p.peek().Kind == EOF || clauses[p.lower()] {
				p.report(p.peek().Span, "measure")
			}
			continue
		}
		if expr != nil && p.peek().Kind != EOF && !p.is("|") && !clauses[p.lower()] {
			p.report(p.peek().Span, "measureComma")
		}
		if p.i == before {
			p.unexpected()
		}
	}
	p.expectNext(Expect{Class: "clause", Scope: "measure"})
	for p.is("by") || p.is("every") {
		if p.eat("by") {
			if len(s.By) > 0 {
				p.report(p.tokens[p.i-1].Span, "duplicate", "by")
			}
			if s.Bucket != "" {
				p.report(p.tokens[p.i-1].Span, "order", "by")
			}
			if len(s.Measures) == 0 && s.Histogram == nil {
				p.report(p.tokens[p.i-1].Span, "byFirst")
			}
			for {
				p.expectNext(Expect{Class: "field", Func: "by", Arg: len(s.By)})
				f, ok := p.field()
				if !ok {
					if p.peek().Kind != EOF && !clauses[p.lower()] {
						p.take()
					}
					break
				}
				s.By = append(s.By, f)
				if len(s.By) == 4 {
					p.report(f.At, "byMax")
				}
				if s.Histogram != nil && len(s.By) == 2 {
					p.report(f.At, "histogramBy")
				}
				if !p.eat(",") {
					break
				}
			}
		} else {
			at := p.take().Span
			p.expectNext(Expect{Class: "bucket"})
			if len(s.Measures) == 0 && s.Histogram == nil {
				p.report(at, "everyFirst")
			}
			if s.Bucket != "" {
				p.report(at, "duplicate", "every")
			}
			t := p.peek()
			s.Bucket = strings.ToLower(t.Text)
			if !buckets[s.Bucket] {
				p.report(t.Span, "bucket", t.Text)
			}
			if t.Kind != EOF {
				p.take()
			}
		}
	}
	p.expectNext(Expect{Class: "clause", Scope: "stage"})
	if len(s.Measures) == 0 && s.Histogram == nil && len(p.problems) == 0 {
		p.report(p.peek().Span, "measure")
	}
	s.At.End = p.previousEnd()
	return s
}

func measurePrecedence(t Token) int {
	if t.Kind != Punct {
		return -1
	}
	switch t.Text {
	case "+", "-":
		return 1
	case "*", "/":
		return 2
	}
	return -1
}

func (p *parser) canMFactor() bool {
	t := p.peek()
	return t.Kind == Number || t.Kind == Ident && t.Text != "as" || p.is("(") || p.is("-")
}

func (p *parser) mexpr(minPrec int) MExpr {
	left := p.mfactor()
	if left == nil {
		return nil
	}
	for prec := measurePrecedence(p.peek()); prec >= minPrec; prec = measurePrecedence(p.peek()) {
		op := p.take()
		if !p.canMFactor() {
			p.report(op.Span, "measureSides", op.Text)
			return left
		}
		right := p.mexpr(prec + 1)
		if right == nil {
			return left
		}
		x := &MBinary{Op: op.Text[0], L: left, R: right, At: Span{left.Span().Start, right.Span().End}}
		p.node(x, left, right)
		left = x
	}
	return left
}

func (p *parser) mfactor() MExpr {
	p.expectNext(Expect{Class: "measure"})
	t := p.peek()
	if p.eat("-") {
		p.enter(t.Span)
		x := p.mfactor()
		p.leave()
		if x == nil {
			return nil
		}
		n := &MNeg{X: x, At: Span{t.Span.Start, x.Span().End}}
		p.node(n, x)
		return n
	}
	if p.eat("(") {
		p.enter(t.Span)
		x := p.mexpr(0)
		p.leave()
		if !p.eat(")") {
			p.report(t.Span, "unclosedGroup")
		} else if x != nil {
			setMeasureSpan(x, Span{t.Span.Start, p.previousEnd()})
		}
		return x
	}
	if t.Kind == Number {
		p.take()
		v, err := strconv.ParseFloat(t.Text, 64)
		if err != nil {
			p.report(t.Span, "measure")
			return nil
		}
		x := &MConst{Value: v, Text: t.Text, At: t.Span}
		p.node(x)
		return x
	}
	if t.Kind == Ident && p.ahead(1).Text == "(" {
		return p.mcall()
	}
	if t.Kind == Bad {
		p.badTerm()
		return nil
	}
	p.report(t.Span, "measure")
	if t.Kind != EOF {
		p.take()
	}
	return nil
}

func (p *parser) mcall() MExpr {
	name := p.take()
	open := p.take()
	at := Span{Start: name.Span.Start}
	p.enter(open.Span)
	if strings.EqualFold(name.Text, "count_if") {
		filter := p.conjunction(p.filters(true))
		if filter == nil {
			p.report(open.Span, "sides", "count_if")
		}
		if !p.eat(")") {
			p.report(open.Span, "callClose")
		}
		p.leave()
		at.End = p.previousEnd()
		x := &MCountIf{Filter: filter, At: at}
		p.node(x, filter)
		return x
	}
	x := &MCall{Name: strings.ToLower(name.Text), At: at}
	p.expectNext(Expect{Class: "marg", Func: x.Name, Arg: 0})
	for p.peek().Kind != EOF && !p.is(")") && !p.is("|") {
		t := p.peek()
		arg := MArg{At: t.Span}
		switch {
		case p.eat("*"):
			arg.Star = true
		case t.Kind == Number || p.is("-"):
			v, ok := p.value()
			if !ok || v.Kind != NumberValue {
				p.report(t.Span, "measureArg")
				p.take()
			} else {
				n, err := strconv.ParseFloat(v.Text, 64)
				if err != nil {
					p.report(v.At, "measureArg")
				} else {
					arg.Number = &n
				}
				arg.At = v.At
			}
		case t.Kind == Ident || t.Kind == Path:
			f, ok := p.field()
			if ok {
				arg.Field = &f
				arg.At = f.At
			}
		default:
			p.report(t.Span, "measureArg")
			p.take()
		}
		x.Args = append(x.Args, arg)
		if !p.eat(",") {
			break
		}
		p.expectNext(Expect{Class: "marg", Func: x.Name, Arg: len(x.Args)})
	}
	if !p.eat(")") {
		p.report(open.Span, "callClose")
	}
	p.leave()
	x.At.End = p.previousEnd()
	for _, arg := range x.Args {
		if arg.Star && (x.Name != "count" || len(x.Args) != 1) {
			p.report(arg.At, "starArg")
		}
	}
	p.node(x)
	return x
}

func measureLeaves(x MExpr) (int, bool, bool) {
	switch x := x.(type) {
	case *MCall:
		return 1, x.Name == "share", x.Name == "histogram"
	case *MCountIf:
		return 1, false, false
	case *MBinary:
		l, ls, lh := measureLeaves(x.L)
		r, rs, rh := measureLeaves(x.R)
		return l + r, ls || rs, lh || rh
	case *MNeg:
		return measureLeaves(x.X)
	}
	return 0, false, false
}
