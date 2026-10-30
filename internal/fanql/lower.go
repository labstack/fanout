package fanql

import (
	"fmt"
	"math/big"
	"strconv"
	"strings"

	"github.com/labstack/fanout/internal/panel"
)

func init() { resolveLower = lower }

// lower is pure: it emits one SQL string per entry and keeps all stored order.
func lower(r *Resolved) (panel.Query, panel.Problems) {
	q := r.Query
	out := panel.Query{From: q.Source}
	var problems panel.Problems
	for i, e := range q.Where {
		if i == 16 {
			problems = append(problems, panel.Problem{Path: fmt.Sprintf("where[%d]", i), Message: "at most 16 filters", Span: textSpan(e.Span())})
			break
		}
		sql := lowerEntry(e)
		if len(sql) > 500 {
			problems = append(problems, panel.Problem{Path: fmt.Sprintf("where[%d]", i), Message: fmt.Sprintf("this filter lowers to %d bytes; filters are at most 500", len(sql)), Span: textSpan(e.Span())})
		}
		out.Where = append(out.Where, sql)
	}
	if s := q.Stage; s != nil {
		out.Bucket = s.Bucket
		for _, f := range s.By {
			out.By = append(out.By, lowerField(f))
		}
		if s.Histogram != nil {
			buckets := "log2"
			if q.Source == "metrics" {
				buckets = "explicit"
			}
			out.Measures = []string{"count()"}
			out.Histogram = &panel.Histogram{Field: lowerField(*s.Histogram), Buckets: buckets}
		} else {
			for _, m := range s.Measures {
				out.Measures = append(out.Measures, lowerMeasure(m))
			}
		}
	}
	if q.Sort != nil {
		key := q.Sort.Key
		if key == "duration" && !sortIsMeasureAlias(q) {
			key = "duration_ms"
		}
		out.Sort = key
		if q.Sort.Ascending {
			out.Sort = "+" + key
		}
	}
	if q.Limit != nil {
		out.Limit = q.Limit.N
	}
	return out, problems
}

func lowerField(f Field) string {
	if f.Ref == nil {
		return f.Key
	}
	if f.Ref.Key != "" {
		return f.Ref.Column + "[" + sqlString(f.Ref.Key) + "]"
	}
	return f.Ref.Column
}
func compareField(f Field) string {
	s := lowerField(f)
	if f.Ref != nil && f.Ref.CompareAs == "integer" {
		return "TRY_CAST(" + s + " AS INTEGER)"
	}
	return s
}
func sqlString(text string) string { return "'" + strings.ReplaceAll(text, "'", "''") + "'" }
func decimal(n float64) string     { return strconv.FormatFloat(n, 'f', -1, 64) }
func numberText(text string) string {
	// Integer spelling carries precision that a float64 cannot preserve.
	if n, ok := new(big.Int).SetString(text, 10); ok {
		return n.String()
	}
	n, err := strconv.ParseFloat(text, 64)
	if err != nil {
		return text
	}
	return decimal(n)
}
func lowerValue(f Field, v Value) string {
	text := v.Stored
	if text == "" {
		text = v.Text
	}
	if v.Kind == VariableValue {
		return v.Text
	}
	if v.Kind == DurationValue {
		n, _ := durationMS(v.Text)
		return decimal(n)
	}
	if f.Ref != nil && f.Ref.Type == panel.TypeTime && f.Ref.Key == "" {
		return sqlString(text) + "::TIMESTAMPTZ_NS"
	}
	if v.Kind == StringValue {
		return sqlString(text)
	}
	if f.Ref != nil && f.Ref.Key == "" && f.Ref.Type == panel.TypeString && f.Ref.CompareAs == "" {
		return sqlString(text)
	}
	if v.Kind == NumberValue {
		return numberText(text)
	}
	if v.Kind == BooleanValue {
		return strings.ToLower(text)
	}
	return sqlString(text)
}

func lowerEntry(e Expr) string {
	// A whole raw entry is the exact interchange spelling (section 0.8).
	// Raw fragments inside boolean expressions are parenthesized below.
	if raw, ok := e.(*RawExpr); ok {
		return raw.Text
	}
	return lowerNegated(e, false)
}
func lowerNegated(e Expr, neg bool) string {
	switch x := e.(type) {
	case *Not:
		return lowerNegated(x.X, !neg)
	case *And:
		return lowerTerms(x.Terms, false, neg)
	case *Or:
		return lowerTerms(x.Terms, true, neg)
	case *Compare:
		if x.Op == "!~" {
			copy := *x
			copy.Op = "~"
			return lowerNegated(&copy, !neg)
		}
		f := compareField(x.Field)
		op := x.Op
		if op == ":" {
			op = "="
		}
		if op == "<>" {
			op = "!="
		}
		if x.Bands != nil {
			return lowerBands(x.Field, x.Bands, false, neg)
		}
		v := lowerValue(x.Field, x.Value)
		if op == "~" {
			field := lowerField(x.Field)
			if x.Field.Ref != nil && x.Field.Ref.Key != "" {
				field = "CAST(" + field + " AS VARCHAR)"
			}
			v = sqlString(x.Value.Stored)
			if x.Value.Kind == VariableValue {
				v = x.Value.Text
			}
			p := containsSQL(field, v)
			if neg {
				return "(" + lowerField(x.Field) + " IS NULL OR NOT " + p + ")"
			}
			return p
		}
		p := f + " " + op + " " + v
		if neg {
			if simpleOperand(x.Field, op) {
				inverse := map[string]string{"=": "!=", "!=": "=", ">": "<=", ">=": "<", "<": ">=", "<=": ">"}[op]
				return "(" + lowerField(x.Field) + " IS NULL OR " + f + " " + inverse + " " + v + ")"
			}
			return nullNegation(p)
		}
		return p
	case *In:
		var vals []string
		for _, v := range x.Values {
			vals = append(vals, lowerValue(x.Field, v))
		}
		list := "(" + strings.Join(vals, ", ") + ")"
		if x.Variable != "" {
			list = "$" + x.Variable
		}
		op := "IN"
		if x.Negated != neg {
			op = "NOT IN"
		}
		p := lowerField(x.Field) + " " + op + " " + list
		if neg {
			return "(" + lowerField(x.Field) + " IS NULL OR " + p + ")"
		}
		return p
	case *Between:
		if x.Bands != nil {
			return lowerBands(x.Field, x.Bands, x.Negated, neg)
		}
		op := "BETWEEN"
		if x.Negated {
			op = "NOT BETWEEN"
		}
		p := compareField(x.Field) + " " + op + " " + lowerValue(x.Field, x.Lo) + " AND " + lowerValue(x.Field, x.Hi)
		if neg {
			if simpleOperand(x.Field, "between") {
				op = "NOT BETWEEN"
				if x.Negated {
					op = "BETWEEN"
				}
				return "(" + lowerField(x.Field) + " IS NULL OR " + lowerField(x.Field) + " " + op + " " + lowerValue(x.Field, x.Lo) + " AND " + lowerValue(x.Field, x.Hi) + ")"
			}
			return nullNegation(p)
		}
		return p
	case *Like:
		op := "~~"
		if x.Insensitive {
			op += "*"
		}
		if x.Negated != neg {
			op = "!" + op
		}
		p := lowerField(x.Field) + " " + op + " " + sqlString(x.Pattern.Text)
		if neg {
			return "(" + lowerField(x.Field) + " IS NULL OR " + p + ")"
		}
		return p
	case *Exists:
		if neg {
			return lowerField(x.Field) + " IS NULL"
		}
		return lowerField(x.Field) + " IS NOT NULL"
	case *IsNull:
		if x.Negated != neg {
			return lowerField(x.Field) + " IS NOT NULL"
		}
		return lowerField(x.Field) + " IS NULL"
	case *FreeText:
		field := "body"
		if x.column != "" {
			field = x.column
		}
		p := containsSQL(field, sqlString(x.Value.Text))
		if neg {
			return "(" + field + " IS NULL OR NOT " + p + ")"
		}
		return p
	case *Call:
		p := lowerCall(x)
		if neg {
			return nullNegation(p)
		}
		return p
	case *RawExpr:
		if neg {
			return nullNegation(x.Text)
		}
		return "(" + x.Text + ")"
	}
	return ""
}

func lowerTerms(terms []Expr, or, neg bool) string {
	var out []string
	for _, e := range terms {
		out = append(out, lowerNegated(e, neg))
	}
	sep := " AND "
	if or != neg {
		sep = " OR "
	}
	return "(" + strings.Join(out, sep) + ")"
}
func nullNegation(p string) string { return "NOT coalesce((" + p + "), FALSE)" }
func simpleOperand(f Field, op string) bool {
	return f.Ref != nil && f.Ref.CompareAs == "" && !(f.Ref.Key != "" && isRange(op))
}
func lowerBands(f Field, bands []string, sqlNeg, neg bool) string {
	var out []string
	for _, b := range bands {
		out = append(out, sqlString(b))
	}
	op := "IN"
	if sqlNeg != neg {
		op = "NOT IN"
	}
	p := lowerField(f) + " " + op + " (" + strings.Join(out, ", ") + ")"
	if neg {
		return "(" + lowerField(f) + " IS NULL OR " + p + ")"
	}
	return p
}
func containsSQL(field, value string) string {
	return "contains(lower(" + field + "), lower(" + value + "))"
}
func lowerCall(c *Call) string {
	var args []string
	for _, a := range c.Args {
		if a.Field != nil {
			args = append(args, lowerField(*a.Field))
		} else if a.Call != nil {
			args = append(args, lowerCall(a.Call))
		} else if a.Value != nil {
			v := a.Value
			if v.Kind == NumberValue || v.Kind == BooleanValue {
				args = append(args, v.Text)
			} else {
				args = append(args, sqlString(v.Text))
			}
		}
	}
	p := c.Name + "(" + strings.Join(args, ", ") + ")"
	if c.Cmp != nil {
		v := c.Cmp.Value.Text
		if c.Cmp.Value.Kind == NumberValue {
			v = numberText(v)
		}
		if c.Cmp.Value.Kind == BooleanValue {
			v = strings.ToLower(v)
		}
		if c.Cmp.Value.Kind != NumberValue && c.Cmp.Value.Kind != BooleanValue && c.Cmp.Value.Kind != VariableValue {
			v = sqlString(v)
		}
		p += " " + c.Cmp.Op + " " + v
	}
	return p
}

func lowerMeasure(m Measure) string {
	text := lowerMExpr(m.Expr, 0)
	if m.Alias != "" {
		text += " as " + m.Alias
	}
	return text
}
func lowerMExpr(e MExpr, parent int) string {
	switch x := e.(type) {
	case *MCall:
		var args []string
		for _, a := range x.Args {
			if a.Field != nil {
				args = append(args, lowerField(*a.Field))
			} else if a.Number != nil {
				args = append(args, decimal(*a.Number))
			}
		}
		return x.Name + "(" + strings.Join(args, ", ") + ")"
	case *MCountIf:
		sql := lowerEntry(x.Filter)
		if _, ok := x.Filter.(*And); ok {
			sql = strings.TrimSuffix(strings.TrimPrefix(sql, "("), ")")
		}
		return "count_if(" + sql + ")"
	case *MConst:
		return decimal(x.Value)
	case *MNeg:
		if _, nested := x.X.(*MNeg); nested {
			return "-(" + lowerMExpr(x.X, 0) + ")"
		}
		return "-" + lowerMExpr(x.X, 3)
	case *MBinary:
		prec := 1
		if x.Op == '*' || x.Op == '/' {
			prec = 2
		}
		text := lowerMExpr(x.L, prec) + " " + string(x.Op) + " " + lowerMExpr(x.R, prec+1)
		if prec < parent {
			return "(" + text + ")"
		}
		return text
	}
	return ""
}
