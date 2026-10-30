package fanql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"

	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/sqlscan"
	"github.com/labstack/fanout/internal/telemetry"
)

// formatInputError denotes a stored query that cannot be expressed without
// changing meaning. Every other non-syntax failure belongs to the engine.
type formatInputError struct{ message string }

func (e formatInputError) Error() string { return e.message }
func formatInput(message string, args ...any) error {
	return formatInputError{message: fmt.Sprintf(message, args...)}
}
func operationalFormatError(err error) bool {
	if err == nil {
		return false
	}
	if errors.Is(err, context.Canceled) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, sql.ErrConnDone) {
		return true
	}
	var input formatInputError
	return !errors.As(err, &input) && !strings.HasPrefix(err.Error(), "SQL parsing failed:")
}

func liftEntry(ctx context.Context, p panel.Parser, signal, stored string) (filterExpr, tier, error) {
	if e := recognizeOwnLowering(signal, stored); e != nil {
		return e, tierExact, nil
	}
	node, err := p.ParseSQL(ctx, "SELECT 1 FROM "+signal+" WHERE ("+stored+")")
	if err != nil {
		return nil, tierRaw, err
	}
	e := liftSQLNode(signal, panel.SQLExpression(node["where_clause"]))
	e = reparseLiftedEntry(signal, e)
	if e == nil {
		return &rawEntry{Text: stored}, tierRaw, nil
	}
	lowered := lowerEntry(e)
	if lowered == stored {
		return e, tierExact, nil
	}
	candidate, err := p.ParseSQL(ctx, "SELECT 1 FROM "+signal+" WHERE ("+lowered+")")
	if err != nil {
		if operationalFormatError(err) {
			return nil, tierRaw, err
		}
		return &rawEntry{Text: stored}, tierRaw, nil
	}
	a, err := p.RenderSQL(ctx, node)
	if err != nil {
		return nil, tierRaw, err
	}
	b, err := p.RenderSQL(ctx, candidate)
	if err != nil {
		return nil, tierRaw, err
	}
	if a == b {
		return e, tierNormalized, nil
	}
	return &rawEntry{Text: stored}, tierRaw, nil
}

// recognizeOwnLowering parses only the grammar our lowerer emits. It is not a
// SQL authority: the result is discarded unless lowering recreates every byte.
func recognizeOwnLowering(signal, stored string) filterExpr {
	r := &ownSQLReader{tokens: ownSQLTokens(stored)}
	n := r.expression(0)
	if r.failed || r.pos != len(r.tokens) {
		return nil
	}
	e := reparseLiftedEntry(signal, liftSQLNode(signal, n))
	if e != nil && lowerEntry(e) == stored {
		return e
	}
	return nil
}

// The gate uses the complete unbounded entry parser, then the resolver's
// semantic walk, so a short spelling is accepted only with the meaning that a
// user editing that spelling will actually obtain.
func reparseLiftedEntry(signal string, e filterExpr) filterExpr {
	if e == nil {
		return nil
	}
	parsed, ps := parseEntry(printEntry(e, false))
	if len(ps) > 0 {
		return nil
	}
	q := &queryTree{Source: signal, Where: []filterExpr{parsed}}
	r := &resolver{vocab: currentVocabulary(), out: &resolved{Query: q, Notices: []Notice{}}}
	r.expr(parsed)
	if len(r.problems) > 0 {
		return nil
	}
	return parsed
}

func liftFieldText(signal, text string) (fieldName, bool) {
	r := &ownSQLReader{tokens: ownSQLTokens(text)}
	node := r.atom()
	if r.failed || r.pos != len(r.tokens) {
		return fieldName{}, false
	}
	return liftSQLField(signal, node, false)
}

func liftSQLField(signal string, node *panel.SQLExpr, contains bool) (fieldName, bool) {
	if node == nil {
		return fieldName{}, false
	}
	switch node.Class {
	case "CAST":
		name := node.CastType
		f, ok := liftSQLField(signal, node.Child, false)
		if !ok {
			return fieldName{}, false
		}
		if contains && name == "VARCHAR" && f.Ref.Key != "" {
			return f, true
		}
		tr := node.TryCast
		if tr && name == "INTEGER" && f.Ref.Column == "http_status_code" && f.Ref.Key == "" {
			f.Ref.CompareAs = "integer"
			return f, true
		}
		return fieldName{}, false
	case "COLUMN_REF":
		names := node.Columns
		if len(names) != 1 {
			return fieldName{}, false
		}
		name := names[0]
		cols, _ := panel.SignalColumns(signal)
		for _, col := range cols {
			if col.Name == name && col.Type != panel.TypeMap {
				return fieldName{Key: name, Single: true, Ref: &fieldRef{Column: name, Type: col.Type, Unit: col.Unit}}, true
			}
		}
	case "OPERATOR":
		if node.Type != "ARRAY_EXTRACT" {
			return fieldName{}, false
		}
		children := node.Children
		if len(children) != 2 {
			return fieldName{}, false
		}
		col := children[0]
		names := col.Columns
		if col.Class != "COLUMN_REF" || len(names) != 1 || (names[0] != "attributes" && names[0] != "resource") {
			return fieldName{}, false
		}
		v, ok := liftSQLValue(children[1])
		if !ok || v.Kind != stringValue {
			return fieldName{}, false
		}
		column := names[0]
		return fieldName{Scope: column, Key: v.Text, Bracket: true, Ref: &fieldRef{Column: column, Key: v.Text, Type: panel.TypeString, Unit: "none"}}, true
	}
	return fieldName{}, false
}

func liftSQLValue(node *panel.SQLExpr) (literalValue, bool) {
	if node == nil {
		return literalValue{}, false
	}
	if node.Class == "CAST" {
		if node.CastType == "TIMESTAMPTZ_NS" {
			return liftSQLValue(node.Child)
		}
		return literalValue{}, false
	}
	if node.Class == "PARAMETER" {
		id := node.Identifier
		if id != "" {
			return literalValue{Kind: variableValue, Text: "$" + id, Stored: "$" + id}, true
		}
		return literalValue{}, false
	}
	if node.Class != "CONSTANT" {
		return literalValue{}, false
	}
	kind, text := node.LiteralKind, node.LiteralText
	switch kind {
	case "STRING":
		return literalValue{Kind: stringValue, Text: text, Stored: text}, true
	case "INTEGER", "NUMERIC", "DECIMAL", "FLOAT", "DOUBLE":
		return literalValue{Kind: numberValue, Text: text, Stored: text}, true
	case "BOOLEAN":
		return literalValue{Kind: booleanValue, Text: strings.ToLower(text), Stored: strings.ToLower(text)}, true
	}
	return literalValue{}, false
}

func spellingValue(f fieldName, v literalValue, aliases bool) literalValue {
	if v.Kind == variableValue {
		return v
	}
	if f.Ref.Column == "duration_ms" && f.Ref.Key == "" && v.Kind == numberValue {
		if n, err := strconv.ParseFloat(v.Text, 64); err == nil && n >= 0 && decimal(n) == numberText(v.Text) {
			v.Kind = durationValue
			v.Text = durationText(n)
		}
		return v
	}
	if v.Kind == stringValue && f.Ref.Key == "" && f.Ref.Type == panel.TypeString {
		if aliases {
			for alias, stored := range panel.FieldAliases(f.Ref.Column) {
				if v.Text == stored {
					v.Kind = wordValue
					v.Text = alias
					return v
				}
			}
			switch f.Ref.Column {
			case "severity":
				if v.Text == strings.ToUpper(v.Text) && classify(lexToken{Kind: tokenWord, Text: strings.ToLower(v.Text)}).Kind == wordValue {
					v.Kind = wordValue
					v.Text = strings.ToLower(v.Text)
					return v
				}
			}
		}
		if f.Ref.Column != "status" && f.Ref.Column != "kind" && f.Ref.Column != "severity" && f.Ref.Column != "type" && f.Ref.CompareAs == "" {
			if numberPattern.MatchString(v.Text) {
				v.Kind = numberValue
				return v
			}
			if v.Text == "true" || v.Text == "false" {
				v.Kind = booleanValue
				return v
			}
		}
		if f.Ref.Column == "status" || f.Ref.Column == "kind" || f.Ref.Column == "severity" {
			return v
		}
		if f.Ref.Column == "type" {
			if _, ok := panel.FieldAliases("type")[v.Text]; !ok {
				return v
			}
		}
		if bareValue(v.Text) && !numberPattern.MatchString(v.Text) && !durationPattern.MatchString(v.Text) && !strings.EqualFold(v.Text, "true") && !strings.EqualFold(v.Text, "false") {
			v.Kind = wordValue
		}
	}
	return v
}
func durationText(ms float64) string {
	units := []struct {
		value float64
		name  string
	}{{3600000, "h"}, {60000, "m"}, {1000, "s"}, {1, "ms"}, {0.001, "us"}}
	for _, u := range units {
		if ms >= u.value {
			text := strconv.FormatFloat(ms/u.value, 'f', -1, 64) + u.name
			// A shorter unit is safe only when parsing it recreates the exact
			// threshold; 1.001s, for example, is not exactly 1001 milliseconds.
			if restored, ok := durationMS(text); ok && restored == ms {
				return text
			}
		}
	}
	return strconv.FormatFloat(ms, 'f', -1, 64) + "ms"
}

func liftSQLNode(signal string, node *panel.SQLExpr) filterExpr {
	if node == nil {
		return nil
	}
	children := node.Children
	switch node.Class {
	case "CONJUNCTION":
		terms := make([]filterExpr, len(children))
		for i, c := range children {
			terms[i] = liftSQLNode(signal, c)
			if terms[i] == nil {
				return nil
			}
		}
		if node.Type == "CONJUNCTION_OR" {
			if len(terms) == 2 {
				if x := liftNullInclusive(terms[0], terms[1]); x != nil {
					return x
				}
				if x := liftNullInclusive(terms[1], terms[0]); x != nil {
					return x
				}
			}
			return &orExpr{Terms: terms}
		}
		return &andExpr{Terms: terms}
	case "COMPARISON":
		f, ok := liftSQLField(signal, node.Left, false)
		v, valid := liftSQLValue(node.Right)
		if ok && valid {
			op := map[string]string{"COMPARE_EQUAL": ":", "COMPARE_NOTEQUAL": "!=", "COMPARE_LESSTHAN": "<", "COMPARE_GREATERTHAN": ">", "COMPARE_LESSTHANOREQUALTO": "<=", "COMPARE_GREATERTHANOREQUALTO": ">="}[node.Type]
			if op != "" {
				return &comparison{Field: f, Op: op, Value: spellingValue(f, v, !isRange(op))}
			}
		}
		if call := liftSQLCall(signal, node.Left); call != nil && valid {
			op := map[string]string{"COMPARE_EQUAL": "=", "COMPARE_NOTEQUAL": "!=", "COMPARE_LESSTHAN": "<", "COMPARE_GREATERTHAN": ">", "COMPARE_LESSTHANOREQUALTO": "<=", "COMPARE_GREATERTHANOREQUALTO": ">="}[node.Type]
			if op != "" {
				call.Cmp = &callComparison{Op: op, Value: v}
				return call
			}
		}
	case "BETWEEN":
		f, ok := liftSQLField(signal, node.Input, false)
		lo, a := liftSQLValue(node.Lower)
		hi, b := liftSQLValue(node.Upper)
		if ok && a && b {
			neg := node.Negated
			return &betweenExpr{Field: f, Lo: spellingValue(f, lo, false), Hi: spellingValue(f, hi, false), Negated: neg}
		}
	case "OPERATOR":
		switch node.Type {
		case "OPERATOR_IS_NULL", "OPERATOR_IS_NOT_NULL":
			if len(children) == 1 {
				if f, ok := liftSQLField(signal, children[0], false); ok {
					return &nullExpr{Field: f, Negated: node.Type == "OPERATOR_IS_NOT_NULL"}
				}
			}
		case "COMPARE_IN", "COMPARE_NOT_IN":
			if len(children) < 2 {
				return nil
			}
			f, ok := liftSQLField(signal, children[0], false)
			if !ok {
				return nil
			}
			in := &inExpr{Field: f, Negated: node.Type == "COMPARE_NOT_IN"}
			for _, c := range children[1:] {
				v, ok := liftSQLValue(c)
				if !ok {
					return nil
				}
				in.Values = append(in.Values, spellingValue(f, v, true))
			}
			if len(in.Values) == 1 && in.Values[0].Kind == variableValue {
				in.Variable = strings.TrimPrefix(in.Values[0].Text, "$")
				in.Values = nil
				return in
			}
			if !in.Negated && f.Ref.Column == "severity" && f.Ref.Key == "" {
				return liftSeverityRange(in)
			}
			return in
		case "OPERATOR_NOT":
			if len(children) != 1 {
				return nil
			}
			inner := children[0]
			if inner.Class == "OPERATOR" && inner.Type == "OPERATOR_COALESCE" {
				parts := inner.Children
				if len(parts) == 2 {
					v, ok := liftSQLValue(parts[1])
					if ok && v.Kind == booleanValue && v.Text == "false" {
						if e := liftSQLNode(signal, parts[0]); e != nil {
							return &notExpr{X: e}
						}
					}
				}
			}
			if inner.Class == "BETWEEN" {
				if e, ok := liftSQLNode(signal, children[0]).(*betweenExpr); ok {
					e.Negated = !e.Negated
					return e
				}
			}
			if e := liftSQLNode(signal, children[0]); e != nil {
				return &notExpr{X: e}
			}
		}
	case "FUNCTION":
		name := node.Function
		args := node.Arguments
		if name == "contains" && len(args) == 2 {
			if v, ok := liftSQLValue(args[0]); ok && v.Kind == variableValue {
				if f, ok := liftSQLField(signal, args[1], false); ok {
					return &inExpr{Field: f, Variable: strings.TrimPrefix(v.Text, "$")}
				}
			}
			left, lok := unwrapFunction(args[0], "lower")
			right, rok := unwrapFunction(args[1], "lower")
			if lok && rok {
				if f, ok := liftSQLField(signal, left, true); ok {
					if v, ok := liftSQLValue(right); ok {
						if v.Kind != variableValue && f.Ref.Key == "" && (signal == "logs" && f.Ref.Column == "body" || signal == "spans" && f.Ref.Column == "operation") {
							if v.Kind == stringValue && bareValue(v.Text) {
								v.Kind = wordValue
							}
							return &textExpr{Value: v, column: f.Ref.Column}
						}
						if v.Kind == stringValue && bareValue(v.Text) {
							v.Kind = wordValue
						}
						return &comparison{Field: f, Op: "~", Value: v}
					}
				}
			}
		}
		if (name == "~~" || name == "!~~" || name == "~~*" || name == "!~~*") && len(args) == 2 {
			f, ok := liftSQLField(signal, args[0], false)
			v, valid := liftSQLValue(args[1])
			if ok && valid {
				return &likeExpr{Field: f, Pattern: v, Negated: strings.HasPrefix(name, "!"), Insensitive: strings.HasSuffix(name, "*")}
			}
		}
		if call := liftSQLCall(signal, node); call != nil {
			return call
		}
	}
	return nil
}

func liftSQLCall(signal string, node *panel.SQLExpr) *filterCall {
	if node == nil || node.Class != "FUNCTION" {
		return nil
	}
	name := node.Function
	// String transforms used as operands deliberately stay raw. Predicate calls
	// and length comparisons are precisely representable and pass the gate.
	if name != "starts_with" && name != "contains" && name != "regexp_matches" && name != "length" {
		return nil
	}
	args := node.Arguments
	out := &filterCall{Name: name}
	for _, a := range args {
		if f, ok := liftSQLField(signal, a, false); ok {
			out.Args = append(out.Args, callArg{Field: &f})
		} else if v, ok := liftSQLValue(a); ok {
			out.Args = append(out.Args, callArg{Value: &v})
		} else {
			return nil
		}
	}
	return out
}
func unwrapFunction(node *panel.SQLExpr, name string) (*panel.SQLExpr, bool) {
	if node == nil || node.Class != "FUNCTION" || node.Function != name || len(node.Arguments) != 1 {
		return nil, false
	}
	return node.Arguments[0], true
}
func sameLiftedField(a, b fieldName) bool {
	return a.Ref != nil && b.Ref != nil && a.Ref.Column == b.Ref.Column && a.Ref.Key == b.Ref.Key
}
func liftNullInclusive(null, other filterExpr) filterExpr {
	is, ok := null.(*nullExpr)
	if !ok || is.Negated {
		return nil
	}
	switch x := other.(type) {
	case *comparison:
		if len(x.Bands) > 0 && sameLiftedField(is.Field, x.Field) {
			return &notExpr{X: bandMembership(x.Field, x.Bands, true)}
		}
		if !sameLiftedField(is.Field, x.Field) {
			return nil
		}
		c := *x
		switch c.Op {
		case ":", "=":
			c.Op = "!="
		case "!=", "<>":
			c.Op = ":"
		case ">":
			c.Op = "<="
		case ">=":
			c.Op = "<"
		case "<":
			c.Op = ">="
		case "<=":
			c.Op = ">"
		default:
			return nil
		}
		return &notExpr{X: &c}
	case *inExpr:
		if sameLiftedField(is.Field, x.Field) {
			c := *x
			c.Negated = !c.Negated
			return &notExpr{X: &c}
		}
	case *betweenExpr:
		if len(x.Bands) > 0 && sameLiftedField(is.Field, x.Field) {
			return &notExpr{X: bandMembership(x.Field, x.Bands, !x.Negated)}
		}
		if sameLiftedField(is.Field, x.Field) {
			c := *x
			c.Negated = !c.Negated
			return &notExpr{X: &c}
		}
	case *likeExpr:
		if sameLiftedField(is.Field, x.Field) {
			c := *x
			c.Negated = !c.Negated
			return &notExpr{X: &c}
		}
	case *notExpr:
		switch c := x.X.(type) {
		case *comparison:
			if c.Op == "~" && sameLiftedField(is.Field, c.Field) {
				return &notExpr{X: c}
			}
		case *textExpr:
			if is.Field.Ref.Key == "" && (is.Field.Ref.Column == "body" || is.Field.Ref.Column == "operation") {
				return &notExpr{X: c}
			}
		}
	}
	return nil
}
func bandMembership(f fieldName, bands []string, negated bool) *inExpr {
	in := &inExpr{Field: f, Negated: negated}
	for _, band := range bands {
		in.Values = append(in.Values, literalValue{Kind: wordValue, Text: strings.ToLower(band), Stored: band})
	}
	return in
}

func liftSeverityRange(in *inExpr) filterExpr {
	if len(in.Values) < 1 {
		return in
	}
	ranks := make([]int, len(in.Values))
	for i, v := range in.Values {
		rank, ok := telemetry.SeverityRank(v.Stored)
		if !ok {
			return in
		}
		ranks[i] = rank
		if i > 0 && rank != ranks[i-1]+1 {
			return in
		}
	}
	// The order and spelling must be exactly the shared canonical band table.
	first, last := ranks[0], ranks[len(ranks)-1]
	if first < 0 || last >= len(telemetry.SeverityBands) {
		return in
	}
	value := func(rank int) literalValue {
		band := telemetry.SeverityBands[rank]
		return literalValue{Kind: wordValue, Text: strings.ToLower(band), Stored: band}
	}
	bands := make([]string, len(ranks))
	for i, r := range ranks {
		bands[i] = telemetry.SeverityBands[r]
	}
	if last == len(telemetry.SeverityBands)-1 {
		return &comparison{Field: in.Field, Op: ">=", Value: value(first), Bands: bands}
	}
	if first == 0 {
		return &comparison{Field: in.Field, Op: "<=", Value: value(last), Bands: bands}
	}
	return &betweenExpr{Field: in.Field, Lo: value(first), Hi: value(last), Bands: bands}
}

func liftMeasure(ctx context.Context, p panel.Parser, signal, stored string) (measureNode, tier, error) {
	parsed, err := panel.ParseMeasure(signal, stored)
	if err != nil {
		return measureNode{}, tierRaw, formatInput("%s", err)
	}
	m := measureNode{Parsed: parsed, Alias: parsed.Alias, Filters: map[string]filterExpr{}}
	var lift func(panel.MeasureExpr) error
	lift = func(e panel.MeasureExpr) error {
		switch x := e.(type) {
		case panel.MeasureCountIf:
			// The count_if conjunction is intentionally stored without outer brackets.
			reader := &ownSQLReader{tokens: ownSQLTokens(x.Filter)}
			node := reader.expression(0)
			var filter filterExpr
			if !reader.failed && reader.pos == len(reader.tokens) {
				candidate := liftSQLNode(signal, node)
				if candidate != nil && lowerCountFilter(candidate) == x.Filter {
					filter = candidate
				}
			}
			if filter == nil {
				var err error
				filter, _, err = liftEntry(ctx, p, signal, x.Filter)
				if err != nil {
					return err
				}
			}
			if lowerCountFilter(filter) != x.Filter {
				filter = &rawEntry{Text: x.Filter}
			}
			m.Filters[x.Filter] = filter
		case panel.MeasureBinary:
			if err := lift(x.L); err != nil {
				return err
			}
			return lift(x.R)
		case panel.MeasureNeg:
			return lift(x.X)
		}
		return nil
	}
	if err := lift(parsed.Expr); err != nil {
		return measureNode{}, tierRaw, err
	}
	lowered := lowerMeasure(m)
	if lowered == stored {
		return m, tierExact, nil
	}
	again, err := panel.ParseMeasure(signal, lowered)
	if err == nil && reflect.DeepEqual(parsed.Expr, again.Expr) && parsed.Alias == again.Alias {
		return m, tierNormalized, nil
	}
	return measureNode{}, tierRaw, formatInput("measure %q cannot be formatted without changing its parsed meaning", stored)
}

// ownSQLReader recognizes only the expression productions emitted by lowerEntry.
// Quoting is scanned rather than split, so clause words and delimiters inside a
// SQL literal cannot affect the tree.
type ownSQLToken struct {
	text       string
	quote      byte
	start, end int
}

func ownSQLTokens(text string) []ownSQLToken {
	var out []ownSQLToken
	for i := 0; i < len(text); {
		if strings.ContainsRune(" \t\r\n", rune(text[i])) {
			i++
			continue
		}
		start := i
		c := text[i]
		region := sqlscan.At(text, start)
		if region.Comment {
			i = region.End
			continue
		}
		if region.Quote != 0 {
			i = region.End
			if region.Message != "" {
				return nil
			}
			quote := string(region.Quote)
			value := strings.ReplaceAll(text[start+1:i-1], quote+quote, quote)
			out = append(out, ownSQLToken{text: value, quote: region.Quote, start: start, end: i})
			continue
		}
		if strings.ContainsRune("(),[]", rune(c)) {
			i++
			out = append(out, ownSQLToken{text: string(c), start: start, end: i})
			continue
		}
		if strings.ContainsRune("=<>!~:", rune(c)) {
			i++
			for i < len(text) && strings.ContainsRune("=<>!~:*", rune(text[i])) {
				i++
			}
			out = append(out, ownSQLToken{text: text[start:i], start: start, end: i})
			continue
		}
		for i < len(text) && !strings.ContainsRune(" \t\r\n(),[]=<>!~:'\"", rune(text[i])) {
			if sqlscan.At(text, i).Comment {
				break
			}
			i++
		}
		if i == start {
			i++
		}
		out = append(out, ownSQLToken{text: text[start:i], start: start, end: i})
	}
	return out
}

type ownSQLReader struct {
	tokens []ownSQLToken
	pos    int
	failed bool
}

func (r *ownSQLReader) peek() string {
	if r.pos == len(r.tokens) {
		return ""
	}
	if r.tokens[r.pos].quote != 0 {
		return "#quoted"
	}
	return strings.ToUpper(r.tokens[r.pos].text)
}
func (r *ownSQLReader) eat(s string) bool {
	if r.peek() == s {
		r.pos++
		return true
	}
	return false
}
func (r *ownSQLReader) need(s string) {
	if !r.eat(s) {
		r.failed = true
	}
}
func (r *ownSQLReader) expression(min int) *panel.SQLExpr {
	left := r.predicate()
	for {
		op := r.peek()
		prec := 0
		if op == "OR" {
			prec = 1
		}
		if op == "AND" {
			prec = 2
		}
		if prec == 0 || prec < min {
			break
		}
		r.pos++
		right := r.expression(prec + 1)
		kind := "CONJUNCTION_" + op
		if left.Class == "CONJUNCTION" && left.Type == kind && !left.Grouped {
			children := left.Children
			left.Children = append(children, right)
		} else {
			left = &panel.SQLExpr{Class: "CONJUNCTION", Type: kind, Children: []*panel.SQLExpr{left, right}}
		}
	}
	return left
}
func (r *ownSQLReader) predicate() *panel.SQLExpr {
	if r.eat("NOT") {
		return ownOperator("OPERATOR_NOT", r.predicate())
	}
	left := r.atom()
	op := r.peek()
	if kind := map[string]string{"=": "COMPARE_EQUAL", "!=": "COMPARE_NOTEQUAL", "<>": "COMPARE_NOTEQUAL", "<": "COMPARE_LESSTHAN", ">": "COMPARE_GREATERTHAN", "<=": "COMPARE_LESSTHANOREQUALTO", ">=": "COMPARE_GREATERTHANOREQUALTO"}[op]; kind != "" {
		r.pos++
		return &panel.SQLExpr{Class: "COMPARISON", Type: kind, Left: left, Right: r.atom()}
	}
	if r.eat("IS") {
		neg := r.eat("NOT")
		r.need("NULL")
		kind := "OPERATOR_IS_NULL"
		if neg {
			kind = "OPERATOR_IS_NOT_NULL"
		}
		return ownOperator(kind, left)
	}
	neg := r.eat("NOT")
	if r.eat("IN") {
		children := []*panel.SQLExpr{left}
		if r.eat("(") {
			for {
				children = append(children, r.atom())
				if !r.eat(",") {
					break
				}
			}
			r.need(")")
		} else {
			children = append(children, r.atom())
		}
		kind := "COMPARE_IN"
		if neg {
			kind = "COMPARE_NOT_IN"
		}
		return ownOperator(kind, children...)
	}
	if r.eat("BETWEEN") {
		lo := r.atom()
		r.need("AND")
		hi := r.atom()
		return &panel.SQLExpr{Class: "BETWEEN", Type: "COMPARE_BETWEEN", Input: left, Lower: lo, Upper: hi, Negated: neg}
	}
	op = r.peek()
	if op == "~~" || op == "!~~" || op == "~~*" || op == "!~~*" {
		r.pos++
		return ownFunction(strings.ToLower(op), []*panel.SQLExpr{left, r.atom()})
	}
	if neg {
		r.failed = true
	}
	return left
}
func (r *ownSQLReader) atom() *panel.SQLExpr {
	if r.pos >= len(r.tokens) {
		r.failed = true
		return nil
	}
	if r.eat("(") {
		node := r.expression(0)
		r.need(")")
		if node != nil {
			node.Grouped = true
		}
		return node
	}
	token := r.tokens[r.pos]
	r.pos++
	var node *panel.SQLExpr
	switch {
	case token.quote == '\'':
		node = ownConstant("STRING", token.text)
	case token.quote == '"':
		node = &panel.SQLExpr{Class: "COLUMN_REF", Columns: []string{token.text}}
	case strings.EqualFold(token.text, "true") || strings.EqualFold(token.text, "false"):
		node = ownConstant("BOOLEAN", strings.ToLower(token.text))
	case numberPattern.MatchString(token.text):
		node = ownConstant("NUMERIC", token.text)
	case strings.HasPrefix(token.text, "$"):
		node = &panel.SQLExpr{Class: "PARAMETER", Identifier: strings.TrimPrefix(token.text, "$")}
	case r.eat("("):
		name := strings.ToLower(token.text)
		if name == "cast" || name == "try_cast" {
			child := r.expression(0)
			r.need("AS")
			if r.pos == len(r.tokens) {
				r.failed = true
				return nil
			}
			typ := strings.ToUpper(r.tokens[r.pos].text)
			r.pos++
			r.need(")")
			node = &panel.SQLExpr{Class: "CAST", Type: "OPERATOR_CAST", Child: child, TryCast: name == "try_cast", CastType: typ}
		} else {
			var args []*panel.SQLExpr
			if !r.eat(")") {
				for {
					args = append(args, r.expression(0))
					if !r.eat(",") {
						break
					}
				}
				r.need(")")
			}
			if name == "coalesce" {
				node = ownOperator("OPERATOR_COALESCE", args...)
			} else {
				node = ownFunction(name, args)
			}
		}
	default:
		node = &panel.SQLExpr{Class: "COLUMN_REF", Columns: []string{token.text}}
	}
	if r.eat("[") {
		key := r.atom()
		r.need("]")
		node = ownOperator("ARRAY_EXTRACT", node, key)
	}
	if r.eat("::") {
		if r.pos == len(r.tokens) {
			r.failed = true
			return nil
		}
		typ := strings.ToUpper(r.tokens[r.pos].text)
		r.pos++
		node = &panel.SQLExpr{Class: "CAST", Child: node, CastType: typ}
	}
	return node
}
func ownConstant(kind, text string) *panel.SQLExpr {
	return &panel.SQLExpr{Class: "CONSTANT", LiteralKind: kind, LiteralText: text}
}
func ownOperator(kind string, children ...*panel.SQLExpr) *panel.SQLExpr {
	return &panel.SQLExpr{Class: "OPERATOR", Type: kind, Children: children}
}
func ownFunction(name string, args []*panel.SQLExpr) *panel.SQLExpr {
	return &panel.SQLExpr{Class: "FUNCTION", Function: name, Arguments: args}
}
