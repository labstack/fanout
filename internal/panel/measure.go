package panel

import (
	"fmt"
	"math"
	"regexp"
	"slices"
	"strconv"
	"strings"
)

// Measure is one parsed aggregate.
type Measure struct {
	Text     string
	Func     string
	Field    *FieldRef
	Q        float64
	Alias    string
	Unit     string
	Additive bool
	Expr     MeasureExpr
	Leaves   int
	Rank     RankRule
	nodes    int
}

type measureFunc struct {
	args     int
	numeric  bool
	additive bool
	unit     string // fixed unit; empty means the field's unit
	signals  []string
	quantile float64
}

var measureFuncs = map[string]measureFunc{
	"count":          {unit: "count", additive: true},
	"rate":           {unit: "per_second", additive: true},
	"error_rate":     {unit: "percent", signals: []string{"spans", "logs"}},
	"share":          {unit: "percent"},
	"avg":            {args: 1, numeric: true},
	"min":            {args: 1, numeric: true},
	"max":            {args: 1, numeric: true},
	"sum":            {args: 1, numeric: true, additive: true},
	"last":           {args: 1, numeric: true, signals: []string{"metrics"}},
	"p50":            {args: 1, numeric: true, quantile: 0.5},
	"p75":            {args: 1, numeric: true, quantile: 0.75},
	"p90":            {args: 1, numeric: true, quantile: 0.9},
	"p95":            {args: 1, numeric: true, quantile: 0.95},
	"p99":            {args: 1, numeric: true, quantile: 0.99},
	"quantile":       {args: 2, numeric: true},
	"count_distinct": {args: 1, unit: "count"},
}

func measureNames() []string {
	out := make([]string, 0, len(measureFuncs))
	for name := range measureFuncs {
		out = append(out, name)
	}
	slices.Sort(out)
	return out
}

var aliasPattern = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)

// MeasureExpr is a bounded aggregate expression. Only checked CountIf.Expr is
// compiled; its source is retained for diagnostics and direction inference.
type MeasureExpr interface{ measureExpr() }
type MeasureCall struct {
	Func  string
	Field *FieldRef
	Q     float64
}
type MeasureCountIf struct {
	Filter string
	Expr   string
}
type MeasureBinary struct {
	Op   byte
	L, R MeasureExpr
}
type MeasureNeg struct{ X MeasureExpr }
type MeasureConst struct {
	Value float64
	Text  string
}

func (MeasureCall) measureExpr()     {}
func (MeasureCountIf) measureExpr()  {}
func (MeasureBinary) measureExpr()   {}
func (MeasureNeg) measureExpr()      {}
func (MeasureConst) measureExpr()    {}
func (m Measure) IsExpression() bool { return m.Func == "expr" }

type RankRule struct {
	Rule, Better  string
	Floor         int
	SupportColumn string
}
type Ranking struct {
	Rule          string   `json:"rule"`
	Better        string   `json:"better"`
	Floor         int      `json:"floor"`
	SupportColumn string   `json:"support_column"`
	BelowFloor    int      `json:"below_floor"`
	TieBreakers   []string `json:"tie_breakers"`
}

const (
	maxMeasureTokens      = 256
	maxMeasureNodes       = 64
	maxMeasureDepth       = 8
	maxMeasureLeaves      = 8
	maxQueryMeasureNodes  = 256
	maxQueryMeasureLeaves = 24
)

type measureToken struct {
	text       string
	start, end int
}
type measureParser struct {
	sig               *signal
	text              string
	tokens            []measureToken
	at, nodes, leaves int
	parsedDepth       int
	message, hint     string
}

// Strings (including escaped SQL quotes) remain one token, and SQL comments
// are skipped. The scanner counts SQL tokens inside count_if too, without
// interpreting or trusting that SQL.
func measureTokens(text string) ([]measureToken, string) {
	var tokens []measureToken
	for i := 0; i < len(text); {
		if text[i] == ' ' || text[i] == '\t' || text[i] == '\r' || text[i] == '\n' {
			i++
			continue
		}
		start := i
		c := text[i]
		i++
		switch {
		case c == '-' && i < len(text) && text[i] == '-':
			for i < len(text) && text[i] != '\n' && text[i] != '\r' {
				i++
			}
			continue
		case c == '/' && i < len(text) && text[i] == '*':
			i++
			depth := 1
			for i < len(text) && depth > 0 {
				switch {
				case i+1 < len(text) && text[i:i+2] == "/*":
					depth++
					i += 2
				case i+1 < len(text) && text[i:i+2] == "*/":
					depth--
					i += 2
				default:
					i++
				}
			}
			if depth != 0 {
				return nil, "unterminated block comment"
			}
			continue
		case c == '\'' || c == '"':
			closed := false
			for i < len(text) {
				if text[i] == c {
					i++
					if i < len(text) && text[i] == c {
						i++
						continue
					}
					closed = true
					break
				}
				i++
			}
			if !closed {
				return nil, "unterminated quoted literal"
			}
		case c >= '0' && c <= '9' || c == '.' && i < len(text) && text[i] >= '0' && text[i] <= '9':
			for i < len(text) && ((text[i] >= '0' && text[i] <= '9') || text[i] == '.') {
				i++
			}
			if i < len(text) && (text[i] == 'e' || text[i] == 'E') {
				i++
				if i < len(text) && (text[i] == '+' || text[i] == '-') {
					i++
				}
				for i < len(text) && text[i] >= '0' && text[i] <= '9' {
					i++
				}
			}
		case c == '_' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= 128:
			for i < len(text) {
				c := text[i]
				if !(c == '_' || c == '.' || c >= 'A' && c <= 'Z' || c >= 'a' && c <= 'z' || c >= '0' && c <= '9' || c >= 128) {
					break
				}
				i++
			}
		}
		tokens = append(tokens, measureToken{text[start:i], start, i})
		if len(tokens) > maxMeasureTokens {
			return nil, fmt.Sprintf("has %d tokens; a measure is at most %d", len(tokens), maxMeasureTokens)
		}
	}
	return tokens, ""
}
func (p *measureParser) peek() string {
	if p.at >= len(p.tokens) {
		return ""
	}
	return p.tokens[p.at].text
}
func (p *measureParser) take(text string) bool {
	if strings.EqualFold(p.peek(), text) {
		p.at++
		return true
	}
	return false
}
func (p *measureParser) fail(message string) {
	if p.message == "" {
		p.message = message
	}
}
func (p *measureParser) node(e MeasureExpr) MeasureExpr {
	p.nodes++
	if p.nodes > maxMeasureNodes {
		p.fail(fmt.Sprintf("has %d nodes; a measure is at most %d", p.nodes, maxMeasureNodes))
	}
	switch e.(type) {
	case MeasureCall, MeasureCountIf:
		p.leaves++
		if p.leaves > maxMeasureLeaves {
			p.fail(fmt.Sprintf("has %d aggregates; a measure has 1 to %d", p.leaves, maxMeasureLeaves))
		}
	}
	return e
}
func (p *measureParser) expr(minPrecedence, depth int) MeasureExpr {
	if depth > maxMeasureDepth {
		p.fail(fmt.Sprintf("nests %d deep; a measure nests at most %d", depth, maxMeasureDepth))
		return nil
	}
	if p.message != "" {
		return nil
	}
	var left MeasureExpr
	leftDepth := 0
	switch {
	case p.take("-"):
		x := p.expr(3, depth+1)
		leftDepth = p.parsedDepth + 1
		left = p.node(MeasureNeg{x})
	case p.take("("):
		left = p.expr(0, depth+1)
		leftDepth = p.parsedDepth + 1
		if !p.take(")") {
			p.fail("expected )")
		}
	case p.peek() == "":
		p.fail("expected a measure or number")
		return nil
	default:
		tok := p.tokens[p.at]
		p.at++
		if n, err := strconv.ParseFloat(tok.text, 64); err == nil && !math.IsNaN(n) && !math.IsInf(n, 0) {
			left = p.node(MeasureConst{n, tok.text})
		} else {
			left = p.call(tok)
		}
	}
	for p.message == "" {
		op := p.peek()
		precedence := 0
		if op == "+" || op == "-" {
			precedence = 1
		}
		if op == "*" || op == "/" {
			precedence = 2
		}
		if precedence == 0 || precedence < minPrecedence {
			break
		}
		p.at++
		right := p.expr(precedence+1, depth+1)
		leftDepth = 1 + max(leftDepth, p.parsedDepth)
		if d := leftDepth + depth; d > maxMeasureDepth {
			p.fail(fmt.Sprintf("nests %d deep; a measure nests at most %d", d, maxMeasureDepth))
			break
		}
		left = p.node(MeasureBinary{op[0], left, right})
	}
	p.parsedDepth = leftDepth
	return left
}
func (p *measureParser) call(tok measureToken) MeasureExpr {
	name := strings.ToLower(tok.text)
	if !p.take("(") {
		p.fail("is not fn(field) [as alias]")
		return nil
	}
	opening := p.tokens[p.at-1]
	start := opening.end
	nest := 1
	end := start
	for p.at < len(p.tokens) {
		t := p.tokens[p.at]
		p.at++
		if t.text == "(" {
			nest++
		}
		if t.text == ")" {
			nest--
			if nest == 0 {
				end = t.start
				break
			}
		}
	}
	if nest != 0 {
		if name == "count_if" {
			p.fail("unclosed count_if")
		} else {
			p.fail("unclosed measure call")
		}
		return nil
	}
	inner := p.text[start:end]
	if name == "count_if" {
		if strings.TrimSpace(inner) == "" {
			p.fail("count_if needs a filter")
		}
		if len(inner) > 500 {
			p.fail("filters are 1 to 500 characters")
		}
		return p.node(MeasureCountIf{Filter: inner})
	}
	spec, ok := measureFuncs[name]
	if !ok {
		p.fail(fmt.Sprintf("unknown function %s", name))
		p.hint = suggestOr(name, measureNames(), "functions: "+strings.Join(measureNames(), ", "))
		return nil
	}
	if len(spec.signals) > 0 && !slices.Contains(spec.signals, p.sig.name) {
		p.fail(fmt.Sprintf("%s is not available for %s", name, p.sig.name))
		p.hint = "available for " + strings.Join(spec.signals, ", ")
		return nil
	}
	var args []string
	// Commas in literal attribute keys belong to the field, not the signature.
	argStart := start
	quoted := byte(0)
	for i := start; i < end; i++ {
		c := p.text[i]
		if quoted != 0 {
			if c == quoted {
				if i+1 < end && p.text[i+1] == quoted {
					i++
					continue
				}
				quoted = 0
			}
			continue
		}
		if c == '\'' || c == '"' {
			quoted = c
		}
		if c == ',' {
			args = append(args, strings.TrimSpace(p.text[argStart:i]))
			argStart = i + 1
		}
	}
	if strings.TrimSpace(inner) != "" {
		args = append(args, strings.TrimSpace(p.text[argStart:end]))
	}
	// SQL's conventional count(*) spelling is equivalent to count().
	if name == "count" && len(args) == 1 && args[0] == "*" {
		args = nil
	}
	if len(args) != spec.args {
		p.fail(fmt.Sprintf("%s takes %d argument(s), got %d", name, spec.args, len(args)))
		return nil
	}
	call := MeasureCall{Func: name, Q: spec.quantile}
	if spec.args >= 1 {
		ref, err := p.sig.field(args[0])
		if err != nil {
			p.fail(err.Error())
			return nil
		}
		if spec.numeric && ref.Type != TypeNumber && ref.Key == "" {
			p.fail(fmt.Sprintf("%s needs a numeric field; %s is %s", name, ref.Text, ref.Type))
			return nil
		}
		call.Field = &ref
	}
	if name == "quantile" {
		q, err := strconv.ParseFloat(args[1], 64)
		if err != nil || q <= 0 || q >= 1 {
			p.fail("quantile q must be a number between 0 and 1")
			p.hint = "e.g. quantile(duration_ms, 0.999)"
			return nil
		}
		call.Q = q
	}
	return p.node(call)
}
func parseMeasure(sig *signal, text string) (Measure, string, string) {
	tokens, message := measureTokens(text)
	if message != "" {
		return Measure{}, message, ""
	}
	p := measureParser{sig: sig, text: text, tokens: tokens}
	e := p.expr(0, 0)
	alias := ""
	if p.take("as") {
		if p.at < len(tokens) {
			alias = p.peek()
			p.at++
		}
		if !aliasPattern.MatchString(alias) {
			p.fail(fmt.Sprintf("alias %q must be lowercase letters, digits and underscores", alias))
		}
	}
	if p.at < len(tokens) {
		p.fail("unexpected token " + p.peek())
	}
	if p.message != "" {
		return Measure{}, p.message, p.hint
	}
	if p.leaves == 0 {
		return Measure{}, "a measure needs an aggregate; constants can only scale one, e.g. count() * 100", ""
	}
	m := Measure{Text: strings.TrimSpace(text), Func: "expr", Alias: alias, Expr: e, Leaves: p.leaves, nodes: p.nodes, Unit: measureUnit(e)}
	switch call := e.(type) {
	case MeasureCall:
		m.Func = call.Func
		m.Field = call.Field
		m.Q = call.Q
		m.Additive = measureFuncs[call.Func].additive
	case MeasureCountIf:
		m.Func = "count_if"
	}
	if sig.name == "metrics" && (m.IsExpression() || m.Func == "count_if") {
		return Measure{}, "measure arithmetic is not available for metrics", ""
	}
	if m.IsExpression() {
		if hasShare(e) {
			return Measure{}, "share() cannot be combined; it is already a percentage of the scope", "use share() alone, or count_if(...) / count() * 100 for a custom ratio"
		}
		if alias == "" {
			return Measure{}, "name this expression with as, e.g. count() / 2 as half", ""
		}
	}
	m.Rank = rankRule(m, sig, "")
	return m, "", ""
}
func hasShare(e MeasureExpr) bool {
	switch e := e.(type) {
	case MeasureCall:
		return e.Func == "share"
	case MeasureBinary:
		return hasShare(e.L) || hasShare(e.R)
	case MeasureNeg:
		return hasShare(e.X)
	}
	return false
}
func measureUnit(e MeasureExpr) string {
	switch e := e.(type) {
	case MeasureCall:
		spec := measureFuncs[e.Func]
		if spec.unit != "" {
			return spec.unit
		}
		if e.Field != nil {
			return e.Field.Unit
		}
	case MeasureCountIf:
		return "count"
	case MeasureNeg:
		return measureUnit(e.X)
	case MeasureBinary:
		l, r := measureUnit(e.L), measureUnit(e.R)
		if (e.Op == '+' || e.Op == '-') && l == r {
			return l
		}
		if e.Op == '/' && l == "count" && r == "count" {
			return "ratio"
		}
		if e.Op == '*' {
			if n, ok := constantValue(e.R); ok {
				if l == "ratio" && n == 100 {
					return "percent"
				}
				return l
			}
			if n, ok := constantValue(e.L); ok {
				if r == "ratio" && n == 100 {
					return "percent"
				}
				return r
			}
		}
		if e.Op == '/' {
			if _, ok := constantValue(e.R); ok {
				return l
			}
		}
	}
	return "none"
}

func constantValue(e MeasureExpr) (float64, bool) {
	switch e := e.(type) {
	case MeasureConst:
		return e.Value, true
	case MeasureNeg:
		v, ok := constantValue(e.X)
		return -v, ok
	case MeasureBinary:
		l, ok := constantValue(e.L)
		r, ok2 := constantValue(e.R)
		if ok && ok2 {
			switch e.Op {
			case '+':
				return l + r, true
			case '-':
				return l - r, true
			case '*':
				return l * r, true
			case '/':
				if r != 0 {
					return l / r, true
				}
			}
		}
	}
	return 0, false
}

func suggestOr(word string, candidates []string, fallback string) string {
	if hint := suggest(word, candidates); hint != "" {
		return hint
	}
	return fallback
}

// parseMeasures parses every measure and gives each a unique alias: the
// function name, or function_field when two measures share a function.
func parseMeasures(sig *signal, texts []string, path string, problems *Problems) []Measure {
	out := make([]Measure, 0, len(texts))
	nodes, leaves := 0, 0
	for i, text := range texts {
		m, message, hint := parseMeasure(sig, text)
		if message != "" {
			problems.addHint(fmt.Sprintf("%s[%d]", path, i), fmt.Sprintf("measures[%d] %s", i, message), hint)
			continue
		}
		out = append(out, m)
		nodes += m.nodes
		leaves += m.Leaves
	}
	if nodes > maxQueryMeasureNodes {
		problems.add(path, fmt.Sprintf("measures have %d nodes together; a query has at most %d", nodes, maxQueryMeasureNodes))
	}
	if leaves > maxQueryMeasureLeaves {
		problems.add(path, fmt.Sprintf("measures have %d aggregates together; a query has at most %d", leaves, maxQueryMeasureLeaves))
	}
	uses := map[string]int{}
	for _, m := range out {
		if m.Alias == "" {
			uses[m.Func]++
		}
	}
	seen := map[string]bool{}
	for i := range out {
		if out[i].Alias == "" {
			out[i].Alias = out[i].Func
			if uses[out[i].Func] > 1 && out[i].Field != nil {
				out[i].Alias = out[i].Func + "_" + sanitize(out[i].Field.alias())
			}
		}
		if seen[out[i].Alias] {
			problems.add(path, fmt.Sprintf("two measures are named %s; add distinct aliases with as", out[i].Alias))
		}
		seen[out[i].Alias] = true
	}
	return out
}

func sanitize(value string) string {
	var b strings.Builder
	for _, r := range strings.ToLower(value) {
		if r >= 'a' && r <= 'z' || r >= '0' && r <= '9' {
			b.WriteRune(r)
		} else {
			b.WriteByte('_')
		}
	}
	return strings.Trim(b.String(), "_")
}

// ParseMeasure parses one stored aggregate measure for the named signal.
func ParseMeasure(signal, text string) (Measure, error) {
	sig, ok := lookupSignal(signal)
	if !ok {
		return Measure{}, fmt.Errorf("unknown signal %q", signal)
	}
	m, message, hint := parseMeasure(sig, text)
	if message != "" {
		if hint != "" {
			message += "; " + hint
		}
		return Measure{}, fmt.Errorf("%s", message)
	}
	return m, nil
}
