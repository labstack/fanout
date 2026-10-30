package query

import "fmt"

// SQLExpr is the shared typed view of an engine expression. Unrecognized classes
// remain visible so consumers can reject them or preserve the original SQL.
// Engine rendering still receives the original, lossless statement tree.
type SQLExpr struct {
	Class, Type                             string
	Children                                []*SQLExpr
	Left, Right, Child, Input, Lower, Upper *SQLExpr
	Columns                                 []string
	Function                                string
	Arguments                               []*SQLExpr
	CastType                                string
	TryCast                                 bool
	LiteralKind, LiteralText                string
	Identifier                              string
	Negated, Distinct, HasOrder, HasFilter  bool
	Grouped                                 bool // explicit parentheses in a byte-checked local lowering
}

// SQLExpression builds a typed expression view for validation and inspection.
// Statement checks and lossless tree rewrites still use the engine JSON maps;
// unknown fields remain in that original tree for rendering.
func SQLExpression(raw any) *SQLExpr {
	v, ok := raw.(map[string]any)
	if !ok || v == nil {
		return nil
	}
	text := func(key string) string { s, _ := v[key].(string); return s }
	flag := func(key string) bool { b, _ := v[key].(bool); return b }
	e := &SQLExpr{Class: text("class"), Type: text("type"), Function: text("function_name"), Identifier: text("identifier"), TryCast: flag("try_cast"), Negated: flag("negated"), Distinct: flag("distinct"), HasFilter: v["filter"] != nil}
	e.Left, e.Right, e.Child = SQLExpression(v["left"]), SQLExpression(v["right"]), SQLExpression(v["child"])
	e.Input, e.Lower, e.Upper = SQLExpression(v["input"]), SQLExpression(v["lower"]), SQLExpression(v["upper"])
	for _, c := range sqlArray(v["children"]) {
		e.Children = append(e.Children, SQLExpression(c))
	}
	for _, c := range sqlArray(v["column_names"]) {
		s, _ := c.(string)
		e.Columns = append(e.Columns, s)
	}
	for _, a := range sqlArray(v["arguments"]) {
		arg, _ := a.(map[string]any)
		e.Arguments = append(e.Arguments, SQLExpression(arg["expression"]))
	}
	typ, _ := v["type_expr"].(map[string]any)
	e.CastType, _ = typ["type_name"].(string)
	literal, _ := v["literal"].(map[string]any)
	e.LiteralKind, _ = literal["kind"].(string)
	e.LiteralText, _ = literal["text"].(string)
	if e.LiteralText == "" && literal["value"] != nil {
		e.LiteralText = fmt.Sprint(literal["value"])
	}
	orders, _ := v["order_bys"].(map[string]any)
	e.HasOrder = len(sqlArray(orders["orders"])) > 0
	return e
}
func sqlArray(raw any) []any { a, _ := raw.([]any); return a }

// everySQLExpression visits the typed tree without rebuilding subtrees.
func everySQLExpression(n *SQLExpr, accept func(*SQLExpr) bool) bool {
	if n == nil {
		return true
	}
	if !accept(n) {
		return false
	}
	for _, child := range []*SQLExpr{n.Left, n.Right, n.Child, n.Input, n.Lower, n.Upper} {
		if !everySQLExpression(child, accept) {
			return false
		}
	}
	for _, children := range [][]*SQLExpr{n.Children, n.Arguments} {
		for _, child := range children {
			if !everySQLExpression(child, accept) {
				return false
			}
		}
	}
	return true
}
