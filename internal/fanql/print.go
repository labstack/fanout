package fanql

import (
	"strconv"
	"strings"
	"unicode"

	"github.com/labstack/fanout/internal/telemetry"
)

func init() { resolvePrint = Print }

// Print prints the canonical spelling of a resolved tree. This spelling is
// frozen for Version; in particular, filter entry boundaries are preserved.
func Print(q *Query) string {
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

func printEntry(e Expr, grouped bool) string { return printExpr(canonicalExpr(e, false), grouped) }

// Canonical negation follows the lowerer's NNF so text with the same lowered
// form has a single spelling and can be lifted without origin metadata.
func canonicalExpr(e Expr, neg bool) Expr {
	switch x := e.(type) {
	case *Not:
		return canonicalExpr(x.X, !neg)
	case *And:
		terms := make([]Expr, len(x.Terms))
		for i, t := range x.Terms {
			terms[i] = canonicalExpr(t, neg)
		}
		if neg {
			return &Or{Terms: terms, At: x.At}
		}
		return &And{Terms: terms, At: x.At}
	case *Or:
		terms := make([]Expr, len(x.Terms))
		for i, t := range x.Terms {
			terms[i] = canonicalExpr(t, neg)
		}
		if neg {
			return &And{Terms: terms, At: x.At}
		}
		return &Or{Terms: terms, At: x.At}
	case *In:
		if !x.Negated && x.Variable == "" && x.Field.Ref != nil && x.Field.Ref.Column == "severity" && x.Field.Ref.Key == "" {
			if candidate := liftSeverityRange(x); candidate != x {
				return canonicalExpr(candidate, neg)
			}
		}
	case *Between:
		if x.Negated && len(x.Bands) > 0 {
			return canonicalExpr(bandMembership(x.Field, x.Bands, true), neg)
		}
	case *IsNull:
		return canonicalExpr(&Exists{Field: x.Field, At: x.At}, neg != !x.Negated)
	case *Compare:
		if x.Op == "~" && x.Value.Kind != VariableValue && x.Field.Ref != nil && x.Field.Ref.Key == "" && (x.Field.Ref.Column == "body" || x.Field.Ref.Column == "operation") {
			v := x.Value
			v.Kind = WordValue
			if v.Stored != "" {
				v.Text = v.Stored
			}
			return canonicalExpr(&FreeText{Value: v, column: x.Field.Ref.Column, At: x.At}, neg)
		}
		if x.Op == "!~" {
			copy := *x
			copy.Op = "~"
			return canonicalExpr(&copy, !neg)
		}
	}
	if neg {
		return &Not{X: e, At: e.Span()}
	}
	return e
}

func printExpr(e Expr, grouped bool) string {
	switch x := e.(type) {
	case *And:
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
	case *Or:
		terms := make([]string, len(x.Terms))
		for i, t := range x.Terms {
			terms[i] = printExpr(t, true)
		}
		text := strings.Join(terms, " or ")
		if grouped {
			text = "(" + text + ")"
		}
		return text
	case *Not:
		odd := true
		inner := x.X
		for {
			n, ok := inner.(*Not)
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
	case *Compare:
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
			if value.Kind != VariableValue {
				// Contains interprets every literal as text, including its exact
				// exponent spelling; numeric normalization would change the search.
				value.Kind = StringValue
				if value.Stored != "" {
					value.Text = value.Stored
				}
			}
			return printField(x.Field) + " " + op + " " + printValue(value, x.Field, false)
		}
		field, native := printFilterField(x.Field, x.Value)
		return field + op + printFilterValue(x.Value, x.Field, op == ":" || op == "=" || op == "!=" || op == "<>", native)
	case *In:
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
	case *Between:
		if len(x.Bands) > 0 && !x.Negated {
			return printBandRange(x.Field, x.Bands)
		}
		op := " between "
		if x.Negated {
			op = " not between "
		}
		field, native := printFilterField(x.Field, x.Lo, x.Hi)
		return field + op + printFilterValue(x.Lo, x.Field, false, native) + " and " + printFilterValue(x.Hi, x.Field, false, native)
	case *Like:
		op := "like"
		if x.Insensitive {
			op = "ilike"
		}
		if x.Negated {
			op = "not " + op
		}
		return printField(x.Field) + " " + op + " " + quoteText(x.Pattern.Text)
	case *Exists:
		return printField(x.Field) + ":*"
	case *IsNull:
		if x.Negated {
			return printField(x.Field) + ":*"
		}
		return "-" + printField(x.Field) + ":*"
	case *Call:
		return printCall(x)
	case *FreeText:
		return printLiteral(x.Value, false)
	case *RawExpr:
		return "`" + strings.ReplaceAll(x.Text, "`", "``") + "`"
	}
	return ""
}

// Canonical negation can introduce a following group. A function-shaped search
// word needs quotes only in that context, where it would become a spaced call.
func quoteSearchBeforeGroup(e Expr, text string) string {
	e = canonicalExpr(e, false)
	neg := false
	if n, ok := e.(*Not); ok {
		neg, e = true, n.X
	}
	if x, ok := e.(*FreeText); ok {
		for _, name := range resolveFilterFunctions() {
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

func printField(f Field) string {
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
// forbids negative units. Keep the numeric field for a whole mixed list/range.
func printFilterField(f Field, values ...Value) (string, bool) {
	if f.Ref != nil && f.Ref.Column == "duration_ms" && f.Ref.Key == "" {
		for _, v := range values {
			if v.Kind == NumberValue {
				if n, err := strconv.ParseFloat(v.Text, 64); err == nil && n < 0 {
					return "duration_ms", true
				}
			}
		}
	}
	return printField(f), false
}

func printFilterValue(v Value, f Field, aliases, nativeDuration bool) string {
	if nativeDuration && v.Kind == NumberValue {
		return printLiteral(v, false)
	}
	return printValue(v, f, aliases)
}

func printValue(v Value, f Field, aliases bool) string {
	// In alias-bearing positions, unresolved fields cannot prove that removing
	// the user's quotes preserves the value. Keep them until that proof exists.
	if aliases && v.Kind == StringValue && f.Ref == nil {
		return quoteText(v.Text)
	}
	if v.Kind == NumberValue && f.Ref != nil && f.Ref.Column == "duration_ms" && f.Ref.Key == "" {
		if n, err := strconv.ParseFloat(v.Text, 64); err == nil {
			return durationText(n)
		}
	}
	if aliases && v.Kind != VariableValue {
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
		switch column {
		case "status":
			for alias, value := range statusAliases {
				if stored == value {
					return alias
				}
			}
		case "kind":
			for alias, value := range kindAliases {
				if stored == value {
					return alias
				}
			}
		case "severity":
			if v.Kind != StringValue || telemetry.SeverityFromText(stored) == stored {
				alias := strings.ToLower(stored)
				// Numeric, boolean and duration-shaped tokens are literals, not
				// vocabulary words. Keep quoted strings' spelling in those cases.
				if classify(Token{Kind: Word, Text: alias}).Kind == WordValue {
					if bareValue(alias) {
						return alias
					}
					return quoteText(stored)
				}
			}
		case "type":
			for alias, value := range typeAliases {
				if stored == value {
					return alias
				}
			}
		}
	}
	if v.Kind == NumberValue && f.Ref != nil && f.Ref.Type == "string" && f.Ref.Key == "" && f.Ref.CompareAs == "" {
		return v.Text
	}
	if v.Kind == StringValue {
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

func printLiteral(v Value, forceQuote bool) string {
	if v.Kind == VariableValue {
		return v.Text
	}
	if v.Kind == NumberValue {
		return numberText(v.Text)
	}
	if v.Kind == BooleanValue {
		return strings.ToLower(v.Text)
	}
	if v.Kind == DurationValue {
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

func printCall(x *Call) string {
	args := make([]string, len(x.Args))
	for i, a := range x.Args {
		switch {
		case a.Field != nil:
			args[i] = printField(*a.Field)
		case a.Value != nil:
			args[i] = quoteText(a.Value.Text)
			if a.Value.Kind == NumberValue {
				args[i] = printLiteral(*a.Value, false)
			}
		case a.Call != nil:
			args[i] = printCall(a.Call)
		}
	}
	text := x.Name + "(" + strings.Join(args, ", ") + ")"
	if x.Cmp != nil {
		value := printLiteral(x.Cmp.Value, false)
		if x.Cmp.Value.Kind == StringValue {
			value = quoteText(x.Cmp.Value.Text)
		}
		text += x.Cmp.Op + value
	}
	return text
}

func printMeasure(m Measure) string {
	text := printMExpr(m.Expr, 0)
	if m.Alias != "" {
		text += " as " + m.Alias
	}
	return text
}

func printMExpr(e MExpr, parent int) string {
	switch x := e.(type) {
	case *MCall:
		if x.Name == "count" && len(x.Args) == 1 && x.Args[0].Star {
			return "count()"
		}
		args := make([]string, len(x.Args))
		for i, a := range x.Args {
			switch {
			case a.Field != nil:
				args[i] = printField(*a.Field)
			case a.Number != nil:
				args[i] = strconv.FormatFloat(*a.Number, 'f', -1, 64)
			case a.Star:
				args[i] = "*"
			}
		}
		return x.Name + "(" + strings.Join(args, ", ") + ")"
	case *MCountIf:
		body := printEntry(x.Filter, false)
		if _, ok := x.Filter.(*And); ok {
			body = strings.TrimSuffix(strings.TrimPrefix(body, "("), ")")
		}
		return "count_if(" + body + ")"
	case *MBinary:
		prec := 1
		if x.Op == '*' || x.Op == '/' {
			prec = 2
		}
		text := printMExpr(x.L, prec) + " " + string(x.Op) + " " + printMExpr(x.R, prec+1)
		if prec < parent {
			text = "(" + text + ")"
		}
		return text
	case *MNeg:
		return "-" + printMExpr(x.X, 3)
	case *MConst:
		return strconv.FormatFloat(x.Value, 'f', -1, 64)
	}
	return ""
}

func printBandRange(f Field, bands []string) string {
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
