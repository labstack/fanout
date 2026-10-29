package fanql

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"reflect"
	"strconv"
	"strings"
	"unicode/utf8"

	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/telemetry"
)

type FormatOptions struct {
	Viz       string
	Variables []panel.Variable
}
type Formatted struct {
	Text      string   `json:"text"`
	Exactness string   `json:"exactness"`
	Editable  bool     `json:"editable"`
	Reason    string   `json:"reason,omitempty"`
	Notices   []Notice `json:"notices"`
}
type tier uint8

const (
	tierExact tier = iota
	tierNormalized
	tierRaw
)

// Format preserves each stored filter as one entry. Candidates are kept only
// after a byte or engine-AST exactness check; all other filters remain raw.
func Format(ctx context.Context, p panel.Parser, stored panel.Query, opts FormatOptions) (*Formatted, panel.Problems, error) {
	vars := map[string]panel.Variable(nil)
	if opts.Variables != nil {
		vars = make(map[string]panel.Variable, len(opts.Variables))
		for _, v := range opts.Variables {
			vars[v.Name] = v
		}
	}
	var problems panel.Problems
	if opts.Viz != "" {
		problems, _ = panel.QueryShape(&stored, opts.Viz, vars)
	} else if len(panel.CompatibleVizzes(&stored, vars)) == 0 {
		problems, _ = panel.QueryShape(&stored, "table", vars)
	}
	if len(problems) > 0 {
		return nil, problems, nil
	}
	q := &Query{Source: stored.From}
	f := &Formatted{Exactness: "exact", Notices: []Notice{}}
	for i, text := range stored.Where {
		e, t, err := liftEntry(ctx, p, stored.From, text)
		if err != nil {
			if operationalFormatError(err) {
				return nil, nil, fmt.Errorf("format where[%d]: %w", i, err)
			}
			return nil, panel.Problems{{Path: fmt.Sprintf("where[%d]", i), Message: err.Error()}}, nil
		}
		q.Where = append(q.Where, e)
		if t == tierNormalized {
			f.Exactness = "normalized"
			f.Notices = append(f.Notices, Notice{Path: fmt.Sprintf("where[%d]", i), Message: fmt.Sprintf("where[%d] is shown as %s; stored text is %q", i, printEntry(e, false), text)})
		}
	}
	if len(stored.Measures) > 0 || stored.Histogram != nil || len(stored.By) > 0 || stored.Bucket != "" {
		q.Stage = &Stage{Bucket: stored.Bucket}
		if stored.Histogram != nil {
			field, ok := liftFieldText(stored.From, stored.Histogram.Field)
			if !ok {
				return nil, panel.Problems{{Path: "histogram.field", Message: "histogram field cannot be expressed in FanQL"}}, nil
			}
			q.Stage.Histogram = &field
		} else {
			for i, text := range stored.Measures {
				m, t, err := liftMeasure(ctx, p, stored.From, text)
				if err != nil {
					if operationalFormatError(err) {
						return nil, nil, fmt.Errorf("format measures[%d]: %w", i, err)
					}
					return nil, panel.Problems{{Path: fmt.Sprintf("measures[%d]", i), Message: err.Error()}}, nil
				}
				q.Stage.Measures = append(q.Stage.Measures, m)
				if t == tierNormalized {
					f.Exactness = "normalized"
					f.Notices = append(f.Notices, Notice{Path: fmt.Sprintf("measures[%d]", i), Message: fmt.Sprintf("measures[%d] is shown as %s; stored text is %q", i, printMeasure(m), text)})
				}
			}
		}
		for _, text := range stored.By {
			field, ok := liftFieldText(stored.From, text)
			if !ok {
				return nil, panel.Problems{{Path: "by", Message: "grouping field cannot be expressed in FanQL"}}, nil
			}
			q.Stage.By = append(q.Stage.By, field)
		}
	}
	if stored.Sort != "" {
		q.Sort = &Sort{Key: strings.TrimPrefix(stored.Sort, "+"), Ascending: strings.HasPrefix(stored.Sort, "+")}
	}
	if stored.Limit != 0 {
		q.Limit = &Limit{N: stored.Limit}
	}
	if opts.Viz == "traces" && stored.Sort == "errors" {
		f.Notices = append(f.Notices, Notice{Path: "sort", Message: "sort errors also filters to error spans; new queries: write status:error … sort duration"})
	}
	if opts.Viz != "" && (stored.Sort != "" || stored.Limit != 0) && stored.Bucket != "" || stored.Limit != 0 && (opts.Viz == "stat" || opts.Viz == "gauge") {
		f.Notices = append(f.Notices, Notice{Path: "sort", Message: "sort does not apply to bucketed series; series are chosen by top (options)"})
	}
	f.Text = Print(q)
	f.Reason = editableReason(f.Text)
	f.Editable = f.Reason == ""
	return f, nil, nil
}

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

func groupedNumber(n int) string {
	s := strconv.Itoa(n)
	for i := len(s) - 3; i > 0; i -= 3 {
		s = s[:i] + "," + s[i:]
	}
	return s
}
func editableReason(text string) string {
	bound := func(n, max int, unit string) string {
		return fmt.Sprintf("formatted text is %s %s; the editor accepts %s", groupedNumber(n), unit, groupedNumber(max))
	}
	if n := utf8.RuneCountInString(text); n > MaxTextRunes {
		return bound(n, MaxTextRunes, "characters")
	}
	if len(text) > MaxTextBytes {
		return bound(len(text), MaxTextBytes, "bytes")
	}
	tokens := Lex(text)
	if n := len(tokens) - 1; n > MaxTokens {
		return bound(n, MaxTokens, "tokens")
	}
	// The internal parser is unbounded; its counters still record the complete
	// tree, so oversized stored text is never truncated by interactive parsing.
	p := &parser{runes: []rune(text), tokens: tokens, heights: make(map[any]int)}
	p.query("")
	if p.nodes > MaxNodes {
		return bound(p.nodes, MaxNodes, "nodes")
	}
	maxDepth, lexicalDepth := 0, 0
	for _, token := range tokens {
		if token.Kind == Punct && token.Text == "(" {
			lexicalDepth++
			maxDepth = max(maxDepth, lexicalDepth)
		}
		if token.Kind == Punct && token.Text == ")" {
			lexicalDepth--
		}
	}
	for _, height := range p.heights {
		maxDepth = max(maxDepth, height)
	}
	if maxDepth > MaxDepth {
		return bound(maxDepth, MaxDepth, "levels of nesting depth")
	}
	return ""
}

func liftEntry(ctx context.Context, p panel.Parser, signal, stored string) (Expr, tier, error) {
	if e := recognizeOwnLowering(signal, stored); e != nil {
		return e, tierExact, nil
	}
	node, err := p.ParseSQL(ctx, "SELECT 1 FROM "+signal+" WHERE ("+stored+")")
	if err != nil {
		return nil, tierRaw, err
	}
	e := liftSQLNode(signal, node["where_clause"])
	e = reparseLiftedEntry(signal, e)
	if e == nil {
		return &RawExpr{Text: stored}, tierRaw, nil
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
		return &RawExpr{Text: stored}, tierRaw, nil
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
	return &RawExpr{Text: stored}, tierRaw, nil
}

// recognizeOwnLowering parses only the grammar our lowerer emits. It is not a
// SQL authority: the result is discarded unless lowering recreates every byte.
func recognizeOwnLowering(signal, stored string) Expr {
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
func reparseLiftedEntry(signal string, e Expr) Expr {
	if e == nil {
		return nil
	}
	parsed, ps := parseEntry(printEntry(e, false))
	if len(ps) > 0 {
		return nil
	}
	q := &Query{Source: signal, Where: []Expr{parsed}}
	r := &resolver{out: &Resolved{Query: q, Notices: []Notice{}}}
	r.expr(parsed)
	if len(r.problems) > 0 {
		return nil
	}
	return parsed
}

func liftFieldText(signal, text string) (Field, bool) {
	r := &ownSQLReader{tokens: ownSQLTokens(text)}
	node := r.atom()
	if r.failed || r.pos != len(r.tokens) {
		return Field{}, false
	}
	return liftSQLField(signal, node, false)
}

func liftSQLField(signal string, n any, contains bool) (Field, bool) {
	node, _ := n.(map[string]any)
	if node == nil {
		return Field{}, false
	}
	switch node["class"] {
	case "CAST":
		typ, _ := node["type_expr"].(map[string]any)
		name, _ := typ["type_name"].(string)
		f, ok := liftSQLField(signal, node["child"], false)
		if !ok {
			return Field{}, false
		}
		if contains && name == "VARCHAR" && f.Ref.Key != "" {
			return f, true
		}
		tr, _ := node["try_cast"].(bool)
		if tr && name == "INTEGER" && f.Ref.Column == "http_status_code" && f.Ref.Key == "" {
			f.Ref.CompareAs = "integer"
			return f, true
		}
		return Field{}, false
	case "COLUMN_REF":
		names, _ := node["column_names"].([]any)
		if len(names) != 1 {
			return Field{}, false
		}
		name, _ := names[0].(string)
		cols, _ := panel.SignalColumns(signal)
		for _, col := range cols {
			if col.Name == name && col.Type != panel.TypeMap {
				return Field{Key: name, Single: true, Ref: &FieldRef{Column: name, Type: col.Type, Unit: col.Unit}}, true
			}
		}
	case "OPERATOR":
		if node["type"] != "ARRAY_EXTRACT" {
			return Field{}, false
		}
		children, _ := node["children"].([]any)
		if len(children) != 2 {
			return Field{}, false
		}
		col, _ := children[0].(map[string]any)
		names, _ := col["column_names"].([]any)
		if col["class"] != "COLUMN_REF" || len(names) != 1 || (names[0] != "attributes" && names[0] != "resource") {
			return Field{}, false
		}
		v, ok := liftSQLValue(children[1])
		if !ok || v.Kind != StringValue {
			return Field{}, false
		}
		column, _ := names[0].(string)
		return Field{Scope: column, Key: v.Text, Bracket: true, Ref: &FieldRef{Column: column, Key: v.Text, Type: panel.TypeString, Unit: "none"}}, true
	}
	return Field{}, false
}

func liftSQLValue(n any) (Value, bool) {
	node, _ := n.(map[string]any)
	if node == nil {
		return Value{}, false
	}
	if node["class"] == "CAST" {
		typ, _ := node["type_expr"].(map[string]any)
		if typ["type_name"] == "TIMESTAMPTZ_NS" {
			return liftSQLValue(node["child"])
		}
		return Value{}, false
	}
	if node["class"] == "PARAMETER" {
		id, _ := node["identifier"].(string)
		if id != "" {
			return Value{Kind: VariableValue, Text: "$" + id, Stored: "$" + id}, true
		}
		return Value{}, false
	}
	if node["class"] != "CONSTANT" {
		return Value{}, false
	}
	literal, _ := node["literal"].(map[string]any)
	kind, _ := literal["kind"].(string)
	text, _ := literal["text"].(string)
	if text == "" {
		if v, ok := literal["value"]; ok {
			text = fmt.Sprint(v)
		}
	}
	switch kind {
	case "STRING":
		return Value{Kind: StringValue, Text: text, Stored: text}, true
	case "INTEGER", "NUMERIC", "DECIMAL", "FLOAT", "DOUBLE":
		return Value{Kind: NumberValue, Text: text, Stored: text}, true
	case "BOOLEAN":
		return Value{Kind: BooleanValue, Text: strings.ToLower(text), Stored: strings.ToLower(text)}, true
	}
	return Value{}, false
}

func spellingValue(f Field, v Value) Value {
	if v.Kind == VariableValue {
		return v
	}
	if f.Ref.Column == "duration_ms" && f.Ref.Key == "" && v.Kind == NumberValue {
		if n, err := strconv.ParseFloat(v.Text, 64); err == nil {
			v.Kind = DurationValue
			v.Text = durationText(n)
		}
		return v
	}
	if v.Kind == StringValue && f.Ref.Key == "" && f.Ref.Type == panel.TypeString {
		switch f.Ref.Column {
		case "status":
			if strings.HasPrefix(v.Text, "STATUS_CODE_") {
				v.Kind = WordValue
				v.Text = strings.ToLower(strings.TrimPrefix(v.Text, "STATUS_CODE_"))
				return v
			}
		case "kind":
			if strings.HasPrefix(v.Text, "SPAN_KIND_") {
				v.Kind = WordValue
				v.Text = strings.ToLower(strings.TrimPrefix(v.Text, "SPAN_KIND_"))
				return v
			}
		case "severity":
			if v.Text == strings.ToUpper(v.Text) && classify(Token{Kind: Word, Text: strings.ToLower(v.Text)}).Kind == WordValue {
				v.Kind = WordValue
				v.Text = strings.ToLower(v.Text)
				return v
			}
		}
		if f.Ref.Column != "status" && f.Ref.Column != "kind" && f.Ref.Column != "severity" && f.Ref.Column != "type" && f.Ref.CompareAs == "" {
			if numberPattern.MatchString(v.Text) {
				v.Kind = NumberValue
				return v
			}
			if v.Text == "true" || v.Text == "false" {
				v.Kind = BooleanValue
				return v
			}
		}
		if f.Ref.Column == "status" || f.Ref.Column == "kind" || f.Ref.Column == "severity" {
			return v
		}
		if f.Ref.Column == "type" {
			if _, ok := typeAliases[v.Text]; !ok {
				return v
			}
		}
		if bareValue(v.Text) && !numberPattern.MatchString(v.Text) && !durationPattern.MatchString(v.Text) && !strings.EqualFold(v.Text, "true") && !strings.EqualFold(v.Text, "false") {
			v.Kind = WordValue
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
			return strconv.FormatFloat(ms/u.value, 'f', -1, 64) + u.name
		}
	}
	return strconv.FormatFloat(ms, 'f', -1, 64) + "ms"
}

func liftSQLNode(signal string, n any) Expr {
	node, _ := n.(map[string]any)
	if node == nil {
		return nil
	}
	children, _ := node["children"].([]any)
	switch node["class"] {
	case "CONJUNCTION":
		terms := make([]Expr, len(children))
		for i, c := range children {
			terms[i] = liftSQLNode(signal, c)
			if terms[i] == nil {
				return nil
			}
		}
		if node["type"] == "CONJUNCTION_OR" {
			if len(terms) == 2 {
				if x := liftNullInclusive(terms[0], terms[1]); x != nil {
					return x
				}
				if x := liftNullInclusive(terms[1], terms[0]); x != nil {
					return x
				}
			}
			return &Or{Terms: terms}
		}
		return &And{Terms: terms}
	case "COMPARISON":
		f, ok := liftSQLField(signal, node["left"], false)
		v, valid := liftSQLValue(node["right"])
		if ok && valid {
			op := map[string]string{"COMPARE_EQUAL": ":", "COMPARE_NOTEQUAL": "!=", "COMPARE_LESSTHAN": "<", "COMPARE_GREATERTHAN": ">", "COMPARE_LESSTHANOREQUALTO": "<=", "COMPARE_GREATERTHANOREQUALTO": ">="}[fmt.Sprint(node["type"])]
			if op != "" {
				return &Compare{Field: f, Op: op, Value: spellingValue(f, v)}
			}
		}
		if call := liftSQLCall(signal, node["left"]); call != nil && valid {
			op := map[string]string{"COMPARE_EQUAL": "=", "COMPARE_NOTEQUAL": "!=", "COMPARE_LESSTHAN": "<", "COMPARE_GREATERTHAN": ">", "COMPARE_LESSTHANOREQUALTO": "<=", "COMPARE_GREATERTHANOREQUALTO": ">="}[fmt.Sprint(node["type"])]
			if op != "" {
				call.Cmp = &Comparison{Op: op, Value: v}
				return call
			}
		}
	case "BETWEEN":
		f, ok := liftSQLField(signal, node["input"], false)
		lo, a := liftSQLValue(node["lower"])
		hi, b := liftSQLValue(node["upper"])
		if ok && a && b {
			neg, _ := node["negated"].(bool)
			return &Between{Field: f, Lo: spellingValue(f, lo), Hi: spellingValue(f, hi), Negated: neg}
		}
	case "OPERATOR":
		switch node["type"] {
		case "OPERATOR_IS_NULL", "OPERATOR_IS_NOT_NULL":
			if len(children) == 1 {
				if f, ok := liftSQLField(signal, children[0], false); ok {
					return &IsNull{Field: f, Negated: node["type"] == "OPERATOR_IS_NOT_NULL"}
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
			in := &In{Field: f, Negated: node["type"] == "COMPARE_NOT_IN"}
			for _, c := range children[1:] {
				v, ok := liftSQLValue(c)
				if !ok {
					return nil
				}
				in.Values = append(in.Values, spellingValue(f, v))
			}
			if len(in.Values) == 1 && in.Values[0].Kind == VariableValue {
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
			inner, _ := children[0].(map[string]any)
			if inner["class"] == "OPERATOR" && inner["type"] == "OPERATOR_COALESCE" {
				parts, _ := inner["children"].([]any)
				if len(parts) == 2 {
					v, ok := liftSQLValue(parts[1])
					if ok && v.Kind == BooleanValue && v.Text == "false" {
						if e := liftSQLNode(signal, parts[0]); e != nil {
							return &Not{X: e}
						}
					}
				}
			}
			if inner["class"] == "BETWEEN" {
				if e, ok := liftSQLNode(signal, children[0]).(*Between); ok {
					e.Negated = !e.Negated
					return e
				}
			}
			if e := liftSQLNode(signal, children[0]); e != nil {
				return &Not{X: e}
			}
		}
	case "FUNCTION":
		name, _ := node["function_name"].(string)
		args := sqlFunctionArgs(node)
		if name == "contains" && len(args) == 2 {
			if v, ok := liftSQLValue(args[0]); ok && v.Kind == VariableValue {
				if f, ok := liftSQLField(signal, args[1], false); ok {
					return &In{Field: f, Variable: strings.TrimPrefix(v.Text, "$")}
				}
			}
			left, lok := unwrapFunction(args[0], "lower")
			right, rok := unwrapFunction(args[1], "lower")
			if lok && rok {
				if f, ok := liftSQLField(signal, left, true); ok {
					if v, ok := liftSQLValue(right); ok {
						if v.Kind != VariableValue && f.Ref.Key == "" && (signal == "logs" && f.Ref.Column == "body" || signal == "spans" && f.Ref.Column == "operation") {
							if v.Kind == StringValue && bareValue(v.Text) {
								v.Kind = WordValue
							}
							return &FreeText{Value: v, column: f.Ref.Column}
						}
						if v.Kind == StringValue && bareValue(v.Text) {
							v.Kind = WordValue
						}
						return &Compare{Field: f, Op: "~", Value: v}
					}
				}
			}
		}
		if (name == "~~" || name == "!~~" || name == "~~*" || name == "!~~*") && len(args) == 2 {
			f, ok := liftSQLField(signal, args[0], false)
			v, valid := liftSQLValue(args[1])
			if ok && valid {
				return &Like{Field: f, Pattern: v, Negated: strings.HasPrefix(name, "!"), Insensitive: strings.HasSuffix(name, "*")}
			}
		}
		if call := liftSQLCall(signal, n); call != nil {
			return call
		}
	}
	return nil
}

func liftSQLCall(signal string, n any) *Call {
	node, _ := n.(map[string]any)
	if node["class"] != "FUNCTION" {
		return nil
	}
	name, _ := node["function_name"].(string)
	// String transforms used as operands deliberately stay raw. Predicate calls
	// and length comparisons are precisely representable and pass the gate.
	if name != "starts_with" && name != "contains" && name != "regexp_matches" && name != "length" {
		return nil
	}
	args := sqlFunctionArgs(node)
	out := &Call{Name: name}
	for _, a := range args {
		if f, ok := liftSQLField(signal, a, false); ok {
			out.Args = append(out.Args, CallArg{Field: &f})
		} else if v, ok := liftSQLValue(a); ok {
			out.Args = append(out.Args, CallArg{Value: &v})
		} else {
			return nil
		}
	}
	return out
}
func sqlFunctionArgs(n map[string]any) []any {
	entries, _ := n["arguments"].([]any)
	out := make([]any, 0, len(entries))
	for _, entry := range entries {
		m, _ := entry.(map[string]any)
		out = append(out, m["expression"])
	}
	return out
}
func unwrapFunction(n any, name string) (any, bool) {
	node, _ := n.(map[string]any)
	if node["class"] != "FUNCTION" || node["function_name"] != name {
		return nil, false
	}
	args := sqlFunctionArgs(node)
	if len(args) != 1 {
		return nil, false
	}
	return args[0], true
}
func sameLiftedField(a, b Field) bool {
	return a.Ref != nil && b.Ref != nil && a.Ref.Column == b.Ref.Column && a.Ref.Key == b.Ref.Key
}
func liftNullInclusive(null, other Expr) Expr {
	is, ok := null.(*IsNull)
	if !ok || is.Negated {
		return nil
	}
	switch x := other.(type) {
	case *Compare:
		if len(x.Bands) > 0 && sameLiftedField(is.Field, x.Field) {
			return &Not{X: bandMembership(x.Field, x.Bands, true)}
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
		return &Not{X: &c}
	case *In:
		if sameLiftedField(is.Field, x.Field) {
			c := *x
			c.Negated = !c.Negated
			return &Not{X: &c}
		}
	case *Between:
		if len(x.Bands) > 0 && sameLiftedField(is.Field, x.Field) {
			return &Not{X: bandMembership(x.Field, x.Bands, !x.Negated)}
		}
		if sameLiftedField(is.Field, x.Field) {
			c := *x
			c.Negated = !c.Negated
			return &Not{X: &c}
		}
	case *Like:
		if sameLiftedField(is.Field, x.Field) {
			c := *x
			c.Negated = !c.Negated
			return &Not{X: &c}
		}
	case *Not:
		switch c := x.X.(type) {
		case *Compare:
			if c.Op == "~" && sameLiftedField(is.Field, c.Field) {
				return &Not{X: c}
			}
		case *FreeText:
			if is.Field.Ref.Key == "" && (is.Field.Ref.Column == "body" || is.Field.Ref.Column == "operation") {
				return &Not{X: c}
			}
		}
	}
	return nil
}
func bandMembership(f Field, bands []string, negated bool) *In {
	in := &In{Field: f, Negated: negated}
	for _, band := range bands {
		in.Values = append(in.Values, Value{Kind: WordValue, Text: strings.ToLower(band), Stored: band})
	}
	return in
}

func liftSeverityRange(in *In) Expr {
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
	value := func(rank int) Value {
		band := telemetry.SeverityBands[rank]
		return Value{Kind: WordValue, Text: strings.ToLower(band), Stored: band}
	}
	bands := make([]string, len(ranks))
	for i, r := range ranks {
		bands[i] = telemetry.SeverityBands[r]
	}
	if last == len(telemetry.SeverityBands)-1 {
		return &Compare{Field: in.Field, Op: ">=", Value: value(first), Bands: bands}
	}
	if first == 0 {
		return &Compare{Field: in.Field, Op: "<=", Value: value(last), Bands: bands}
	}
	return &Between{Field: in.Field, Lo: value(first), Hi: value(last), Bands: bands}
}

func liftMeasure(ctx context.Context, p panel.Parser, signal, stored string) (Measure, tier, error) {
	// Count-if bodies are stored SQL, while the surrounding expression already
	// uses FanQL's measure grammar. Lift the bodies before invoking that parser.
	converted, err := liftCountIfBodies(ctx, p, signal, stored)
	if err != nil {
		return Measure{}, tierRaw, err
	}
	text := signal + " | " + converted
	parser := &parser{runes: []rune(text), tokens: Lex(text), heights: make(map[any]int)}
	q := parser.query("")
	if ps := parser.finish(); len(ps) > 0 || q.Stage == nil || len(q.Stage.Measures) != 1 {
		return Measure{}, tierRaw, formatInput("measure %q cannot be expressed in FanQL", stored)
	}
	if _, ps := Resolve(q, ResolveOptions{}); len(ps) > 0 {
		return Measure{}, tierRaw, formatInput("measure %q cannot be expressed in FanQL: %s", stored, ps[0].Message)
	}
	m := q.Stage.Measures[0]
	lowered := lowerMeasure(m)
	if lowered == stored {
		return m, tierExact, nil
	}
	a, errA := panel.ParseMeasure(signal, lowered)
	b, errB := panel.ParseMeasure(signal, stored)
	if errA == nil && errB == nil && a.Func == b.Func && reflect.DeepEqual(a.Field, b.Field) && a.Q == b.Q && a.Alias == b.Alias {
		return m, tierNormalized, nil
	}
	return Measure{}, tierRaw, formatInput("measure %q cannot be formatted without changing its parsed meaning", stored)
}
func liftCountIfBodies(ctx context.Context, p panel.Parser, signal, text string) (string, error) {
	tokens := ownSQLTokens(text)
	var b strings.Builder
	end := 0
	for i := 0; i+1 < len(tokens); i++ {
		if !strings.EqualFold(tokens[i].text, "count_if") || tokens[i+1].text != "(" {
			continue
		}
		depth := 1
		j := i + 2
		for ; j < len(tokens); j++ {
			if tokens[j].text == "(" {
				depth++
			}
			if tokens[j].text == ")" {
				depth--
				if depth == 0 {
					break
				}
			}
		}
		if j == len(tokens) {
			return "", formatInput("unclosed count_if")
		}
		body := text[tokens[i+1].end:tokens[j].start]
		// A count_if conjunction has no entry-level outer parentheses.
		// Recognize that exact emitted production without consulting the engine.
		reader := &ownSQLReader{tokens: ownSQLTokens(body)}
		node := reader.expression(0)
		var e Expr
		if !reader.failed && reader.pos == len(reader.tokens) {
			candidate := liftSQLNode(signal, node)
			if candidate != nil && lowerMExpr(&MCountIf{Filter: candidate}, 0) == "count_if("+body+")" {
				e = candidate
			}
		}
		if e == nil {
			var err error
			e, _, err = liftEntry(ctx, p, signal, body)
			if err != nil {
				return "", err
			}
		}
		b.WriteString(text[end:tokens[i+1].end])
		b.WriteString(printEntry(e, false))
		end = tokens[j].start
		i = j
	}
	b.WriteString(text[end:])
	return b.String(), nil
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
		if c == '\'' || c == '"' {
			quote := c
			i++
			var b strings.Builder
			for i < len(text) {
				if text[i] == quote {
					if i+1 < len(text) && text[i+1] == quote {
						b.WriteByte(quote)
						i += 2
						continue
					}
					i++
					break
				}
				b.WriteByte(text[i])
				i++
			}
			out = append(out, ownSQLToken{text: b.String(), quote: quote, start: start, end: i})
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
func (r *ownSQLReader) expression(min int) map[string]any {
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
		if left["class"] == "CONJUNCTION" && left["type"] == kind && left["own_group"] != true {
			children, _ := left["children"].([]any)
			left["children"] = append(children, right)
		} else {
			left = map[string]any{"class": "CONJUNCTION", "type": kind, "children": []any{left, right}}
		}
	}
	return left
}
func (r *ownSQLReader) predicate() map[string]any {
	if r.eat("NOT") {
		return ownOperator("OPERATOR_NOT", r.predicate())
	}
	left := r.atom()
	op := r.peek()
	if kind := map[string]string{"=": "COMPARE_EQUAL", "!=": "COMPARE_NOTEQUAL", "<>": "COMPARE_NOTEQUAL", "<": "COMPARE_LESSTHAN", ">": "COMPARE_GREATERTHAN", "<=": "COMPARE_LESSTHANOREQUALTO", ">=": "COMPARE_GREATERTHANOREQUALTO"}[op]; kind != "" {
		r.pos++
		return map[string]any{"class": "COMPARISON", "type": kind, "left": left, "right": r.atom()}
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
		children := []any{left}
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
		return map[string]any{"class": "BETWEEN", "type": "COMPARE_BETWEEN", "input": left, "lower": lo, "upper": hi, "negated": neg}
	}
	op = r.peek()
	if op == "~~" || op == "!~~" || op == "~~*" || op == "!~~*" {
		r.pos++
		return ownFunction(strings.ToLower(op), []any{left, r.atom()})
	}
	if neg {
		r.failed = true
	}
	return left
}
func (r *ownSQLReader) atom() map[string]any {
	if r.pos >= len(r.tokens) {
		r.failed = true
		return nil
	}
	if r.eat("(") {
		node := r.expression(0)
		r.need(")")
		if node != nil {
			node["own_group"] = true
		}
		return node
	}
	token := r.tokens[r.pos]
	r.pos++
	var node map[string]any
	switch {
	case token.quote == '\'':
		node = ownConstant("STRING", token.text)
	case token.quote == '"':
		node = map[string]any{"class": "COLUMN_REF", "column_names": []any{token.text}}
	case strings.EqualFold(token.text, "true") || strings.EqualFold(token.text, "false"):
		node = ownConstant("BOOLEAN", strings.ToLower(token.text))
	case numberPattern.MatchString(token.text):
		node = ownConstant("NUMERIC", token.text)
	case strings.HasPrefix(token.text, "$"):
		node = map[string]any{"class": "PARAMETER", "identifier": strings.TrimPrefix(token.text, "$")}
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
			node = map[string]any{"class": "CAST", "type": "OPERATOR_CAST", "child": child, "try_cast": name == "try_cast", "type_expr": map[string]any{"type_name": typ}}
		} else {
			var args []any
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
		node = map[string]any{"class": "COLUMN_REF", "column_names": []any{token.text}}
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
		node = map[string]any{"class": "CAST", "child": node, "type_expr": map[string]any{"type_name": typ}}
	}
	return node
}
func ownConstant(kind, text string) map[string]any {
	return map[string]any{"class": "CONSTANT", "literal": map[string]any{"kind": kind, "text": text}}
}
func ownOperator(kind string, children ...any) map[string]any {
	return map[string]any{"class": "OPERATOR", "type": kind, "children": children}
}
func ownFunction(name string, args []any) map[string]any {
	entries := make([]any, len(args))
	for i, a := range args {
		entries[i] = map[string]any{"expression": a}
	}
	return map[string]any{"class": "FUNCTION", "function_name": name, "arguments": entries}
}
