package panel

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"strings"
)

// Filter is one checked filter expression.
type Filter struct {
	// Expr is DuckDB's canonical rendering of the checked expression; it is
	// the only text ever embedded in SQL.
	Expr string `json:"expr"`
	// Source is the author's text, kept for messages.
	Source   string          `json:"source"`
	Params   []string        `json:"params,omitempty"`
	InParams map[string]bool `json:"-"`
	// EqField is the field compared when the filter is `field = value`; the
	// empty-panel diagnosis lists values seen for it.
	EqField string `json:"-"`
}

var (
	filterFunctions = map[string]bool{"~~": true, "!~~": true, "~~*": true, "!~~*": true, "lower": true, "upper": true,
		"starts_with": true, "contains": true, "regexp_matches": true, "length": true}
	filterCasts     = map[string]bool{"VARCHAR": true, "BIGINT": true, "INTEGER": true, "DOUBLE": true, "BOOLEAN": true, "UBIGINT": true, "TIMESTAMPTZ_NS": true}
	filterOperators = map[string]bool{"OPERATOR_NOT": true, "OPERATOR_IS_NULL": true, "OPERATOR_IS_NOT_NULL": true, "OPERATOR_COALESCE": true, "COMPARE_IN": true, "COMPARE_NOT_IN": true}
)

// checkFilter parses expr with DuckDB as the WHERE clause of a query over the
// signal and walks the tree against an allowlist: columns of that signal,
// attribute lookups with literal keys, comparisons, boolean logic, IN, LIKE,
// IS NULL, BETWEEN, casts to scalar types, constants, declared variables, and
// a few scalar functions. Anything else is rejected. The accepted expression
// is stored as DuckDB's canonical rendering with typed parameter casts, and
// that rendering must bind against the signal.
func checkFilter(ctx context.Context, parser Parser, sig *signal, vars map[string]Variable, expr string) (Filter, error) {
	if strings.TrimSpace(expr) == "" || len(expr) > 500 {
		return Filter{}, errors.New("filters are 1 to 500 characters")
	}
	if strings.Contains(stripQuoted(expr), "?") {
		return Filter{}, errors.New("use $variables instead of ? placeholders")
	}
	node, err := parser.ParseSQL(ctx, "SELECT 1 FROM "+sig.name+" WHERE ("+expr+")")
	if err != nil {
		if isOperational(err) {
			return Filter{}, err
		}
		return Filter{}, fmt.Errorf("does not parse: %w", err)
	}
	if !onlyWhereClause(node, sig.name) {
		return Filter{}, errors.New("must be a single boolean expression")
	}
	f := Filter{Source: strings.TrimSpace(expr), InParams: map[string]bool{}}
	uses := map[string]int{}
	if err := walkFilter(node["where_clause"], sig, vars, &f, uses); err != nil {
		return Filter{}, err
	}
	for _, name := range f.Params {
		if vars[name].Multi && !f.InParams[name] {
			return Filter{}, fmt.Errorf("$%s holds several values; use it as IN $%s", name, name)
		}
		if f.InParams[name] && uses[name] > 1 {
			return Filter{}, fmt.Errorf("use $%s either with IN or with a comparison, not both", name)
		}
	}
	f.EqField = equalityField(node["where_clause"])
	rewriteContainsAsIn(node["where_clause"])
	node["where_clause"] = castParameters(node["where_clause"], sig, "")
	canonical, err := canonicalFilter(ctx, parser, sig, vars, node, f)
	if err != nil {
		return Filter{}, err
	}
	f.Expr = canonical
	if err := bindFilter(ctx, parser, sig, vars, f); err != nil {
		return Filter{}, err
	}
	return f, nil
}

// bindFilter binds the canonical expression on the engine's guarded DESCRIBE
// path, with NULL for every value and as many values as a run binds (one per
// scalar variable, two per list), so a filter whose types do not line up is a
// Check problem instead of an error on every run.
func bindFilter(ctx context.Context, parser Parser, sig *signal, vars map[string]Variable, f Filter) error {
	query, describe, _, err := bindParams(f.Expr, func(name string) ([]any, bool, error) {
		if vars[name].Multi {
			return []any{"", ""}, true, nil
		}
		return []any{""}, false, nil
	})
	if err != nil {
		return err
	}
	prefix := "SELECT 1 FROM " + sig.name + " WHERE ("
	if _, _, err := parser.PrepareTelemetrySQL(ctx, prefix+query+")", prefix+describe+")", 1); err != nil {
		if isOperational(err) {
			return err
		}
		message, _, _ := strings.Cut(err.Error(), "\n")
		message = "does not bind: " + strings.TrimSpace(message)
		if len(f.Params) > 0 {
			name := f.Params[0]
			message += fmt.Sprintf("; cast variables to the compared type, e.g. CAST($%s AS DOUBLE) or CAST($%s AS TIMESTAMPTZ_NS)", name, name)
		}
		return errors.New(message)
	}
	return nil
}

// canonicalFilter renders the checked statement with DuckDB, keeps only the
// WHERE expression, and checks the rendering again: it must parse as the same
// single expression and use the same variables.
func canonicalFilter(ctx context.Context, parser Parser, sig *signal, vars map[string]Variable, node map[string]any, f Filter) (string, error) {
	rendered, err := parser.RenderSQL(ctx, node)
	if err != nil {
		if isOperational(err) {
			return "", err
		}
		return "", fmt.Errorf("the filter could not be rendered canonically: %w", err)
	}
	prefix := "SELECT 1 FROM " + sig.name + " WHERE "
	if !strings.HasPrefix(rendered, prefix) {
		return "", errors.New("the filter could not be rendered canonically")
	}
	expr := strings.TrimPrefix(rendered, prefix)
	again, err := parser.ParseSQL(ctx, "SELECT 1 FROM "+sig.name+" WHERE ("+expr+")")
	if err != nil && isOperational(err) {
		return "", err
	}
	if err != nil || !onlyWhereClause(again, sig.name) {
		return "", errors.New("the filter could not be rendered canonically")
	}
	g := Filter{InParams: map[string]bool{}}
	if err := walkFilter(again["where_clause"], sig, vars, &g, map[string]int{}); err != nil {
		return "", err
	}
	if !slices.Equal(g.Params, f.Params) {
		return "", errors.New("the filter could not be rendered canonically")
	}
	return expr, nil
}

func onlyWhereClause(node map[string]any, table string) bool {
	if node["type"] != "SELECT_NODE" {
		return false
	}
	if mods, _ := node["modifiers"].([]any); len(mods) > 0 {
		return false
	}
	if cte, _ := node["cte_map"].(map[string]any); cte != nil {
		if entries, _ := cte["map"].([]any); len(entries) > 0 {
			return false
		}
	}
	if groups, _ := node["group_expressions"].([]any); len(groups) > 0 {
		return false
	}
	if sets, _ := node["group_sets"].([]any); len(sets) > 0 {
		return false
	}
	if node["aggregate_handling"] != "STANDARD_HANDLING" {
		return false
	}
	if node["having"] != nil || node["qualify"] != nil || node["sample"] != nil {
		return false
	}
	if list, _ := node["select_list"].([]any); len(list) != 1 {
		return false
	}
	from, _ := node["from_table"].(map[string]any)
	return from != nil && from["type"] == "BASE_TABLE" && from["table_name"] == table && from["schema_name"] == "" && from["catalog_name"] == ""
}

func walkFilter(n any, sig *signal, vars map[string]Variable, f *Filter, uses map[string]int) error {
	switch v := n.(type) {
	case nil:
		return nil
	case []any:
		for _, child := range v {
			if err := walkFilter(child, sig, vars, f, uses); err != nil {
				return err
			}
		}
		return nil
	case map[string]any:
		class, _ := v["class"].(string)
		kind, _ := v["type"].(string)
		walk := func(keys ...string) error {
			for _, key := range keys {
				if err := walkFilter(v[key], sig, vars, f, uses); err != nil {
					return err
				}
			}
			return nil
		}
		switch class {
		case "CONJUNCTION":
			return walk("children")
		case "COMPARISON":
			return walk("left", "right")
		case "BETWEEN":
			return walk("input", "lower", "upper")
		case "OPERATOR":
			if kind == "ARRAY_EXTRACT" {
				return checkAttributeLookup(v)
			}
			if !filterOperators[kind] {
				return fmt.Errorf("%s is not allowed in a filter", strings.ToLower(kind))
			}
			if kind == "COMPARE_IN" || kind == "COMPARE_NOT_IN" {
				if children, _ := v["children"].([]any); len(children) > 1 {
					for _, c := range children[1:] {
						if param, _ := c.(map[string]any); param != nil && param["class"] == "PARAMETER" {
							id, _ := param["identifier"].(string)
							f.InParams[id] = true
						}
					}
				}
			}
			return walk("children")
		case "CAST":
			castType, _ := v["type_expr"].(map[string]any)
			id, _ := castType["type_name"].(string)
			if !filterCasts[id] {
				return fmt.Errorf("casting to %s is not allowed in a filter", id)
			}
			return walk("child")
		case "COLUMN_REF":
			names, _ := v["column_names"].([]any)
			if len(names) != 1 {
				return errors.New("qualified column names are not allowed in a filter")
			}
			name, _ := names[0].(string)
			if _, err := sig.field(name); err != nil {
				return err
			}
			return nil
		case "CONSTANT":
			return nil
		case "PARAMETER":
			name, _ := v["identifier"].(string)
			if name == "" || (name[0] >= '0' && name[0] <= '9') {
				return errors.New("use $name variables, not positional parameters")
			}
			if _, ok := vars[name]; !ok {
				return fmt.Errorf("$%s is not a declared variable", name)
			}
			uses[name]++
			if !slices.Contains(f.Params, name) {
				f.Params = append(f.Params, name)
			}
			return nil
		case "FUNCTION":
			name, _ := v["function_name"].(string)
			if !filterFunctions[name] {
				return fmt.Errorf("function %s is not allowed in a filter", name)
			}
			if distinct, _ := v["distinct"].(bool); distinct {
				return errors.New("DISTINCT is not allowed in a filter")
			}
			if orders, _ := v["order_bys"].(map[string]any); orders != nil {
				if list, _ := orders["orders"].([]any); len(list) > 0 {
					return errors.New("ORDER BY is not allowed in a filter")
				}
			}
			if v["filter"] != nil {
				return errors.New("FILTER is not allowed in a filter")
			}
			args := functionArguments(v)
			if name == "contains" && len(args) == 2 {
				if param, _ := args[0].(map[string]any); param != nil && param["class"] == "PARAMETER" {
					id, _ := param["identifier"].(string)
					f.InParams[id] = true
				}
			}
			return walkFilter(args, sig, vars, f, uses)
		default:
			return fmt.Errorf("%s is not allowed in a filter", strings.ToLower(strings.TrimPrefix(kind, "VALUE_")))
		}
	}
	return nil
}

func checkAttributeLookup(v map[string]any) error {
	children, _ := v["children"].([]any)
	if len(children) != 2 {
		return errors.New("attribute lookups take one key")
	}
	column, _ := children[0].(map[string]any)
	names, _ := column["column_names"].([]any)
	if column["class"] != "COLUMN_REF" || len(names) != 1 || (names[0] != "attributes" && names[0] != "resource") {
		return errors.New("only attributes['key'] and resource['key'] lookups are allowed")
	}
	key, _ := children[1].(map[string]any)
	text := constantText(key)
	if key["class"] != "CONSTANT" || !attributePattern.MatchString(fmt.Sprintf("%s['%s']", names[0], text)) {
		return errors.New("attribute keys must be literal strings such as attributes['http.route']")
	}
	return nil
}

func equalityField(n any) string {
	v, _ := n.(map[string]any)
	if v == nil || v["class"] != "COMPARISON" || v["type"] != "COMPARE_EQUAL" {
		return ""
	}
	for _, side := range []string{"left", "right"} {
		if field := fieldText(v[side]); field != "" {
			return field
		}
	}
	return ""
}

func fieldText(n any) string {
	v, _ := n.(map[string]any)
	if v == nil {
		return ""
	}
	switch v["class"] {
	case "CAST":
		return fieldText(v["child"])
	case "COLUMN_REF":
		names, _ := v["column_names"].([]any)
		if len(names) == 1 {
			name, _ := names[0].(string)
			return name
		}
	case "OPERATOR":
		if v["type"] == "ARRAY_EXTRACT" {
			children, _ := v["children"].([]any)
			column, _ := children[0].(map[string]any)
			names, _ := column["column_names"].([]any)
			key, _ := children[1].(map[string]any)
			text := constantText(key)
			if len(names) == 1 {
				return fmt.Sprintf("%s['%s']", names[0], text)
			}
		}
	}
	return ""
}

// stripQuoted blanks quoted strings and identifiers so scans for tokens see
// only SQL syntax.
func stripQuoted(text string) string {
	var b strings.Builder
	var quote byte
	for i := 0; i < len(text); i++ {
		c := text[i]
		switch {
		case quote != 0:
			if c == quote {
				if i+1 < len(text) && text[i+1] == quote {
					i++
					continue
				}
				quote = 0
			}
			b.WriteByte(' ')
		case c == '\'' || c == '"':
			quote = c
			b.WriteByte(' ')
		default:
			b.WriteByte(c)
		}
	}
	return b.String()
}

// constantText returns the text of a string constant node.
func constantText(n map[string]any) string {
	literal, _ := n["literal"].(map[string]any)
	text, _ := literal["text"].(string)
	return text
}

// functionArguments returns the argument expressions of a FUNCTION node.
func functionArguments(v map[string]any) []any {
	entries, _ := v["arguments"].([]any)
	out := make([]any, 0, len(entries))
	for _, e := range entries {
		if entry, _ := e.(map[string]any); entry != nil {
			out = append(out, entry["expression"])
		}
	}
	return out
}

// rewriteContainsAsIn turns DuckDB's parse of `x IN $p`, contains($p, x),
// back into `x IN ($p)` (OPERATOR / COMPARE_IN). The bound list then renders
// as an IN list, which compares VARIANT attributes and numeric columns with
// string binds correctly; contains([?, ?], x) does not.
func rewriteContainsAsIn(n any) {
	switch v := n.(type) {
	case []any:
		for _, child := range v {
			rewriteContainsAsIn(child)
		}
	case map[string]any:
		for _, child := range v {
			rewriteContainsAsIn(child)
		}
		if v["class"] != "FUNCTION" || v["function_name"] != "contains" {
			return
		}
		args := functionArguments(v)
		if len(args) != 2 {
			return
		}
		if param, _ := args[0].(map[string]any); param == nil || param["class"] != "PARAMETER" {
			return
		}
		for key := range v {
			delete(v, key)
		}
		v["alias"] = ""
		v["class"] = "OPERATOR"
		v["type"] = "COMPARE_IN"
		v["children"] = []any{args[1], args[0]}
	}
}

var numericCasts = map[string]bool{"INTEGER": true, "BIGINT": true, "UBIGINT": true, "DOUBLE": true}

// The types castParameters gives a parameter. The time columns are
// TIMESTAMPTZ_NS; DuckDB 2 does not compare them with a TIMESTAMPTZ cast, and
// the _NS cast keeps a variable's nanoseconds.
const (
	paramNumber = "DOUBLE"
	paramTime   = "TIMESTAMPTZ_NS"
	paramText   = "VARCHAR"
)

// castParameters wraps every PARAMETER in a CAST typed from the operands it is
// compared with: in a comparison, BETWEEN (input and bounds alike), IN list or
// coalesce, the first operand that is a number (numeric column, numeric cast,
// numeric constant, length(), a numeric coalesce) makes the parameters DOUBLE,
// and the first that is a time (time column, TIMESTAMPTZ_NS cast) makes them
// TIMESTAMPTZ_NS. Everything else, including a parameter compared only with
// parameters, is VARCHAR. Variables bind as strings; the driver cannot bind an
// untyped parameter against a VARIANT attribute, and VARCHAR does not compare
// with numbers or times, so the type has to be in the canonical SQL. Whatever
// this misses, checkFilter's bind check reports.
func castParameters(n any, sig *signal, want string) any {
	switch v := n.(type) {
	case []any:
		for i := range v {
			v[i] = castParameters(v[i], sig, "")
		}
	case map[string]any:
		switch v["class"] {
		case "PARAMETER":
			if want == "" {
				want = paramText
			}
			return castNode(v, want)
		case "CAST":
			if child, _ := v["child"].(map[string]any); child != nil && child["class"] == "PARAMETER" {
				return v // the author's own cast
			}
		case "COMPARISON":
			group := groupType(sig, v["left"], v["right"])
			v["left"], v["right"] = castParameters(v["left"], sig, group), castParameters(v["right"], sig, group)
			return v
		case "BETWEEN":
			group := groupType(sig, v["input"], v["lower"], v["upper"])
			for _, key := range []string{"input", "lower", "upper"} {
				v[key] = castParameters(v[key], sig, group)
			}
			return v
		case "OPERATOR":
			if kind := v["type"]; kind == "COMPARE_IN" || kind == "COMPARE_NOT_IN" || kind == "OPERATOR_COALESCE" {
				children, _ := v["children"].([]any)
				group := groupType(sig, children...)
				for i := range children {
					children[i] = castParameters(children[i], sig, group)
				}
				return v
			}
		}
		for key, child := range v {
			switch child.(type) {
			case map[string]any, []any:
				v[key] = castParameters(child, sig, "")
			}
		}
	}
	return n
}

// groupType is the parameter type for a group of compared operands: the type
// of the first one operandType classifies, or "" when none is classified.
func groupType(sig *signal, operands ...any) string {
	for _, operand := range operands {
		if t := operandType(operand, sig); t != "" {
			return t
		}
	}
	return ""
}

// operandType classifies an operand as a number (paramNumber) or a time
// (paramTime), or returns "" for text, attribute lookups, parameters and
// anything else.
func operandType(n any, sig *signal) string {
	v, _ := n.(map[string]any)
	switch v["class"] {
	case "COLUMN_REF":
		names, _ := v["column_names"].([]any)
		if len(names) == 1 {
			name, _ := names[0].(string)
			switch sig.index[name].Type {
			case TypeNumber:
				return paramNumber
			case TypeTime:
				return paramTime
			}
		}
	case "CAST":
		typeExpr, _ := v["type_expr"].(map[string]any)
		name, _ := typeExpr["type_name"].(string)
		if numericCasts[name] {
			return paramNumber
		}
		if name == paramTime {
			return paramTime
		}
	case "CONSTANT":
		literal, _ := v["literal"].(map[string]any)
		if kind := literal["kind"]; kind == "INTEGER" || kind == "NUMERIC" {
			return paramNumber
		}
	case "FUNCTION":
		if v["function_name"] == "length" {
			return paramNumber
		}
	case "OPERATOR":
		if v["type"] == "OPERATOR_COALESCE" {
			children, _ := v["children"].([]any)
			return groupType(sig, children...)
		}
	}
	return ""
}

func castNode(param map[string]any, typeName string) map[string]any {
	return map[string]any{
		"alias": "", "class": "CAST", "type": "OPERATOR_CAST", "try_cast": false, "child": param,
		"type_expr": map[string]any{
			"alias": "", "catalog": "", "children": []any{}, "class": "TYPE", "schema": "", "type": "TYPE",
			"type_name": typeName, "qualified_name": map[string]any{"path": []any{typeName}},
		},
	}
}
