package fanql

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/labstack/fanout/internal/panel"
)

const (
	maxTextRunes = 4000
	maxTextBytes = 8192
	maxTokens    = 1024
	maxNodes     = 512
	maxDepth     = 16
	maxListItems = 100
	maxProblems  = 20
)

var numberPattern = regexp.MustCompile(`^-?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?$`)
var durationPattern = regexp.MustCompile(`(?i)^-?(?:[0-9]+(?:\.[0-9]*)?|\.[0-9]+)(?:[eE][+-]?[0-9]+)?(?:us|ms|s|m|h)$`)

type parser struct {
	vocab           vocabulary
	tokens          lexTokens
	runes           []rune
	i               int
	problems        panel.Problems
	bounded         bool
	nodes, depth    int
	heights         map[any]int
	completion      bool
	expected        []expect
	completionQuery *queryTree
	completionUsed  map[string]bool
}

// Completion uses the same productions and alternatives as Parse. Unwinding at
// the first unfinished production preserves its context without error recovery
// inventing a different position for the cursor.
type completionEnd struct{}

func (p *parser) expectNext(alternatives ...expect) {
	if p.completion && p.peek().Kind == eof {
		p.expected = append(p.expected, alternatives...)
		panic(completionEnd{})
	}
}

func (p *parser) expectTerm(nested bool) {
	alternatives := []expect{{Class: "field"}, {Class: "free_text"}, {Class: "operator", Scope: "term"}, {Class: "variable"}}
	if !nested {
		alternatives = append([]expect{{Class: "clause"}}, alternatives...)
	}
	p.expectNext(alternatives...)
}

// A private sentinel unwinds only a bound failure, before further recursion or
// allocation. Other panics remain visible to callers and tests.
type parseBound struct{}

// Parse returns no query whenever a problem is present. source is empty for a
// whole query, or the page-supplied source for a search-bar filter.
func parse(text string, source string) (*queryTree, panel.Problems) {
	return parseWith(text, source, currentVocabulary())
}
func parseWith(text string, source string, vocab vocabulary) (q *queryTree, problems panel.Problems) {
	p := &parser{vocab: vocab, runes: []rune(text), bounded: true, heights: make(map[any]int)}
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
	if len(p.runes) > maxTextRunes || len(text) > maxTextBytes {
		p.bound(span{0, len(p.runes)}, diagTextBound)
	}
	p.tokens = lex(text)
	if len(p.tokens)-1 > maxTokens {
		p.bound(p.tokens[maxTokens].Span, diagTokensBound)
	}
	q = p.query(source)
	return q, nil
}

// parseEntry is the unbounded internal entry parser used by the lift gate.
func parseEntry(text string) (filterExpr, panel.Problems) {
	p := &parser{vocab: currentVocabulary(), runes: []rune(text), tokens: lex(text), heights: make(map[any]int)}
	terms := p.filters(false)
	for p.peek().Kind != eof {
		p.unexpected()
	}
	x := p.conjunction(terms)
	problems := p.finish()
	if len(problems) > 0 {
		return nil, problems
	}
	return x, nil
}

func (p *parser) peek() lexToken       { return p.tokens[p.i] }
func (p *parser) ahead(n int) lexToken { return p.tokens[min(p.i+n, len(p.tokens)-1)] }
func (p *parser) take() lexToken {
	t := p.peek()
	if t.Kind != eof {
		p.i++
	}
	return t
}

// Quoted and raw tokens carry decoded text that can spell an operator or
// delimiter. Only unquoted grammar tokens may participate in syntax matching.
func grammarText(t lexToken) string {
	if t.Kind == tokenWord || t.Kind == tokenIdent || t.Kind == tokenPunct {
		return strings.ToLower(t.Text)
	}
	return ""
}

func (p *parser) lower() string { return grammarText(p.peek()) }
func (p *parser) is(s string) bool {
	t := p.peek()
	return (t.Kind == tokenWord || t.Kind == tokenIdent || t.Kind == tokenPunct) && strings.EqualFold(t.Text, s)
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
func (p *parser) text(at span) string { return string(p.runes[at.Start:at.End]) }
func (p *parser) issue(at span, message, hint string) {
	if p.completion {
		return
	}
	p.problems = append(p.problems, panel.Problem{Path: "text", Message: message, Hint: hint, Span: &panel.TextSpan{Start: at.Start, End: at.End}})
}
func (p *parser) report(at span, code diagnosticCode, args ...any) {
	p.issue(at, diagnostic(code, args...), hint(code))
}
func (p *parser) bound(at span, code diagnosticCode) {
	p.problems = nil
	p.report(at, code)
	panic(parseBound{})
}
func (p *parser) enter(at span) {
	if p.bounded && p.depth >= maxDepth {
		p.bound(at, diagDepthBound)
	}
	p.depth++
}
func (p *parser) leave() { p.depth-- }
func (p *parser) node(x any, children ...any) {
	p.nodes++
	if p.bounded && p.nodes > maxNodes {
		p.bound(x.(interface{ Span() span }).Span(), diagNodesBound)
	}
	height := 0
	for _, child := range children {
		if child != nil {
			height = max(height, p.heights[child]+1)
		}
	}
	p.heights[x] = height
	if p.bounded && height+p.depth > maxDepth {
		p.bound(x.(interface{ Span() span }).Span(), diagDepthBound)
	}
}
func (p *parser) finish() panel.Problems {
	sort.SliceStable(p.problems, func(i, j int) bool { return p.problems[i].Span.Start < p.problems[j].Span.Start })
	if len(p.problems) > maxProblems {
		at := *p.problems[maxProblems].Span
		p.problems = p.problems[:maxProblems]
		p.issue(span{at.Start, at.End}, diagnostic(diagMore), "")
	}
	return p.problems
}

func (p *parser) query(source string) *queryTree {
	q := &queryTree{Span: span{0, len(p.runes)}}
	p.completionQuery = q
	if source != "" {
		q.Source = strings.ToLower(source)
		if !sources[q.Source] {
			p.report(span{}, diagSource, source)
		}
		if p.is("where") {
			p.report(p.take().Span, diagPageWhere)
		}
		q.Where = p.filters(false)
		for p.peek().Kind != eof {
			p.report(p.peek().Span, diagPageFilter)
			p.skipRun()
		}
		return q
	}
	p.expectNext(expect{Class: "source"})
	first := p.take()
	q.Source = strings.ToLower(first.Text)
	q.SourceSpan = first.Span
	if first.Kind != tokenWord || !sources[q.Source] {
		switch q.Source {
		case "from", "index", "source", "search":
			p.report(first.Span, diagFrom, q.Source)
		default:
			suggestion := hint(diagSource)
			if q.Source == "traces" {
				suggestion = hint(diagTraces)
			}
			p.issue(first.Span, diagnostic(diagSource, first.Text), suggestion)
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
		p.issue(p.tokens[whereAt].Span, diagnostic(diagWhereEmpty), hint)
	}
	for p.peek().Kind != eof {
		switch {
		case p.is("|"):
			pipe := p.take()
			if p.is("=") || p.is("~") {
				op := p.take()
				p.report(span{pipe.Span.Start, op.Span.End}, diagPipeOp, "|"+op.Text)
				p.skipRun()
				continue
			}
			if p.is("sort") && grammarText(p.ahead(1)) == "by" {
				p.report(p.take().Span, diagForeignStage, "sort")
				p.skipStage()
				continue
			}
			if q.Stage != nil {
				if !p.is("sort") && !p.is("limit") {
					p.report(pipe.Span, diagSecondStage)
					p.skipStage()
					continue
				}
			} else if !p.is("sort") && !p.is("limit") {
				q.Stage = p.stage(pipe.Span.Start)
				continue
			}
		case p.is("where"):
			p.report(p.take().Span, diagWhereTwice)
			q.Where = append(q.Where, p.filters(false)...)
		case p.is("sort"):
			if q.Limit != nil {
				p.report(p.peek().Span, diagLimitBeforeSort)
			}
			if q.Sort != nil {
				p.report(p.peek().Span, diagDuplicate, "sort")
			}
			q.Sort = p.parseSort()
		case p.is("limit"):
			if q.Limit != nil {
				p.report(p.peek().Span, diagDuplicate, "limit")
			}
			q.Limit = p.parseLimit()
		case p.is("by"), p.is("every"):
			if q.Stage == nil {
				code := diagByFirst
				if p.is("every") {
					code = diagEveryFirst
				}
				p.report(p.take().Span, code)
				p.skipRun()
			} else {
				p.report(p.take().Span, diagOrder, p.tokens[p.i-1].Text)
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
	return t.Kind == eof || p.is(")") || p.is("|") || (!nested && t.Kind == tokenWord && clauses[p.lower()])
}

// filters keeps conjunction entries separate. Only an explicit group or an OR
// chain creates a single entry around multiple terms.
func (p *parser) filters(nested bool) []filterExpr {
	var terms []filterExpr
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
					p.report(tok.Span, diagSides, op)
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
			p.report(p.take().Span, diagSides, p.tokens[p.i-1].Text)
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
		at := span{terms[0].Span().Start, terms[len(terms)-1].Span().End}
		first := p.groupingEdit(terms, ops, ops[mixed])
		second := p.groupingEdit(terms, ops, ops[0])
		p.issue(at, diagnostic(diagMixed), groupingHint(first, second))
		span := panel.TextSpan{Start: at.Start, End: at.End}
		p.problems[len(p.problems)-1].FixIts = []panel.FixIt{{Label: first, Span: span, Text: first}, {Label: second, Span: span, Text: second}}
	}
	if mode == "or" && len(terms) > 1 {
		x := &orExpr{Terms: terms, At: span{terms[0].Span().Start, terms[len(terms)-1].Span().End}}
		p.node(x)
		return []filterExpr{x}
	}
	return terms
}

func (p *parser) conjunction(terms []filterExpr) filterExpr {
	if len(terms) == 0 {
		return nil
	}
	if len(terms) == 1 {
		return terms[0]
	}
	x := &andExpr{Terms: terms, At: span{terms[0].Span().Start, terms[len(terms)-1].Span().End}}
	p.node(x)
	return x
}

// Each alternative gives one operator kind priority explicitly, grouping every
// run of that kind. Both edits are valid even when a longer list mixes repeatedly.
func (p *parser) groupingEdit(terms []filterExpr, ops []string, grouped string) string {
	var out strings.Builder
	for i := 0; i < len(terms); {
		j := i
		for j < len(ops) && ops[j] == grouped {
			j++
		}
		if j > i {
			out.WriteByte('(')
		}
		out.WriteString(p.text(span{terms[i].Span().Start, terms[j].Span().End}))
		if j > i {
			out.WriteByte(')')
		}
		if j+1 < len(terms) {
			out.WriteString(p.text(span{terms[j].Span().End, terms[j+1].Span().Start}))
		}
		i = j + 1
	}
	return out.String()
}

func setExprSpan(x filterExpr, at span) {
	switch x := x.(type) {
	case *andExpr:
		x.At = at
	case *orExpr:
		x.At = at
	case *notExpr:
		x.At = at
	case *comparison:
		x.At = at
	case *inExpr:
		x.At = at
	case *betweenExpr:
		x.At = at
	case *likeExpr:
		x.At = at
	case *nullExpr:
		x.At = at
	case *existsExpr:
		x.At = at
	case *filterCall:
		x.At = at
	case *textExpr:
		x.At = at
	case *rawEntry:
		x.At = at
	}
}

func setMeasureSpan(x mExpr, at span) {
	switch x := x.(type) {
	case *mCall:
		x.At = at
	case *mCountIf:
		x.At = at
	case *mBinary:
		x.At = at
	case *mNeg:
		x.At = at
	case *mConst:
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

func (p *parser) filterCallStart() bool {
	name, open := p.peek(), p.ahead(1)
	if name.Kind != tokenWord || grammarText(open) != "(" || name.Text == "" {
		return false
	}
	for i, r := range name.Text {
		if i == 0 && !identStart(r) || i > 0 && !identContinue(r) {
			return false
		}
	}
	if open.Span.Start == name.Span.End {
		return true
	}
	// Preserve the useful typo diagnostic for registered functions, while an
	// ordinary search word followed by whitespace and a group stays two terms.
	for _, function := range p.vocab.functions() {
		if strings.EqualFold(name.Text, function) {
			return true
		}
	}
	return false
}

func (p *parser) term(nested bool) filterExpr {
	p.expectTerm(nested)
	if p.is("-") || p.is("not") {
		t := p.take()
		p.enter(t.Span)
		x := p.term(nested)
		p.leave()
		if x == nil {
			return nil
		}
		n := &notExpr{X: x, At: span{t.Span.Start, x.Span().End}}
		p.node(n, x)
		return n
	}
	return p.primary(nested)
}

func (p *parser) primary(nested bool) filterExpr {
	t := p.peek()
	if p.eat("(") {
		p.enter(t.Span)
		terms := p.filters(true)
		p.leave()
		closed := p.eat(")")
		if !closed {
			p.report(t.Span, diagUnclosedGroup)
		}
		x := p.conjunction(terms)
		if x != nil && closed {
			setExprSpan(x, span{t.Span.Start, p.previousEnd()})
		}
		return x
	}
	if t.Kind == tokenRaw {
		p.take()
		p.checkQuoted(t)
		x := &rawEntry{Text: t.Text, At: t.Span}
		p.node(x)
		return x
	}
	if t.Kind == tokenString {
		p.take()
		p.checkQuoted(t)
		v := literalValue{Kind: stringValue, Text: t.Text, At: t.Span}
		x := &textExpr{Value: v, At: t.Span}
		p.node(x)
		return x
	}
	if t.Kind == tokenBad {
		p.badTerm()
		return nil
	}
	if t.Kind != tokenWord {
		if t.Kind != eof {
			p.unexpected()
		} else {
			p.report(t.Span, diagSides, "not")
		}
		return nil
	}
	word := strings.ToLower(t.Text)
	if p.foreignTerm() {
		return nil
	}
	next := grammarText(p.ahead(1))
	fieldOperator := isComparison(next) || next == ":" || next == "~" || next == "!~" || next == "is" || isFieldWordOperator(next) || next == "not" && isFieldWordOperator(grammarText(p.ahead(2)))
	if reserved[word] && !fieldOperator {
		p.report(t.Span, diagReserved, word)
		p.take()
		return nil
	}
	if p.filterCallStart() {
		return p.call()
	}
	field, ok := p.field()
	if !ok {
		p.skipRun()
		return nil
	}
	op := p.lower()
	p.expectNext(expect{Class: "operator", Field: &field})
	if isComparison(op) || op == ":" || op == "~" || op == "!~" {
		if numberPattern.MatchString(field.Key) || durationPattern.MatchString(field.Key) {
			p.report(field.At, diagFieldBefore, op)
			p.skipRun()
			return nil
		}
		oper := p.take()
		if (op == "=" && (p.is("=") || p.is("~"))) || op == ":" && p.is(":") {
			code := diagEqeq
			if p.is("~") {
				code = diagRegex
			}
			if op == ":" {
				code = diagCast
			}
			end := p.take().Span.End
			p.report(span{oper.Span.Start, end}, code)
			p.skipRun()
			return nil
		}
		if op == ":" && isComparison(p.lower()) {
			op = p.take().Text
		}
		p.expectNext(expect{Class: "value", Field: &field, Scope: op})
		v, valid := p.value()
		if !valid {
			example := "api"
			if field.Key == "duration" {
				example = "500ms"
			}
			p.report(span{field.At.Start, oper.Span.End}, diagValue, field.Key, op, field.Key, op, example)
			p.skipRun()
			return nil
		}
		at := span{field.At.Start, v.At.End}
		if p.is(":") {
			p.colonProblem(field, v, at)
			return nil
		}
		if p.is("*") && p.peek().Span.Start == v.At.End {
			p.wildcard(v)
			return nil
		}
		if op == ":" && v.Kind == starValue {
			x := &existsExpr{Field: field, At: at}
			p.node(x)
			return x
		}
		if v.Kind == starValue {
			p.issue(at, diagnostic(diagWildcard), wildcardHint(v.Text))
			return nil
		}
		x := &comparison{Field: field, Op: op, Value: v, At: at}
		p.node(x)
		return x
	}
	neg := false
	if expressionWords[op] && op == "not" && isFieldWordOperator(grammarText(p.ahead(1))) {
		neg = true
		p.take()
		op = p.lower()
	}
	switch op {
	case "in":
		return p.in(field, neg)
	case "between":
		p.take()
		p.expectNext(expect{Class: "value", Field: &field, Scope: "between"})
		lo, ok := p.value()
		if !ok || !p.eat("and") {
			p.report(span{field.At.Start, p.previousEnd()}, diagBetween)
			return nil
		}
		p.expectNext(expect{Class: "value", Field: &field, Scope: "between"})
		hi, ok := p.value()
		if !ok {
			p.report(span{field.At.Start, p.previousEnd()}, diagBetween)
			return nil
		}
		x := &betweenExpr{Field: field, Lo: lo, Hi: hi, Negated: neg, At: span{field.At.Start, hi.At.End}}
		p.node(x)
		return x
	case "is":
		p.take()
		neg = p.eat("not")
		if !p.eat("null") {
			p.report(span{field.At.Start, p.previousEnd()}, diagNull)
			return nil
		}
		x := &nullExpr{Field: field, Negated: neg, At: span{field.At.Start, p.previousEnd()}}
		p.node(x)
		return x
	case "like", "ilike":
		p.take()
		p.expectNext(expect{Class: "value", Field: &field})
		v, ok := p.value()
		if !ok || v.Kind != stringValue {
			p.report(span{field.At.Start, p.previousEnd()}, diagLike)
			return nil
		}
		x := &likeExpr{Field: field, Pattern: v, Insensitive: op == "ilike", Negated: neg, At: span{field.At.Start, v.At.End}}
		p.node(x)
		return x
	}
	if neg {
		p.report(p.tokens[p.i-1].Span, diagUnexpected, "not")
		return nil
	}
	if !nested && clauses[word] {
		p.report(t.Span, diagUnexpected, t.Text)
		return nil
	}
	if p.is("*") && p.peek().Span.Start == field.At.End {
		p.wildcard(literalValue{Text: t.Text, At: t.Span})
		return nil
	}
	x := &textExpr{Value: literalValue{Kind: wordValue, Text: p.text(field.At), At: field.At}, At: field.At}
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

func (p *parser) field() (fieldName, bool) {
	t := p.peek()
	if t.Kind != tokenWord && t.Kind != tokenIdent && t.Kind != tokenPath {
		p.report(t.Span, diagField)
		return fieldName{}, false
	}
	p.take()
	f := fieldName{Key: t.Text, Single: !strings.Contains(t.Text, "."), At: t.Span}
	if p.is("[") && (strings.EqualFold(t.Text, "attributes") || strings.EqualFold(t.Text, "resource")) {
		p.take()
		p.expectNext(expect{Class: "bracket_key", Scope: strings.ToLower(t.Text)})
		key := p.peek()
		if key.Kind != tokenBracketKey && key.Kind != tokenString {
			p.report(key.Span, diagBracket)
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
			p.report(key.Span, diagBracket)
			return f, false
		}
		f.At.End = p.previousEnd()
	} else if prefix, key, ok := strings.Cut(t.Text, "."); ok && (strings.EqualFold(prefix, "attributes") || strings.EqualFold(prefix, "resource")) {
		f.Scope = strings.ToLower(prefix)
		f.Key = key
	}
	return f, true
}

func classify(t lexToken) literalValue {
	v := literalValue{Text: t.Text, At: t.Span}
	switch {
	case t.Kind == tokenString || t.Kind == tokenBracketKey:
		v.Kind = stringValue
	case strings.HasPrefix(t.Text, "$"):
		v.Kind = variableValue
	case t.Text == "*":
		v.Kind = starValue
	case durationPattern.MatchString(t.Text):
		v.Kind = durationValue
	case numberPattern.MatchString(t.Text):
		v.Kind = numberValue
	case strings.EqualFold(t.Text, "true") || strings.EqualFold(t.Text, "false"):
		v.Kind = booleanValue
	default:
		v.Kind = wordValue
	}
	return v
}

func (p *parser) value() (literalValue, bool) {
	for p.is("+") {
		p.report(p.take().Span, diagPlus)
	}
	t := p.peek()
	if p.is("-") {
		next := p.ahead(1)
		v := classify(next)
		if (next.Kind == tokenWord || next.Kind == tokenNumber) && (v.Kind == numberValue || v.Kind == durationValue) && !strings.HasPrefix(next.Text, "-") {
			p.take()
			p.take()
			v.Text = "-" + v.Text
			v.At.Start = t.Span.Start
			return v, true
		}
		return literalValue{}, false
	}
	if t.Kind != tokenWord && t.Kind != tokenString && t.Kind != tokenNumber && !(t.Kind == tokenPunct && t.Text == "*") {
		return literalValue{}, false
	}
	p.take()
	p.checkQuoted(t)
	v := classify(t)
	if v.Kind == variableValue && strings.HasPrefix(v.Text, "$__") {
		p.report(v.At, diagVariable)
	}
	return v, true
}

func (p *parser) in(f fieldName, neg bool) filterExpr {
	p.take()
	p.expectNext(expect{Class: "value", Field: &f, Scope: "in"})
	x := &inExpr{Field: f, Negated: neg, At: span{f.At.Start, p.previousEnd()}}
	if p.peek().Kind == tokenWord && strings.HasPrefix(p.peek().Text, "$") {
		v, _ := p.value()
		x.Variable = strings.TrimPrefix(v.Text, "$")
		x.At.End = v.At.End
		p.node(x)
		return x
	}
	if !p.eat("(") {
		p.report(p.peek().Span, diagList)
		return nil
	}
	p.expectNext(expect{Class: "value", Field: &f, Scope: "in"})
	for p.peek().Kind != eof && !p.is(")") {
		v, ok := p.value()
		if !ok {
			p.report(p.peek().Span, diagListValue)
			p.skipRun()
			break
		}
		if p.bounded && len(x.Values) >= maxListItems {
			p.bound(v.At, diagListBound)
		}
		x.Values = append(x.Values, v)
		if p.completion {
			p.completionUsed[strings.ToLower(v.Text)] = true
		}
		if v.Kind == starValue {
			p.issue(v.At, diagnostic(diagWildcard), wildcardHint(v.Text))
		}
		if !p.eat(",") {
			break
		}
		p.expectNext(expect{Class: "value", Field: &f, Scope: "in"})
		if p.is(")") {
			p.report(p.peek().Span, diagListValue)
		}
	}
	if len(x.Values) == 0 {
		p.report(p.peek().Span, diagListValue)
	}
	if !p.eat(")") {
		p.report(p.peek().Span, diagList)
	}
	x.At.End = p.previousEnd()
	p.node(x)
	return x
}

func (p *parser) call() *filterCall {
	t := p.take()
	x := &filterCall{Name: strings.ToLower(t.Text), At: t.Span}
	open := p.take()
	if open.Span.Start != t.Span.End {
		p.report(span{t.Span.Start, open.Span.End}, diagCallSpace, x.Name)
	}
	p.enter(open.Span)
	for p.peek().Kind != eof && !p.is(")") && !p.is("|") {
		before := p.i
		arg := callArg{}
		switch t := p.peek(); {
		case p.filterCallStart():
			arg.Call = p.call()
		case t.Kind == tokenString || t.Kind == tokenNumber || t.Kind == tokenWord && numberPattern.MatchString(t.Text) || p.is("-"):
			v, ok := p.value()
			if ok {
				arg.Value = &v
			} else {
				p.report(t.Span, diagCallArg, x.Name)
				p.take()
			}
		case t.Kind == tokenWord:
			f, ok := p.field()
			if ok {
				arg.Field = &f
			}
		default:
			p.report(t.Span, diagCallArg, x.Name)
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
		p.report(open.Span, diagCallClose)
	}
	p.leave()
	if isComparison(p.lower()) {
		op := p.take()
		v, ok := p.value()
		if ok {
			x.Cmp = &callComparison{Op: op.Text, Value: v}
		} else {
			p.report(op.Span, diagValue, x.Name, op.Text, x.Name, op.Text, "1000")
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

func (p *parser) checkQuoted(t lexToken) {
	if !t.Unclosed {
		return
	}
	if t.Kind == tokenRaw {
		p.report(t.Span, diagRaw)
	} else {
		p.report(t.Span, diagQuote, t.Quote)
	}
}

func (p *parser) colonProblem(f fieldName, v literalValue, at span) {
	// The field, operator and value may be separated by whitespace, or the
	// quoted field/value may contain it. Recover through the offending colon's
	// run rather than stopping before the value whose diagnostic we render.
	run := span{at.Start, p.runSpan(p.peek().Span.Start).End}
	value := p.text(span{v.At.Start, run.End})
	prefix := p.text(span{f.At.Start, v.At.Start})
	after := v.Text
	if strings.HasPrefix(value, "//") {
		value = p.text(run)
		prefix = ""
		after = f.Key
	}
	p.issue(run, diagnostic(diagColon, after), quoteValueHint(prefix, value))
	p.skipRun()
}

func (p *parser) wildcard(v literalValue) {
	at := p.runSpan(v.At.Start)
	p.issue(at, diagnostic(diagWildcard), wildcardHint(p.text(at)))
	p.skipRun()
}

func (p *parser) runSpan(start int) span {
	end := start
	for end < len(p.runes) && !unicode.IsSpace(p.runes[end]) {
		end++
	}
	return span{start, end}
}

// Recovery skips a malformed run, respecting delimiters and whitespace at the
// term's depth. Clause words and pipes remain available to the outer parser.
func (p *parser) skipRun() {
	if p.peek().Kind == eof {
		return
	}
	first := p.take()
	end := first.Span.End
	depth := 0
	if first.Kind == tokenPunct && first.Text == "(" {
		depth = 1
	}
	for p.peek().Kind != eof {
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
		p.report(t.Span, diagSelector)
	case "@":
		word := p.peek()
		p.report(span{t.Span.Start, word.Span.End}, diagAt, "@"+word.Text, word.Text)
	case "!":
		at := p.runSpan(t.Span.Start)
		p.report(at, diagBang, strings.TrimPrefix(p.text(at), "!"))
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
		p.report(t.Span, diagTime, word)
		p.take()
		if p.peek().Kind == tokenWord {
			p.take()
		}
		return true
	}
	if (word == "ago" || word == "now") && grammarText(p.ahead(1)) == "(" {
		p.report(t.Span, diagTimeCall, word)
		p.skipRun()
		return true
	}
	if word == "earliest" && grammarText(p.ahead(1)) == "=" || word == "_time" && grammarText(p.ahead(1)) == ":" {
		p.report(t.Span, diagTime, word)
		p.skipRun()
		return true
	}
	if word == "facet" || (word == "order" || word == "group") && grammarText(p.ahead(1)) == "by" {
		p.report(t.Span, diagForeignClause, word)
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
	if t.Kind == tokenBad {
		p.badTerm()
		return
	}
	if p.eat(")") {
		p.report(t.Span, diagUnmatched)
		return
	}
	if isComparison(t.Text) || t.Text == ":" || t.Text == "~" || t.Text == "!~" {
		p.report(t.Span, diagFieldBefore, t.Text)
		p.skipRun()
		return
	}
	if p.eat("+") {
		p.report(t.Span, diagPlus)
		return
	}
	if p.is("*") {
		p.wildcard(literalValue{Text: t.Text, At: t.Span})
		return
	}
	if !p.foreignTerm() {
		p.report(t.Span, diagUnexpected, t.Text)
		p.take()
	}
}

func (p *parser) skipStage() {
	for p.peek().Kind != eof && !p.is("sort") && !p.is("limit") && !p.is("|") {
		p.take()
	}
}

func (p *parser) parseSort() *sortClause {
	start := p.take()
	p.expectNext(expect{Class: "sort_key"})
	x := &sortClause{At: start.Span}
	signed := p.is("+") || p.is("-")
	if signed {
		x.Ascending = p.take().Text == "+"
	}
	p.expectNext(expect{Class: "sort_key"})
	t := p.peek()
	if t.Kind != tokenWord && t.Kind != tokenIdent {
		p.report(t.Span, diagSortKey)
		return x
	}
	x.Key = strings.ToLower(p.take().Text)
	if p.is("asc") || p.is("desc") {
		if signed {
			p.report(p.peek().Span, diagDirection)
		}
		x.Ascending = !strings.EqualFold(p.take().Text, "desc")
	}
	x.At.End = p.previousEnd()
	return x
}

func (p *parser) parseLimit() *limitClause {
	start := p.take()
	p.expectNext(expect{Class: "integer"})
	t := p.peek()
	x := &limitClause{At: start.Span}
	n, err := strconv.Atoi(t.Text)
	if t.Kind != tokenWord || err != nil || n < 1 || n > 1000 || strings.IndexFunc(t.Text, func(r rune) bool { return !digit(r) }) >= 0 {
		p.report(t.Span, diagLimit)
		if t.Kind != eof {
			p.take()
		}
	} else {
		x.N = n
		p.take()
	}
	x.At.End = p.previousEnd()
	return x
}

func (p *parser) stage(start int) *aggregation {
	s := &aggregation{At: span{Start: start}}
	if p.completion {
		p.completionQuery.Stage = s
	}
	p.expectNext(expect{Class: "measure"})
	for p.peek().Kind != eof && !p.is("|") && !clauses[p.lower()] {
		before := p.i
		if p.peek().Kind == tokenIdent && foreignStages[p.lower()] {
			t := p.take()
			p.report(t.Span, diagForeignStage, strings.ToLower(t.Text))
			for p.peek().Kind != eof && !p.is("|") && !p.is(",") && !clauses[p.lower()] && !foreignStages[p.lower()] && grammarText(p.ahead(1)) != "(" {
				p.take()
			}
			continue
		}
		if p.peek().Kind == tokenIdent && foreignCalls[p.lower()] && grammarText(p.ahead(1)) == "(" {
			p.report(p.peek().Span, diagForeignCall, p.lower())
			p.skipRun()
			continue
		}
		expr := p.mexpr(0)
		if expr != nil {
			m := measureNode{Expr: expr, At: expr.Span()}
			if modifiers[p.lower()] && p.eat("as") {
				t := p.peek()
				if t.Kind == tokenIdent {
					m.Alias = p.take().Text
					m.At.End = t.Span.End
				} else {
					p.report(t.Span, diagAlias)
				}
			}
			leaves, share, distribution := measureLeaves(expr)
			if leaves == 0 {
				p.report(expr.Span(), diagAggregate)
			}
			_, call := expr.(*mCall)
			_, countIf := expr.(*mCountIf)
			if distribution && !call {
				p.report(expr.Span(), diagHistogram)
			}
			if !call && !countIf {
				if m.Alias == "" {
					p.report(expr.Span(), diagMeasureName)
				}
				if share {
					p.report(expr.Span(), diagShare)
				}
			}
			if call, ok := expr.(*mCall); ok && call.Name == "histogram" {
				if s.Histogram != nil || len(s.Measures) > 0 {
					p.report(call.At, diagHistogram)
				}
				if m.Alias != "" {
					p.report(m.At, diagHistogramAlias)
				}
				if len(call.Args) != 1 || call.Args[0].Field == nil {
					p.report(call.At, diagHistogramField)
				} else {
					s.Histogram = call.Args[0].Field
				}
			} else {
				if s.Histogram != nil {
					p.report(m.At, diagHistogram)
				}
				s.Measures = append(s.Measures, m)
			}
		}
		if p.eat(",") {
			p.expectNext(expect{Class: "measure"})
			if p.peek().Kind == eof || clauses[p.lower()] {
				p.report(p.peek().Span, diagMeasure)
			}
			continue
		}
		if expr != nil && p.peek().Kind != eof && !p.is("|") && !clauses[p.lower()] {
			p.report(p.peek().Span, diagMeasureComma)
		}
		if p.i == before {
			p.unexpected()
		}
	}
	p.expectNext(expect{Class: "clause", Scope: "measure"})
	for p.is("by") || p.is("every") {
		if p.eat("by") {
			if len(s.By) > 0 {
				p.report(p.tokens[p.i-1].Span, diagDuplicate, "by")
			}
			if s.Bucket != "" {
				p.report(p.tokens[p.i-1].Span, diagOrder, "by")
			}
			if len(s.Measures) == 0 && s.Histogram == nil {
				p.report(p.tokens[p.i-1].Span, diagByFirst)
			}
			for {
				p.expectNext(expect{Class: "field", Func: "by", Arg: len(s.By)})
				f, ok := p.field()
				if !ok {
					if p.peek().Kind != eof && !clauses[p.lower()] {
						p.take()
					}
					break
				}
				s.By = append(s.By, f)
				if len(s.By) == 4 {
					p.report(f.At, diagByMax)
				}
				if s.Histogram != nil && len(s.By) == 2 {
					p.report(f.At, diagHistogramBy)
				}
				if !p.eat(",") {
					break
				}
			}
		} else {
			at := p.take().Span
			p.expectNext(expect{Class: "bucket"})
			if len(s.Measures) == 0 && s.Histogram == nil {
				p.report(at, diagEveryFirst)
			}
			if s.Bucket != "" {
				p.report(at, diagDuplicate, "every")
			}
			t := p.peek()
			s.Bucket = strings.ToLower(t.Text)
			if !slices.Contains(p.vocab.buckets(), s.Bucket) {
				p.report(t.Span, diagBucket, t.Text)
			}
			if t.Kind != eof {
				p.take()
			}
		}
	}
	p.expectNext(expect{Class: "clause", Scope: "stage"})
	if len(s.Measures) == 0 && s.Histogram == nil && len(p.problems) == 0 {
		p.report(p.peek().Span, diagMeasure)
	}
	s.At.End = p.previousEnd()
	return s
}

func measurePrecedence(t lexToken) int {
	if t.Kind != tokenPunct {
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
	return t.Kind == tokenNumber || t.Kind == tokenIdent && t.Text != "as" || p.is("(") || p.is("-")
}

func (p *parser) mexpr(minPrec int) mExpr {
	left := p.mfactor()
	if left == nil {
		return nil
	}
	for prec := measurePrecedence(p.peek()); prec >= minPrec; prec = measurePrecedence(p.peek()) {
		op := p.take()
		if !p.canMFactor() {
			p.report(op.Span, diagMeasureSides, op.Text)
			return left
		}
		right := p.mexpr(prec + 1)
		if right == nil {
			return left
		}
		x := &mBinary{Op: op.Text[0], L: left, R: right, At: span{left.Span().Start, right.Span().End}}
		p.node(x, left, right)
		left = x
	}
	return left
}

func (p *parser) mfactor() mExpr {
	p.expectNext(expect{Class: "measure"})
	t := p.peek()
	if p.eat("-") {
		p.enter(t.Span)
		x := p.mfactor()
		p.leave()
		if x == nil {
			return nil
		}
		n := &mNeg{X: x, At: span{t.Span.Start, x.Span().End}}
		p.node(n, x)
		return n
	}
	if p.eat("(") {
		p.enter(t.Span)
		x := p.mexpr(0)
		p.leave()
		if !p.eat(")") {
			p.report(t.Span, diagUnclosedGroup)
		} else if x != nil {
			setMeasureSpan(x, span{t.Span.Start, p.previousEnd()})
		}
		return x
	}
	if t.Kind == tokenNumber {
		p.take()
		v, err := strconv.ParseFloat(t.Text, 64)
		if err != nil {
			p.report(t.Span, diagMeasure)
			return nil
		}
		x := &mConst{Value: v, Text: t.Text, At: t.Span}
		p.node(x)
		return x
	}
	if t.Kind == tokenIdent && grammarText(p.ahead(1)) == "(" {
		return p.mcall()
	}
	if t.Kind == tokenBad {
		p.badTerm()
		return nil
	}
	p.report(t.Span, diagMeasure)
	if t.Kind != eof {
		p.take()
	}
	return nil
}

func (p *parser) mcall() mExpr {
	name := p.take()
	open := p.take()
	at := span{Start: name.Span.Start}
	p.enter(open.Span)
	if strings.EqualFold(name.Text, "count_if") {
		filter := p.conjunction(p.filters(true))
		if filter == nil {
			p.report(open.Span, diagSides, "count_if")
		}
		if !p.eat(")") {
			p.report(open.Span, diagCallClose)
		}
		p.leave()
		at.End = p.previousEnd()
		x := &mCountIf{Filter: filter, At: at}
		p.node(x, filter)
		return x
	}
	x := &mCall{Name: strings.ToLower(name.Text), At: at}
	p.expectNext(expect{Class: "marg", Func: x.Name, Arg: 0})
	for p.peek().Kind != eof && !p.is(")") && !p.is("|") {
		t := p.peek()
		arg := mArg{At: t.Span}
		switch {
		case p.eat("*"):
			arg.Star = true
		case t.Kind == tokenNumber || p.is("-"):
			v, ok := p.value()
			if !ok || v.Kind != numberValue {
				p.report(t.Span, diagMeasureArg)
				p.take()
			} else {
				n, err := strconv.ParseFloat(v.Text, 64)
				if err != nil {
					p.report(v.At, diagMeasureArg)
				} else {
					arg.Number = &n
				}
				arg.At = v.At
			}
		case t.Kind == tokenIdent || t.Kind == tokenPath:
			f, ok := p.field()
			if ok {
				arg.Field = &f
				arg.At = f.At
			}
		default:
			p.report(t.Span, diagMeasureArg)
			p.take()
		}
		x.Args = append(x.Args, arg)
		if !p.eat(",") {
			break
		}
		p.expectNext(expect{Class: "marg", Func: x.Name, Arg: len(x.Args)})
	}
	if !p.eat(")") {
		p.report(open.Span, diagCallClose)
	}
	p.leave()
	x.At.End = p.previousEnd()
	for _, arg := range x.Args {
		if arg.Star && (x.Name != "count" || len(x.Args) != 1) {
			p.report(arg.At, diagStarArg)
		}
	}
	p.node(x)
	return x
}

func measureLeaves(x mExpr) (int, bool, bool) {
	switch x := x.(type) {
	case *mCall:
		return 1, x.Name == "share", x.Name == "histogram"
	case *mCountIf:
		return 1, false, false
	case *mBinary:
		l, ls, lh := measureLeaves(x.L)
		r, rs, rh := measureLeaves(x.R)
		return l + r, ls || rs, lh || rh
	case *mNeg:
		return measureLeaves(x.X)
	}
	return 0, false, false
}
