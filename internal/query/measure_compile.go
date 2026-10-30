package query

import (
	"strconv"
	"strings"
)

// measureExprSQL renders only the bounded tree. SQL filter source never enters
// this path: Check installs the validated canonical rendering in each leaf.
func measureExprSQL(m Measure, sig *signal, seconds float64, partition string) string {
	if m.Expr == nil {
		return measureSQL(m, sig, seconds, partition)
	}
	return renderMeasureExpr(m.Expr, sig, seconds, partition, 0)
}
func renderMeasureExpr(e MeasureExpr, sig *signal, seconds float64, partition string, parent int) string {
	switch e := e.(type) {
	case MeasureCall:
		return measureSQL(Measure{Func: e.Func, Field: e.Field, Q: e.Q}, sig, seconds, partition)
	case MeasureCountIf:
		return "count(*) FILTER (WHERE (" + e.Expr + "))::DOUBLE"
	case MeasureConst:
		return strconv.FormatFloat(e.Value, 'f', -1, 64)
	case MeasureNeg:
		return "-(" + renderMeasureExpr(e.X, sig, seconds, partition, 0) + ")"
	case MeasureBinary:
		precedence := 1
		if e.Op == '*' || e.Op == '/' {
			precedence = 2
		}
		left := renderMeasureExpr(e.L, sig, seconds, partition, precedence)
		right := renderMeasureExpr(e.R, sig, seconds, partition, precedence+1)
		if e.Op == '/' {
			right = "nullif(" + renderMeasureExpr(e.R, sig, seconds, partition, 0) + ", 0)"
		}
		sql := left + " " + string(e.Op) + " " + right
		if precedence < parent {
			return "(" + sql + ")"
		}
		return sql
	}
	panic("unknown measure expression")
}

// affineBase decomposes a*x+b while preserving x's untransformed ranking score.
// It intentionally declines expressions with multiple nonconstant operands.
func affineBase(e MeasureExpr) (base MeasureExpr, a, b float64, ok bool) {
	switch e := e.(type) {
	case MeasureNeg:
		x, a, b, ok := affineBase(e.X)
		return x, -a, -b, ok
	case MeasureBinary:
		if c, constant := constantValue(e.R); constant {
			x, a, b, ok := affineBase(e.L)
			switch e.Op {
			case '+':
				return x, a, b + c, ok
			case '-':
				return x, a, b - c, ok
			case '*':
				return x, a * c, b * c, ok
			case '/':
				if c != 0 {
					return x, a / c, b / c, ok
				}
			}
		}
		if c, constant := constantValue(e.L); constant {
			x, a, b, ok := affineBase(e.R)
			switch e.Op {
			case '+':
				return x, a, b + c, ok
			case '-':
				return x, -a, c - b, ok
			case '*':
				return x, a * c, b * c, ok
			}
		}
	case MeasureConst:
		return nil, 0, 0, false
	}
	return e, 1, 0, true
}
func binomial(e MeasureExpr) (MeasureCountIf, bool) {
	if e, ok := e.(MeasureBinary); ok && e.Op == '/' {
		n, ok := e.L.(MeasureCountIf)
		d, ok2 := e.R.(MeasureCall)
		if ok && ok2 && d.Func == "count" {
			return n, true
		}
	}
	return MeasureCountIf{}, false
}
func flipBetter(better string) string {
	if better == "lower" {
		return "higher"
	}
	if better == "higher" {
		return "lower"
	}
	return better
}
func neutralBetter(better string) string {
	if better == "" {
		return "neutral"
	}
	return better
}
func rankRule(m Measure, sig *signal, better string) RankRule {
	rule := RankRule{Rule: "builtin", Better: neutralBetter(better), Floor: 20, SupportColumn: "count(*)"}
	base, a, _, ok := affineBase(m.Expr)
	if _, singleCall := m.Expr.(MeasureCall); !singleCall {
		rule.Rule = "value_floor"
	}
	if ok && a != 0 {
		if call, yes := base.(MeasureCall); yes {
			if m.IsExpression() {
				rule.Rule = "inherit"
			}
			rule.SupportColumn = builtinSupport(call)
		} else if _, yes := binomial(base); yes {
			rule.Rule = "wilson"
		}
	}
	if rule.Rule == "value_floor" {
		rule.SupportColumn = expressionSupport(m.Expr)
	}
	return rule
}

// Builtin and inherited calls retain their original support and tie semantics.
func builtinSupport(call MeasureCall) string {
	switch call.Func {
	case "p50", "p75", "p90", "p95", "p99", "quantile", "avg", "max", "min":
		return "count(" + call.Field.numberSQL() + ")"
	}
	return "count(*)"
}
func leafSupport(e MeasureExpr) string {
	switch e := e.(type) {
	case MeasureCall:
		if e.Field != nil && measureFuncs[e.Func].numeric {
			return "count(" + e.Field.numberSQL() + ")"
		}
	}
	return "count(*)"
}
func expressionSupport(e MeasureExpr) string {
	// Ratios with a conditional-count denominator use that population directly.
	base, _, _, ok := affineBase(e)
	if ratio, yes := base.(MeasureBinary); ok && yes && ratio.Op == '/' {
		if denom, yes := ratio.R.(MeasureCountIf); yes {
			return "count(*) FILTER (WHERE (" + denom.Expr + "))"
		}
	}
	var supports []string
	var walk func(MeasureExpr)
	walk = func(e MeasureExpr) {
		switch e := e.(type) {
		case MeasureCall, MeasureCountIf:
			s := leafSupport(e)
			for _, v := range supports {
				if v == s {
					return
				}
			}
			supports = append(supports, s)
		case MeasureBinary:
			walk(e.L)
			walk(e.R)
		case MeasureNeg:
			walk(e.X)
		}
	}
	walk(e)
	if len(supports) == 1 {
		return supports[0]
	}
	return "least(" + strings.Join(supports, ", ") + ")"
}
func rankingOrder(first Measure, sig *signal, better string) (value, samples, extra, order string) {
	// Ranking comparisons use totals; rate's fixed positive seconds scale cannot
	// affect order, so one second is sufficient for its score.
	value = measureExprSQL(first, sig, 1, "")
	rule := rankRule(first, sig, better)
	samples = rule.SupportColumn
	base, a, _, _ := affineBase(first.Expr)
	if a < 0 && (rule.Rule == "inherit" || rule.Rule == "wilson") {
		better = flipBetter(better)
	}
	direction := "DESC"
	if better == "higher" {
		direction = "ASC"
	}
	if rule.Rule == "builtin" || rule.Rule == "inherit" {
		call, _ := base.(MeasureCall)
		m := Measure{Func: call.Func, Field: call.Field, Q: call.Q, Additive: measureFuncs[call.Func].additive}
		// count_if alone uses its value and row support like the count family.
		if call.Func == "" {
			return value, samples, "", "value DESC NULLS LAST, n DESC, d"
		}
		value = measureSQL(m, sig, 1, "")
		if m.Additive || m.Func == "share" {
			direction = "DESC"
		}
		order = "value " + direction + " NULLS LAST, n DESC, d"
		switch m.Func {
		case "error_rate":
			extra = ", sum(" + errorCase(sig) + ") AS errors, " + wilsonLowerSQL() + " AS confidence"
			order = floorOrder("confidence", "DESC")
		case "p50", "p75", "p90", "p95", "p99", "quantile", "avg", "max", "min":
			order = floorOrder("value", direction)
		}
		return
	}
	if rule.Rule == "wilson" {
		leaf, _ := binomial(base)
		extra = ", sum(CASE WHEN " + leaf.Expr + " THEN 1.0 ELSE 0.0 END) AS errors, " + wilsonLowerSQL() + " AS confidence"
		if better == "higher" {
			extra = ", sum(CASE WHEN " + leaf.Expr + " THEN 1.0 ELSE 0.0 END) AS errors, " + wilsonUpperSQL() + " AS confidence"
			order = floorOrder("confidence", "ASC")
		} else {
			order = floorOrder("confidence", "DESC")
		}
	} else {
		order = floorOrder("value", direction)
	}
	return
}
func floorOrder(score, direction string) string {
	return "(n >= 20) DESC, CASE WHEN n >= 20 THEN " + score + " END " + direction + " NULLS LAST, n DESC, d"
}
func wilsonUpperSQL() string {
	return strings.Replace(strings.Replace(wilsonLowerSQL(), "greatest(0.0,", "least(1.0,", 1), "- 1.96 * sqrt", "+ 1.96 * sqrt", 1)
}
