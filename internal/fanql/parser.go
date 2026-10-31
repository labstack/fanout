package fanql

import (
	"regexp"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode"

	"github.com/labstack/fanout/internal/query"
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
	clauseID        int
	reported        map[int]bool
	structural      map[int]int
	measureInfo     map[query.MeasureExpr]*measureSource
	vocab           vocabulary
	tokens          lexTokens
	runes           []rune
	i               int
	problems        query.Problems
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

// Fields rank before clause words: a term start more often begins a filter
// than it ends the filter list.
func termAlternatives(nested bool) []expect {
	alternatives := []expect{{Class: "field"}, {Class: "free_text"}, {Class: "operator", Scope: "term"}, {Class: "variable"}}
	if !nested {
		alternatives = append(alternatives, expect{Class: "clause"})
	}
	return alternatives
}

func (p *parser) expectTerm(nested bool) {
	alternatives := termAlternatives(nested)
	if !nested && p.i == 1 && p.completionQuery != nil && !p.completionQuery.WhereWord {
		// Directly after the source, where and the other clause words lead.
		alternatives = append(alternatives[len(alternatives)-1:], alternatives[:len(alternatives)-1]...)
	}
	p.expectNext(alternatives...)
}

// A private sentinel unwinds only a bound failure, before further recursion or
// allocation. Other panics remain visible to callers and tests.
type parseBound struct{}

// parseRecovered also returns the recovered tree beside syntax problems, so the
// resolver can report its own problems in the same pass. A bound failure
// returns no tree.
func parseRecovered(text string, source string, vocab vocabulary) (q *queryTree, problems query.Problems) {
	p := &parser{vocab: vocab, runes: []rune(text), bounded: true, heights: make(map[any]int)}
	defer func() {
		if v := recover(); v != nil {
			if _, ok := v.(parseBound); !ok {
				panic(v)
			}
			q = nil
		}
		problems = p.finish()
	}()
	if len(p.runes) > maxTextRunes || len(text) > maxTextBytes {
		p.bound(span{0, len(p.runes)}, diagTextBound)
	}
	p.tokens = lex(text)
	for _, token := range p.tokens {
		if token.Kind == tokenBad && strings.HasPrefix(token.Text, "$__") {
			p.report(token.Span, diagVariable)
			return nil, nil
		}
	}
	if len(p.tokens)-1 > maxTokens {
		p.bound(p.tokens[maxTokens].Span, diagTokensBound)
	}
	q = p.query(source)
	return q, nil
}

// parseEntry is the unbounded internal entry parser used by the lift gate.
func parseEntry(text string) (filterExpr, query.Problems) {
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
	if p.reported == nil {
		p.reported = map[int]bool{}
	}
	if p.reported[p.clauseID] {
		return
	}
	p.reported[p.clauseID] = true
	p.problems = append(p.problems, query.Problem{Path: "text", Message: message, Hint: hint, Span: &query.TextSpan{Start: at.Start, End: at.End}})
}
func (p *parser) fix(at span, message, hint string, fixes ...query.FixIt) {
	before := len(p.problems)
	p.issue(at, message, hint)
	if len(p.problems) > before {
		p.problems[len(p.problems)-1].FixIts = fixes
	}
}
func (p *parser) report(at span, code diagnosticCode, args ...any) {
	p.issue(at, diagnostic(code, args...), hint(code))
}
func (p *parser) bound(at span, code diagnosticCode) {
	p.problems = nil
	p.reported = nil
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
func (p *parser) finish() query.Problems {
	sort.SliceStable(p.problems, func(i, j int) bool { return p.problems[i].Span.Start < p.problems[j].Span.Start })
	if len(p.problems) > maxProblems {
		at := *p.problems[maxProblems].Span
		p.problems = p.problems[:maxProblems]
		p.problems = append(p.problems, query.Problem{Path: "text", Message: diagnostic(diagMore), Span: &at})
	}
	return p.problems
}

func (p *parser) beginClause() { p.clauseID++ }

// Local recovery suppresses cascades only until the next independent term.
// Enclosing grouping repairs are suppressed separately to preserve the first repair.
func (p *parser) beginIndependentTerm() {
	delete(p.reported, p.clauseID)
}

func (p *parser) query(source string) *queryTree {
	p.beginClause()
	q := &queryTree{Span: span{0, len(p.runes)}}
	p.completionQuery = q
	if source != "" {
		q.Source = strings.ToLower(source)
		if !sources[q.Source] {
			p.report(span{}, diagSource, source)
		}
		p.beginClause()
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
			// The source after a foreign lead word is still the source, so the
			// rest of the query parses without a cascade.
			if next := p.peek(); next.Kind == tokenWord && sources[strings.ToLower(next.Text)] && next.Span.Start > first.Span.End {
				source := strings.ToLower(p.take().Text)
				at := span{first.Span.Start, next.Span.End}
				p.fix(at, diagnostic(diagFrom, q.Source, source), "", query.FixIt{Label: source, Span: query.TextSpan{Start: at.Start, End: at.End}, Text: source})
				q.Source, q.SourceSpan = source, next.Span
				break
			}
			p.report(first.Span, diagFrom, q.Source, "spans")
		case "select", "{":
			code := diagSQL
			if q.Source == "{" {
				code = diagSelector
			}
			p.report(first.Span, code)
			for p.peek().Kind != eof {
				p.take()
			}
			return q
		default:
			suggestion := hint(diagSource)
			if q.Source == "traces" {
				suggestion = hint(diagTraces)
			}
			p.issue(first.Span, diagnostic(diagSource, first.Text), suggestion)
		}
	}
	p.beginClause()
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
			p.beginClause()
			if p.is("=") || p.is("~") {
				op := p.take()
				p.report(span{pipe.Span.Start, op.Span.End}, diagPipeOp, "|"+op.Text)
				p.skipRun()
				continue
			}
			if p.is("sort") && grammarText(p.ahead(1)) == "by" || q.Stage != nil && (p.is("head") || p.is("take") || (p.is("order") || p.is("group")) && grammarText(p.ahead(1)) == "by") {
				p.foreignStage(pipe.Span.Start)
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
			p.beginClause()
			p.report(p.take().Span, diagWhereTwice)
			q.Where = append(q.Where, p.filters(false)...)
		case p.is("sort"):
			p.beginClause()
			orderAt := p.peek().Span
			outOfOrder := q.Limit != nil
			if q.Sort != nil {
				p.report(p.peek().Span, diagDuplicate, "sort")
			}
			q.Sort = p.parseSort()
			if outOfOrder {
				key := q.Sort.Key
				if q.Sort.Ascending {
					key = "+" + key
				}
				p.issue(orderAt, diagnostic(diagLimitBeforeSort, key, q.Limit.N), "write "+print(q))
			}
		case p.is("limit"):
			p.beginClause()
			if q.Limit != nil {
				p.report(p.peek().Span, diagDuplicate, "limit")
			}
			q.Limit = p.parseLimit()
		case p.is("by"), p.is("every"):
			p.beginClause()
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
	groupingReports := p.structural[p.clauseID]
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
				p.beginIndependentTerm()
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

		if p.peek().Span.Start > p.previousEnd() && (p.peek().Kind == tokenWord || p.peek().Kind == tokenString || p.peek().Kind == tokenRaw) {
			p.beginIndependentTerm()
		}
		x := p.term(nested)
		if x != nil {
			terms = append(terms, x)
			if p.completion && !nested {
				p.completionQuery.Where = terms
			}
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
	if !p.completion && p.structural[p.clauseID] == groupingReports && mixed >= 0 && len(terms) >= 3 && len(ops) == len(terms)-1 {
		at := span{terms[0].Span().Start, terms[len(terms)-1].Span().End}
		first := p.groupingEdit(terms, ops, ops[mixed])
		second := p.groupingEdit(terms, ops, ops[0])
		before := len(p.problems)
		p.beginIndependentTerm()
		p.issue(at, diagnostic(diagMixed), groupingHint(first, second))
		if p.structural == nil {
			p.structural = map[int]int{}
		}
		p.structural[p.clauseID]++
		span := query.TextSpan{Start: at.Start, End: at.End}
		if len(p.problems) > before {
			p.problems[len(p.problems)-1].FixIts = []query.FixIt{{Label: first, Span: span, Text: first}, {Label: second, Span: span, Text: second}}
		}
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
		// A negation needs a term; clause words cannot follow it.
		p.expectNext(termAlternatives(true)...)
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
	if p.completion && p.peek().Kind == eof && p.peek().Span.Start > field.At.End && !p.knownField(field) {
		// After a word that names no column and a space, the word is most
		// likely finished free text: the next term ranks before an operator.
		p.expectNext(append(termAlternatives(nested), expect{Class: "operator", Field: &field})...)
	}
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
		// Boolean operators and a whitespace-separated next filter belong
		// to recovery, not to this filter's value.
		next := grammarText(p.ahead(1))
		nextFilter := p.peek().Kind == tokenWord && !numberPattern.MatchString(p.peek().Text) && !durationPattern.MatchString(p.peek().Text) && p.peek().Span.Start > p.previousEnd() &&
			(next == ":" || isComparison(next) || next == "~" || next == "!~" || next == "is" || isFieldWordOperator(next))
		boundary := p.is("and") || p.is("or") || p.is("not") || nextFilter
		var v literalValue
		valid := false
		if !boundary {
			v, valid = p.value()
		}
		if !valid {
			if bad := p.peek(); bad.Kind == tokenBad && bad.Text == "$" {
				p.issue(bad.Span, bad.Message, bad.Hint)
				p.take()
				p.skipRun()
				return nil
			}
			example := fieldExample(field, op)
			typed := p.text(field.At)
			p.report(span{field.At.Start, oper.Span.End}, diagValue, typed, op, typed, op, example)
			if !boundary && !p.filterStop(nested) {
				p.skipRun()
			}
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
	if field.Bracket {
		// A bracket key is unmistakably a field; it is never searched as text.
		typed := p.text(field.At)
		p.report(field.At, diagBracketOperator, typed, typed, typed)
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
	if v := classify(t); v.Kind == variableValue && p.completionQuery != nil {
		// A variable alone searches the free-text column for its value, so it is
		// checked against the declarations like any other variable reference.
		column := map[string]string{"spans": "operation", "logs": "body"}[p.completionQuery.Source]
		if column != "" {
			x := &comparison{Field: fieldName{Key: column, Single: true, At: span{t.Span.Start, t.Span.Start}}, Op: "~", Value: v, At: t.Span}
			p.node(x)
			return x
		}
	}
	x := &textExpr{Value: literalValue{Kind: wordValue, Text: p.text(field.At), At: field.At}, At: field.At}
	p.node(x)
	return x
}

// knownField reports a word completion should treat as a field: a column of
// the source, or an attribute path.
func (p *parser) knownField(f fieldName) bool {
	if !f.Single || f.Scope != "" || f.Bracket || p.completionQuery == nil {
		return true
	}
	if strings.EqualFold(f.Key, "duration") {
		return true
	}
	columns, _ := p.vocab.columns(p.completionQuery.Source)
	return slices.ContainsFunc(columns, func(c query.Field) bool { return strings.EqualFold(c.Name, f.Key) })
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
		p.report(p.peek().Span, diagList, printField(f), listExamples(f))
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
		p.report(p.peek().Span, diagList, printField(f), listExamples(f))
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
	case "~~":
		suggestion := t.Hint
		if p.i >= 2 && p.tokens[p.i-2].Kind == tokenWord && (p.peek().Kind == tokenWord || p.peek().Kind == tokenString) {
			field, value := p.tokens[p.i-2].Text, p.peek().Text
			suggestion = "write " + field + " like " + quoteText(value+"%") + " for a pattern, or " + field + " ~ " + quoteText(value) + " for contains"
		}
		p.issue(t.Span, t.Message, suggestion)
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
		p.report(t.Span, diagTime)
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
		p.report(t.Span, diagTime)
		p.skipRun()
		return true
	}
	if word == "facet" || (word == "order" || word == "group") && grammarText(p.ahead(1)) == "by" {
		p.foreignStage(t.Span.Start)
		return true
	}
	return false
}

// foreignStage reports a foreign command at the current token, naming the
// FanQL clause it maps to, and consumes the command with its simple argument.
// start is where a fix-it replacement begins, including a preceding |.
func (p *parser) foreignStage(start int) {
	t := p.take()
	word := strings.ToLower(t.Text)
	end := t.Span.End
	if (word == "order" || word == "sort" || word == "group") && p.is("by") {
		end = p.take().Span.End
		word += " by"
	}
	fixAt := query.TextSpan{Start: start, End: end}
	at := span{t.Span.Start, end}
	switch word {
	case "order by", "sort by":
		key := "count"
		if k := p.peek(); k.Kind == tokenWord || k.Kind == tokenIdent {
			key = p.take().Text
			for p.is("asc") || p.is("desc") {
				p.take()
			}
		}
		p.fix(at, diagnostic(diagForeignSort, word, key), "", query.FixIt{Label: "sort", Span: fixAt, Text: "sort"})
	case "head", "take":
		if n := p.peek(); n.Kind == tokenNumber || n.Kind == tokenWord && strings.IndexFunc(n.Text, func(r rune) bool { return !digit(r) }) < 0 {
			p.take()
			p.fix(at, diagnostic(diagForeignLimit, word, n.Text), "", query.FixIt{Label: "limit", Span: fixAt, Text: "limit"})
			return
		}
		p.issue(at, diagnostic(diagForeignLimit, word, "10"), "")
	case "group by", "facet":
		p.report(at, diagForeignClause, word)
		if word == "facet" {
			p.skipRun()
		}
	default:
		p.report(at, diagForeignStage, word)
	}
}

var foreignPercentile = regexp.MustCompile(`(?i)^(percentile|pct|perc([0-9]{1,2})|median|mean|dc|distinct_count|cardinality)\(\s*([^\s,()]+)\s*(?:,\s*([0-9.]+)\s*)?\)$`)

// foreignCall reports a foreign aggregate call, suggesting the FanQL measure
// when the call has a direct equivalent: percentile(duration, 95) is p95(duration).
func (p *parser) foreignCall() {
	name := p.peek()
	word := p.lower()
	p.skipRun()
	at := span{name.Span.Start, p.previousEnd()}
	suggestion := ""
	if m := foreignPercentile.FindStringSubmatch(p.text(at)); m != nil {
		field := m[3]
		switch fn := strings.ToLower(m[1]); {
		case fn == "median":
			suggestion = "p50(" + field + ")"
		case fn == "mean":
			suggestion = "avg(" + field + ")"
		case fn == "dc" || fn == "distinct_count" || fn == "cardinality":
			suggestion = "count_distinct(" + field + ")"
		default:
			percent := m[2]
			if percent == "" {
				percent = m[4]
			}
			if n, err := strconv.ParseFloat(percent, 64); err == nil && n > 0 && n < 100 {
				suggestion = "quantile(" + field + ", " + strconv.FormatFloat(n/100, 'f', -1, 64) + ")"
				for _, q := range []string{"50", "75", "90", "95", "99"} {
					if percent == q {
						suggestion = "p" + q + "(" + field + ")"
					}
				}
			}
		}
	}
	if suggestion == "" {
		p.report(name.Span, diagForeignCall, word)
		return
	}
	p.fix(at, diagnostic(diagForeignMeasure, word, suggestion), "", query.FixIt{Label: suggestion, Span: query.TextSpan{Start: at.Start, End: at.End}, Text: suggestion})
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
	directions := 0
	if signed {
		directions = 1
	}
	reported := false
	for p.is("asc") || p.is("desc") {
		if directions > 0 && !reported {
			p.report(p.peek().Span, diagDirection)
			reported = true
		}
		x.Ascending = !strings.EqualFold(p.take().Text, "desc")
		directions++
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
	p.beginClause()
	p.measureInfo = map[query.MeasureExpr]*measureSource{}
	s := &aggregation{At: span{Start: start}}
	if p.completion {
		p.completionQuery.Stage = s
	}
	p.expectNext(expect{Class: "measure"})
	for p.peek().Kind != eof && !p.is("|") && !clauses[p.lower()] {
		before := p.i
		if p.peek().Kind == tokenIdent && foreignStages[p.lower()] {
			stageStart := p.peek().Span.Start
			if len(s.Measures) == 0 && s.Histogram == nil {
				stageStart = start
			}
			p.foreignStage(stageStart)
			for p.peek().Kind != eof && !p.is("|") && !p.is(",") && !clauses[p.lower()] && !foreignStages[p.lower()] && grammarText(p.ahead(1)) != "(" {
				p.take()
			}
			continue
		}
		if p.peek().Kind == tokenIdent && foreignCalls[p.lower()] && grammarText(p.ahead(1)) == "(" {
			p.foreignCall()
			continue
		}
		expr := p.measureExpression(0)
		if expr != nil {
			m := measureNode{Parsed: query.Measure{Expr: expr}, At: p.measureSpan(expr), Info: p.measureInfo, Filters: map[string]filterExpr{}}
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
				p.report(p.measureSpan(expr), diagAggregate)
			}
			_, call := expr.(query.MeasureCall)
			_, countIf := expr.(*query.MeasureCountIf)
			if distribution && !call {
				p.report(p.measureSpan(expr), diagHistogram)
			}
			if !call && !countIf {
				if m.Alias == "" {
					p.report(p.measureSpan(expr), diagMeasureName)
				}
				if share {
					p.report(p.measureSpan(expr), diagShare)
				}
			}
			if call, ok := expr.(query.MeasureCall); ok && call.Func == "histogram" {
				if s.Histogram != nil || len(s.Measures) > 0 {
					p.report(p.measureSpan(call), diagHistogram)
				}
				if m.Alias != "" {
					p.report(m.At, diagHistogramAlias)
				}
				if len(p.measureInfo[call].args) != 1 || p.measureInfo[call].args[0].Field == nil {
					p.report(p.measureSpan(call), diagHistogramField)
				} else {
					s.Histogram = p.measureInfo[call].args[0].Field
				}
			} else {
				if s.Histogram != nil {
					p.report(m.At, diagHistogram)
				}
				s.Measures = append(s.Measures, m)
			}
		}
		if p.eat(",") {
			p.beginIndependentTerm()
			p.expectNext(expect{Class: "measure"})
			if p.peek().Kind == eof || clauses[p.lower()] {
				p.report(p.peek().Span, diagMeasure)
			}
			continue
		}
		if expr != nil && p.peek().Kind != eof && !p.is("|") && !clauses[p.lower()] && !foreignStages[p.lower()] {
			p.report(p.peek().Span, diagMeasureComma)
		}
		if p.i == before {
			p.unexpected()
		}
	}
	p.expectNext(expect{Class: "clause", Scope: "measure"})
	for p.is("by") || p.is("every") {
		if p.eat("by") {
			p.beginClause()
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
			p.beginClause()
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

func (p *parser) measureSpan(e query.MeasureExpr) span { return p.measureInfo[e].at }
func (p *parser) measureExpression(minPrec int) query.MeasureExpr {
	g := query.MeasureGrammar{
		Peek: func() string { return grammarText(p.peek()) }, Take: func() { p.take() }, Position: func() int { return p.peek().Span.Start },
		CanAtom: func() bool {
			t := p.peek()
			return t.Kind == tokenNumber || t.Kind == tokenBad || t.Kind == tokenIdent && t.Text != "as"
		},
		Atom: func() query.MeasureExpr {
			p.expectNext(expect{Class: "measure"})
			t := p.peek()
			if t.Kind == tokenNumber {
				p.take()
				v, err := strconv.ParseFloat(t.Text, 64)
				if err != nil {
					p.report(t.Span, diagMeasure)
					return nil
				}
				x := query.MeasureConst{Value: v, Text: t.Text}
				p.recordMeasure(x, t.Span)
				return x
			}
			if t.Kind == tokenIdent && grammarText(p.ahead(1)) == "(" {
				return p.measureCall()
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
		},
		Node: func(e query.MeasureExpr, start int, children ...query.MeasureExpr) query.MeasureExpr {
			p.recordMeasure(e, span{start, p.previousEnd()}, children...)
			return e
		},
		Group: func(e query.MeasureExpr, start int) { info := p.measureInfo[e]; info.at = span{start, p.previousEnd()} },
		Enter: func(start int) { p.enter(span{start, start + 1}) }, Leave: p.leave,
		Depth: func(int) bool { return true }, OK: func() bool { return true },
		Fail: func(code query.MeasureSyntaxCode, op string, start int) {
			at := span{start, start + len(op)}
			switch code {
			case query.MeasureMissingOperand:
				p.report(at, diagMeasureSides, op)
			case query.MeasureMissingClose:
				p.report(span{start, start + 1}, diagUnclosedGroup)
			default:
				p.report(p.peek().Span, diagMeasure)
				if p.peek().Kind != eof {
					p.take()
				}
			}
		},
	}
	e, _ := query.ReadMeasureExpression(g, minPrec, 0)
	return e
}
func (p *parser) recordMeasure(e query.MeasureExpr, at span, children ...query.MeasureExpr) {
	if p.measureInfo == nil {
		p.measureInfo = map[query.MeasureExpr]*measureSource{}
	}
	p.measureInfo[e] = &measureSource{at: at}
	p.nodes++
	height := 0
	for _, child := range children {
		height = max(height, p.heights[child]+1)
	}
	p.heights[e] = height
	if p.bounded && p.nodes > maxNodes {
		p.bound(at, diagNodesBound)
	}
	if p.bounded && height+p.depth > maxDepth {
		p.bound(at, diagDepthBound)
	}
}

func (p *parser) measureCall() query.MeasureExpr {
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
		x := &query.MeasureCountIf{}
		p.recordMeasure(x, at)
		if filter != nil {
			p.heights[x] = p.heights[filter] + 1
			if p.bounded && p.heights[x]+p.depth > maxDepth {
				p.bound(at, diagDepthBound)
			}
		}
		p.measureInfo[x].filter = filter
		return x
	}
	x := query.MeasureCall{Func: strings.ToLower(name.Text), Field: &query.FieldRef{}}
	var args []measureArgument
	p.expectNext(expect{Class: "marg", Func: x.Func, Arg: 0})
	for p.peek().Kind != eof && !p.is(")") && !p.is("|") {
		t := p.peek()
		arg := measureArgument{At: t.Span}
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
				if p.is("-") {
					op := p.take()
					key := f.Key + "-"
					if p.peek().Kind == tokenIdent || p.peek().Kind == tokenPath {
						key += p.take().Text
					}
					p.report(op.Span, diagMeasureHyphen, x.Func, sqlString(key))
					for p.peek().Kind != eof && !p.is(")") && !p.is("|") {
						p.take()
					}
				} else if p.peek().Kind == tokenBad && p.peek().Text == "." {
					bad := p.take()
					p.issue(bad.Span, bad.Message, bad.Hint)
				}
			}
		default:
			p.report(t.Span, diagMeasureArg)
			p.take()
		}
		args = append(args, arg)
		if !p.eat(",") {
			break
		}
		p.expectNext(expect{Class: "marg", Func: x.Func, Arg: len(args)})
	}
	if !p.eat(")") {
		p.report(open.Span, diagCallClose)
	}
	p.leave()
	at.End = p.previousEnd()
	for _, arg := range args {
		if arg.Star && (x.Func != "count" || len(args) != 1) {
			p.report(arg.At, diagStarArg)
		}
	}
	p.recordMeasure(x, at)
	p.measureInfo[x].args = args
	return x
}

func measureLeaves(x query.MeasureExpr) (int, bool, bool) {
	switch x := x.(type) {
	case query.MeasureCall:
		return 1, x.Func == "share", x.Func == "histogram"
	case query.MeasureCountIf, *query.MeasureCountIf:
		return 1, false, false
	case query.MeasureBinary:
		l, ls, lh := measureLeaves(x.L)
		r, rs, rh := measureLeaves(x.R)
		return l + r, ls || rs, lh || rh
	case query.MeasureNeg:
		return measureLeaves(x.X)
	}
	return 0, false, false
}

func fieldExample(field fieldName, op string) string {
	if field.Scope != "" || field.Bracket {
		return "value"
	}
	name := strings.ToLower(field.Key)
	if vocabulary := query.FieldVocabularies()[name]; len(vocabulary.Order) > 0 {
		return vocabulary.Order[0]
	}
	switch name {
	case "service":
		return "api"
	case "severity":
		if isRange(op) {
			return "warn"
		}
		return "error"
	case "name":
		return "http.server.duration"
	case "duration":
		return "500ms"
	case "enabled":
		return "true"
	}
	var columns []query.Field
	for _, source := range query.SignalNames() {
		if cols, ok := query.SignalColumns(source); ok {
			columns = append(columns, cols...)
		}
	}
	for _, column := range columns {
		if column.Name == name {
			switch column.Type {
			case query.TypeNumber:
				return "1000"
			case query.TypeTime:
				return `"2026-10-29T10:00:00Z"`
			}
		}
	}
	return "value"
}

func listExamples(field fieldName) string {
	if field.Scope == "" && !field.Bracket {
		name := strings.ToLower(field.Key)
		if vocabulary := query.FieldVocabularies()[name]; len(vocabulary.Order) > 1 {
			return strings.Join(vocabulary.Order[:2], ", ")
		}
		switch name {
		case "service":
			return "api, web"
		case "severity":
			return "warn, error"
		case "name":
			return "http.server.duration, http.server.requests"
		case "duration":
			return "500ms, 1s"
		}
	}
	first := fieldExample(field, "in")
	if first == "1000" {
		return "1000, 2000"
	}
	if first == "true" {
		return "true, false"
	}
	return first + ", other"
}
