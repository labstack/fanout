package panel

import (
	"encoding/json"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
)

// Value is a variable's current value. JSON: a string, a list of strings, or
// "$__all".
type Value struct {
	All    bool
	Values []string
}

func valueOf(s string) Value {
	if s == AllValue {
		return Value{All: true}
	}
	return Value{Values: []string{s}}
}

func (v Value) MarshalJSON() ([]byte, error) {
	if v.All {
		return json.Marshal(AllValue)
	}
	if len(v.Values) == 1 {
		return json.Marshal(v.Values[0])
	}
	if v.Values == nil {
		return []byte("[]"), nil
	}
	return json.Marshal(v.Values)
}

func (v *Value) UnmarshalJSON(data []byte) error {
	var one string
	if err := json.Unmarshal(data, &one); err == nil {
		*v = valueOf(one)
		return nil
	}
	var many []string
	if err := json.Unmarshal(data, &many); err != nil {
		return errors.New("a variable value is a string or a list of strings")
	}
	if len(many) == 1 && many[0] == AllValue {
		*v = Value{All: true}
		return nil
	}
	*v = Value{Values: many}
	return nil
}

// Scope is everything a compiled query depends on besides the spec.
type Scope struct {
	Start, End time.Time
	Interval   time.Duration // bucket width; zero means no time grouping
	Vars       map[string]Value
}

// Column describes one frame column.
type Column struct {
	Name string `json:"name"`
	Type string `json:"type"`
	Role string `json:"role"`
	Unit string `json:"unit,omitempty"`
}

// Compiled is one statement ready to run.
type Compiled struct {
	SQL     string
	Args    []any
	Columns []Column
}

// dropped reports whether a filter references a variable set to
// All; such a filter is removed rather than compared with NULL.
func (s Scope) dropped(f Filter) bool {
	for _, name := range f.Params {
		v := s.Vars[name]
		if v.All {
			return true
		}
	}
	return false
}

func (s Scope) lookup(f Filter) func(string) ([]any, bool, error) {
	return func(name string) ([]any, bool, error) {
		v, ok := s.Vars[name]
		if !ok {
			return nil, false, fmt.Errorf("$%s has no value", name)
		}
		values := make([]any, len(v.Values))
		for i, value := range v.Values {
			values[i] = value
		}
		return values, f.InParams[name], nil
	}
}

// buildWhere renders the read window and the active filters. The window is
// always the first predicate: the snapshot binder prunes files by it.
func buildWhere(sig *signal, filters []Filter, scope Scope) (string, []any, error) {
	column := quoteIdent(sig.time)
	clauses := []string{fmt.Sprintf("%s >= ?::TIMESTAMP_NS::TIMESTAMPTZ_NS AND %s < ?::TIMESTAMP_NS::TIMESTAMPTZ_NS", column, column)}
	args := []any{scope.Start.UTC(), scope.End.UTC()}
	for _, f := range filters {
		if scope.dropped(f) {
			continue
		}
		empty := false
		for _, name := range f.Params {
			if v, ok := scope.Vars[name]; ok && !v.All && len(v.Values) == 0 {
				empty = true
			}
		}
		if empty {
			clauses = append(clauses, "FALSE")
			continue
		}
		text, _, values, err := bindParams(f.Expr, scope.lookup(f))
		if err != nil {
			return "", nil, err
		}
		clauses = append(clauses, "("+text+")")
		args = append(args, values...)
	}
	return strings.Join(clauses, " AND "), args, nil
}

// compileQuery turns a structured query into one statement. The bucket is
// selected as "_t" and named "time" in the frame, so it never collides with a
// signal's own time column.
func compileQuery(p *Panel, measures []Measure, filters []Filter, scope Scope) (Compiled, error) {
	q := p.Query
	sig, ok := lookupSignal(q.From)
	if !ok {
		return Compiled{}, fmt.Errorf("unknown signal %q", q.From)
	}
	where, args, err := buildWhere(sig, filters, scope)
	if err != nil {
		return Compiled{}, err
	}
	dims := make([]FieldRef, 0, len(q.By))
	for _, by := range q.By {
		ref, err := sig.field(by)
		if err != nil {
			return Compiled{}, err
		}
		dims = append(dims, ref)
	}
	var selects, groups []string
	var columns []Column
	bucket := ""
	if scope.Interval > 0 {
		bucket = fmt.Sprintf("time_bucket(INTERVAL '%d seconds', %s::TIMESTAMP_NS)", int64(scope.Interval/time.Second), quoteIdent(sig.time))
		selects = append(selects, "epoch_ms("+bucket+`)::BIGINT AS "_t"`)
		groups = append(groups, bucket)
		columns = append(columns, Column{Name: "time", Type: "time", Role: "time"})
	}
	top := 0
	if bucket != "" && len(dims) == 1 {
		top = p.Top()
	}
	for i, d := range dims {
		expr := d.stringSQL()
		if top > 0 && i == 0 {
			expr = fmt.Sprintf("CASE WHEN %s IN (SELECT d FROM top) THEN %s ELSE 'Other' END", expr, expr)
		}
		selects = append(selects, fmt.Sprintf("coalesce(%s, '') AS %s", expr, quoteIdent(d.alias())))
		groups = append(groups, expr)
		columns = append(columns, Column{Name: d.alias(), Type: "string", Role: "dimension"})
	}
	seconds := scope.Interval.Seconds()
	if seconds == 0 {
		seconds = scope.End.Sub(scope.Start).Seconds()
	}
	partition := ""
	if bucket != "" {
		partition = "PARTITION BY " + bucket
	}
	for _, m := range measures {
		selects = append(selects, measureSQL(m, sig, seconds, partition)+" AS "+quoteIdent(m.Alias))
		unit := m.Unit
		if unit == "none" {
			unit = "" // The catalog cannot infer a physical unit for this field.
		}
		columns = append(columns, Column{Name: m.Alias, Type: "number", Role: "measure", Unit: unit})
	}
	var b strings.Builder
	b.WriteString("WITH base AS (SELECT * FROM " + structuredSource(sig.name) + " WHERE " + where + ")")
	if top > 0 {
		fmt.Fprintf(&b, ", top AS (SELECT %s AS d FROM base GROUP BY 1 ORDER BY count(*) DESC LIMIT %d)", dims[0].stringSQL(), top)
	}
	b.WriteString(" SELECT " + strings.Join(selects, ", ") + " FROM base")
	if len(groups) > 0 {
		b.WriteString(" GROUP BY " + strings.Join(groups, ", "))
	}
	b.WriteString(orderAndLimit(p, measures, bucket != "", len(dims)))
	return Compiled{SQL: b.String(), Args: args, Columns: columns}, nil
}

func measureSQL(m Measure, sig *signal, seconds float64, partition string) string {
	switch m.Func {
	case "count":
		return "count(*)::DOUBLE"
	case "rate":
		return "count(*) / " + sqlFloat(seconds)
	case "error_rate":
		if sig.name == "logs" {
			return "100.0 * avg(CASE WHEN upper(severity) IN ('ERROR', 'FATAL', 'CRITICAL') OR severity_number >= 17 THEN 1.0 ELSE 0.0 END)"
		}
		return "100.0 * avg(CASE WHEN status IN ('STATUS_CODE_ERROR', 'ERROR') THEN 1.0 ELSE 0.0 END)"
	case "share":
		return fmt.Sprintf("100.0 * count(*) / sum(count(*)) OVER (%s)", partition)
	case "avg", "min", "max", "sum":
		return fmt.Sprintf("%s(%s)::DOUBLE", m.Func, m.Field.numberSQL())
	case "last":
		return fmt.Sprintf("arg_max(%s, %s)::DOUBLE", m.Field.numberSQL(), quoteIdent(sig.time))
	case "count_distinct":
		return fmt.Sprintf("count(DISTINCT %s)::DOUBLE", m.Field.stringSQL())
	default:
		return fmt.Sprintf("quantile_cont(%s, %s)::DOUBLE", m.Field.numberSQL(), strconv.FormatFloat(m.Q, 'f', -1, 64))
	}
}

func sqlFloat(v float64) string {
	text := strconv.FormatFloat(v, 'f', -1, 64)
	if !strings.Contains(text, ".") {
		text += ".0"
	}
	return text
}

func orderAndLimit(p *Panel, measures []Measure, bucketed bool, dims int) string {
	if bucketed {
		order := ` ORDER BY "_t"`
		if dims > 0 {
			order += ", 2"
		}
		return order + fmt.Sprintf(" LIMIT %d", maxSeriesPoints*(p.Top()+1))
	}
	limit := p.Query.Limit
	if limit == 0 {
		switch p.Viz {
		case "bar":
			limit = 10
		case "table":
			limit = 100
		default:
			limit = 1000
		}
	}
	if len(measures) == 0 {
		return fmt.Sprintf(" LIMIT %d", limit)
	}
	alias, direction := measures[0].Alias, "DESC"
	if p.Query.Sort != "" {
		alias = strings.TrimPrefix(p.Query.Sort, "+")
		if strings.HasPrefix(p.Query.Sort, "+") {
			direction = "ASC"
		}
	}
	return fmt.Sprintf(" ORDER BY %s %s NULLS LAST LIMIT %d", quoteIdent(alias), direction, limit)
}
