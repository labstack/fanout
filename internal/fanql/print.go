package fanql

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/telemetry"
)

// Print prints the canonical spelling of a resolved tree. This spelling is
// frozen for Version; in particular, filter entry boundaries are preserved.
func print(q *queryTree) string {
	if q == nil {
		return ""
	}
	text := q.Source
	if len(q.Where) > 0 {
		entries := make([]string, len(q.Where))
		for i, e := range q.Where {
			entries[i] = printEntry(e, len(q.Where) > 1)
		}
		for i := 0; i+1 < len(entries); i++ {
			if strings.HasPrefix(entries[i+1], "(") {
				entries[i] = quoteSearchBeforeGroup(q.Where[i], entries[i])
			}
		}
		text += " where " + strings.Join(entries, " ")
	}
	if s := q.Stage; s != nil {
		text += " | "
		if s.Histogram != nil {
			text += "histogram(" + printField(*s.Histogram) + ")"
		} else {
			ms := make([]string, len(s.Measures))
			for i, m := range s.Measures {
				ms[i] = printMeasure(m)
			}
			text += strings.Join(ms, ", ")
		}
		if len(s.By) > 0 {
			by := make([]string, len(s.By))
			for i, f := range s.By {
				by[i] = printField(f)
			}
			text += " by " + strings.Join(by, ", ")
		}
		if s.Bucket != "" {
			text += " every " + s.Bucket
		}
	}
	if q.Sort != nil {
		text += " sort "
		if q.Sort.Ascending {
			text += "+"
		}
		key := q.Sort.Key
		if key == "duration_ms" && !sortIsMeasureAlias(q) {
			key = "duration"
		}
		text += key
	}
	if q.Limit != nil {
		text += " limit " + strconv.Itoa(q.Limit.N)
	}
	return text
}

func printEntry(e filterExpr, grouped bool) string {
	return printExpr(canonicalExpr(e, false), grouped)
}

// Canonical negation follows the lowerer's NNF so text with the same lowered
// form has a single spelling and can be lifted without origin metadata.
func canonicalExpr(e filterExpr, neg bool) filterExpr {
	switch x := e.(type) {
	case *notExpr:
		return canonicalExpr(x.X, !neg)
	case *andExpr:
		terms := make([]filterExpr, len(x.Terms))
		for i, t := range x.Terms {
			terms[i] = canonicalExpr(t, neg)
		}
		if neg {
			return &orExpr{Terms: terms, At: x.At}
		}
		return &andExpr{Terms: terms, At: x.At}
	case *orExpr:
		terms := make([]filterExpr, len(x.Terms))
		for i, t := range x.Terms {
			terms[i] = canonicalExpr(t, neg)
		}
		if neg {
			return &andExpr{Terms: terms, At: x.At}
		}
		return &orExpr{Terms: terms, At: x.At}
	case *inExpr:
		if !x.Negated && x.Variable == "" && x.Field.Ref != nil && x.Field.Ref.Column == "severity" && x.Field.Ref.Key == "" {
			if candidate := liftSeverityRange(x); candidate != x {
				return canonicalExpr(candidate, neg)
			}
		}
	case *betweenExpr:
		if x.Negated && len(x.Bands) > 0 {
			return canonicalExpr(bandMembership(x.Field, x.Bands, true), neg)
		}
	case *nullExpr:
		return canonicalExpr(&existsExpr{Field: x.Field, At: x.At}, neg != !x.Negated)
	case *comparison:
		if x.Op == "~" && x.Value.Kind != variableValue && x.Field.Ref != nil && x.Field.Ref.Key == "" && (x.Field.Ref.Column == "body" || x.Field.Ref.Column == "operation") {
			v := x.Value
			v.Kind = wordValue
			if v.Stored != "" {
				v.Text = v.Stored
			}
			return canonicalExpr(&textExpr{Value: v, column: x.Field.Ref.Column, At: x.At}, neg)
		}
		if x.Op == "!~" {
			copy := *x
			copy.Op = "~"
			return canonicalExpr(&copy, !neg)
		}
	}
	if neg {
		return &notExpr{X: e, At: e.Span()}
	}
	return e
}

func printExpr(e filterExpr, grouped bool) string {
	switch x := e.(type) {
	case *andExpr:
		terms := make([]string, len(x.Terms))
		for i, t := range x.Terms {
			terms[i] = printExpr(t, true)
		}
		for i := 0; i+1 < len(terms); i++ {
			if strings.HasPrefix(terms[i+1], "(") {
				terms[i] = quoteSearchBeforeGroup(x.Terms[i], terms[i])
			}
		}
		return "(" + strings.Join(terms, " ") + ")"
	case *orExpr:
		terms := make([]string, len(x.Terms))
		for i, t := range x.Terms {
			terms[i] = printExpr(t, true)
		}
		text := strings.Join(terms, " or ")
		if grouped {
			text = "(" + text + ")"
		}
		return text
	case *notExpr:
		odd := true
		inner := x.X
		for {
			n, ok := inner.(*notExpr)
			if !ok {
				break
			}
			odd = !odd
			inner = n.X
		}
		if !odd {
			return printExpr(inner, grouped)
		}
		return "-" + printExpr(inner, true)
	case *comparison:
		if len(x.Bands) > 0 {
			return printBandRange(x.Field, x.Bands)
		}
		op := x.Op
		if op == "=" {
			op = ":"
		}
		if op == "<>" {
			op = "!="
		}
		if op == "~" || op == "!~" {
			value := x.Value
			if value.Kind != variableValue {
				// Contains interprets every literal as text, including its exact
				// exponent spelling; numeric normalization would change the search.
				value.Kind = stringValue
				if value.Stored != "" {
					value.Text = value.Stored
				}
			}
			return printField(x.Field) + " " + op + " " + printValue(value, x.Field, false)
		}
		field, native := printFilterField(x.Field, x.Value)
		return field + op + printFilterValue(x.Value, x.Field, op == ":" || op == "=" || op == "!=" || op == "<>", native)
	case *inExpr:
		op := " in "
		if x.Negated {
			op = " not in "
		}
		if x.Variable != "" {
			return printField(x.Field) + op + "$" + x.Variable
		}
		vs := make([]string, len(x.Values))
		field, native := printFilterField(x.Field, x.Values...)
		for i, v := range x.Values {
			vs[i] = printFilterValue(v, x.Field, true, native)
		}
		return field + op + "(" + strings.Join(vs, ", ") + ")"
	case *betweenExpr:
		if len(x.Bands) > 0 && !x.Negated {
			return printBandRange(x.Field, x.Bands)
		}
		op := " between "
		if x.Negated {
			op = " not between "
		}
		field, native := printFilterField(x.Field, x.Lo, x.Hi)
		return field + op + printFilterValue(x.Lo, x.Field, false, native) + " and " + printFilterValue(x.Hi, x.Field, false, native)
	case *likeExpr:
		op := "like"
		if x.Insensitive {
			op = "ilike"
		}
		if x.Negated {
			op = "not " + op
		}
		return printField(x.Field) + " " + op + " " + quoteText(x.Pattern.Text)
	case *existsExpr:
		return printField(x.Field) + ":*"
	case *nullExpr:
		if x.Negated {
			return printField(x.Field) + ":*"
		}
		return "-" + printField(x.Field) + ":*"
	case *filterCall:
		return printCall(x)
	case *textExpr:
		return printLiteral(x.Value, false)
	case *rawEntry:
		return "`" + strings.ReplaceAll(x.Text, "`", "``") + "`"
	}
	return ""
}

// Canonical negation can introduce a following group. A function-shaped search
// word needs quotes only in that context, where it would become a spaced call.
func quoteSearchBeforeGroup(e filterExpr, text string) string {
	e = canonicalExpr(e, false)
	neg := false
	if n, ok := e.(*notExpr); ok {
		neg, e = true, n.X
	}
	if x, ok := e.(*textExpr); ok {
		for _, name := range panel.FilterFunctionNames() {
			if strings.EqualFold(x.Value.Text, name) {
				text = quoteText(x.Value.Text)
				if neg {
					text = "-" + text
				}
				return text
			}
		}
	}
	return text
}

func printField(f fieldName) string {
	column, key := f.Key, ""
	if f.Ref != nil {
		column, key = f.Ref.Column, f.Ref.Key
	} else if f.Scope != "" {
		column, key = f.Scope, f.Key
	} else if !f.Single {
		column, key = "attributes", f.Key
	}
	if key == "" {
		if column == "duration_ms" {
			return "duration"
		}
		return column
	}
	if column == "attributes" && strings.Contains(key, ".") && !strings.HasPrefix(strings.ToLower(key), "resource.") && !strings.HasPrefix(strings.ToLower(key), "attributes.") && validPath(key) {
		return key
	}
	if column == "resource" && validPath(key) {
		return "resource." + key
	}
	return column + "['" + strings.ReplaceAll(key, "'", "''") + "']"
}

func validPath(s string) bool { return identifierPath.MatchString(s) }

func quoteText(s string) string { return `"` + strings.ReplaceAll(s, `"`, `""`) + `"` }

// The explicit numeric column accepts negative numbers; the duration shorthand
// forbids negative units and uses float64 milliseconds. Keep the numeric field
// for a whole list/range if a value cannot use that shorthand exactly.
func printFilterField(f fieldName, values ...literalValue) (string, bool) {
	if f.Ref != nil && f.Ref.Column == "duration_ms" && f.Ref.Key == "" {
		for _, v := range values {
			if v.Kind == numberValue {
				if n, err := strconv.ParseFloat(v.Text, 64); err == nil && (n < 0 || decimal(n) != numberText(v.Text)) {
					return "duration_ms", true
				}
			}
		}
	}
	return printField(f), false
}

func printFilterValue(v literalValue, f fieldName, aliases, nativeDuration bool) string {
	if nativeDuration && v.Kind == numberValue {
		return printLiteral(v, false)
	}
	return printValue(v, f, aliases)
}

func printValue(v literalValue, f fieldName, aliases bool) string {
	// In alias-bearing positions, unresolved fields cannot prove that removing
	// the user's quotes preserves the value. Keep them until that proof exists.
	if aliases && v.Kind == stringValue && f.Ref == nil {
		return quoteText(v.Text)
	}
	if v.Kind == numberValue && f.Ref != nil && f.Ref.Column == "duration_ms" && f.Ref.Key == "" {
		if n, err := strconv.ParseFloat(v.Text, 64); err == nil {
			return durationText(n)
		}
	}
	if aliases && v.Kind != variableValue {
		stored := v.Stored
		if stored == "" {
			stored = v.Text
		}
		column := f.Key
		if f.Ref != nil {
			column = f.Ref.Column
			if f.Ref.Key != "" {
				column = ""
			}
		}
		for alias, value := range panel.FieldAliases(column) {
			if stored == value {
				return alias
			}
		}
		switch column {
		case "severity":
			if v.Kind != stringValue || telemetry.SeverityFromText(stored) == stored {
				alias := strings.ToLower(stored)
				// Numeric, boolean and duration-shaped tokens are literals, not
				// vocabulary words. Keep quoted strings' spelling in those cases.
				if classify(lexToken{Kind: tokenWord, Text: alias}).Kind == wordValue {
					if bareValue(alias) {
						return alias
					}
					return quoteText(stored)
				}
			}
		}
	}
	if v.Kind == numberValue && f.Ref != nil && f.Ref.Type == "string" && f.Ref.Key == "" && f.Ref.CompareAs == "" {
		return v.Text
	}
	if v.Kind == stringValue {
		if f.Ref != nil && f.Ref.Key == "" && f.Ref.Type == "string" && f.Ref.CompareAs == "" {
			if numberPattern.MatchString(v.Text) {
				if _, err := strconv.ParseFloat(v.Text, 64); err == nil {
					return v.Text
				}
			}
			if v.Text == "true" || v.Text == "false" {
				return strings.ToLower(v.Text)
			}
		}
		// A quoted value can deliberately bypass vocabulary mapping or force a
		// string rather than a numeric/boolean attribute literal.
		column := ""
		if f.Ref != nil && f.Ref.Key == "" {
			column = f.Ref.Column
		}
		if (!aliases || column != "status" && column != "kind" && column != "severity" && column != "type") && (f.Ref == nil || f.Ref.Type != "time") && bareValue(v.Text) && !numberPattern.MatchString(v.Text) && !durationPattern.MatchString(v.Text) && !strings.EqualFold(v.Text, "true") && !strings.EqualFold(v.Text, "false") {
			return v.Text
		}
		return quoteText(v.Text)
	}
	return printLiteral(v, false)
}

func printLiteral(v literalValue, forceQuote bool) string {
	if v.Kind == variableValue {
		return v.Text
	}
	if v.Kind == numberValue {
		return numberText(v.Text)
	}
	if v.Kind == booleanValue {
		return strings.ToLower(v.Text)
	}
	if v.Kind == durationValue {
		if ms, ok := durationMS(v.Text); ok {
			return durationText(ms)
		}
		return strings.ToLower(v.Text)
	}
	if forceQuote || !bareValue(v.Text) {
		return quoteText(v.Text)
	}
	return v.Text
}

func bareValue(s string) bool {
	if s == "" || strings.ContainsRune("-+$*", rune(s[0])) {
		return false
	}
	if clauses[strings.ToLower(s)] || expressionWords[strings.ToLower(s)] || modifiers[strings.ToLower(s)] || reserved[strings.ToLower(s)] {
		return false
	}
	for _, r := range s {
		if unicode.IsSpace(r) || strings.ContainsRune("|(),:=!<>~\"'`[]#{}@$*", r) {
			return false
		}
	}
	return true
}

func printCall(x *filterCall) string {
	args := make([]string, len(x.Args))
	for i, a := range x.Args {
		switch {
		case a.Field != nil:
			args[i] = printField(*a.Field)
		case a.Value != nil:
			args[i] = quoteText(a.Value.Text)
			if a.Value.Kind == numberValue {
				args[i] = printLiteral(*a.Value, false)
			}
		case a.Call != nil:
			args[i] = printCall(a.Call)
		}
	}
	text := x.Name + "(" + strings.Join(args, ", ") + ")"
	if x.Cmp != nil {
		value := printLiteral(x.Cmp.Value, false)
		if x.Cmp.Value.Kind == stringValue {
			value = quoteText(x.Cmp.Value.Text)
		}
		text += x.Cmp.Op + value
	}
	return text
}

func printMeasure(m measureNode) string {
	text := renderMeasure(m.Parsed.Expr, 0, func(f *panel.FieldRef) string {
		return printField(fieldName{Key: f.Text, Ref: &fieldRef{Column: f.Column, Key: f.Key, Type: f.Type, Unit: f.Unit}})
	}, func(sql string) string {
		e := m.Filters[sql]
		if e == nil {
			return "`" + strings.ReplaceAll(sql, "`", "``") + "`"
		}
		body := printEntry(e, false)
		if _, ok := e.(*andExpr); ok {
			body = strings.TrimSuffix(strings.TrimPrefix(body, "("), ")")
		}
		return body
	}, m.Info)
	if m.Alias != "" {
		text += " as " + m.Alias
	}
	return text
}
func renderMeasure(e panel.MeasureExpr, parent int, field func(*panel.FieldRef) string, filter func(string) string, syntax ...map[panel.MeasureExpr]*measureSource) string {
	if len(syntax) > 0 {
		if info := syntax[0][e]; info != nil {
			switch x := e.(type) {
			case panel.MeasureCall:
				var args []string
				for _, a := range info.args {
					switch {
					case a.Field != nil:
						args = append(args, printField(*a.Field))
					case a.Number != nil:
						args = append(args, decimal(*a.Number))
					case a.Star:
						if x.Func != "count" {
							args = append(args, "*")
						}
					}
				}
				return x.Func + "(" + strings.Join(args, ", ") + ")"
			case panel.MeasureCountIf:
				body := printEntry(info.filter, false)
				if _, ok := info.filter.(*andExpr); ok {
					body = strings.TrimSuffix(strings.TrimPrefix(body, "("), ")")
				}
				return "count_if(" + body + ")"
			}
		}
	}
	switch x := e.(type) {
	case panel.MeasureCall:
		var args []string
		if x.Field != nil {
			args = append(args, field(x.Field))
		}
		if x.Func == "quantile" {
			args = append(args, strconv.FormatFloat(x.Q, 'f', -1, 64))
		}
		return x.Func + "(" + strings.Join(args, ", ") + ")"
	case panel.MeasureCountIf:
		return "count_if(" + filter(x.Filter) + ")"
	case panel.MeasureConst:
		return strconv.FormatFloat(x.Value, 'f', -1, 64)
	case panel.MeasureNeg:
		if _, nested := x.X.(panel.MeasureNeg); nested {
			return "-(" + renderMeasure(x.X, 0, field, filter, syntax...) + ")"
		}
		return "-" + renderMeasure(x.X, 3, field, filter, syntax...)
	case panel.MeasureBinary:
		prec := 1
		if x.Op == '*' || x.Op == '/' {
			prec = 2
		}
		text := renderMeasure(x.L, prec, field, filter, syntax...) + " " + string(x.Op) + " " + renderMeasure(x.R, prec+1, field, filter, syntax...)
		if prec < parent {
			return "(" + text + ")"
		}
		return text
	}
	return ""
}

func printBandRange(f fieldName, bands []string) string {
	first, last := strings.ToLower(bands[0]), strings.ToLower(bands[len(bands)-1])
	field := printField(f)
	if last == "fatal" {
		return field + ">=" + first
	}
	if first == "trace" {
		return field + "<=" + last
	}
	return field + " between " + first + " and " + last
}
