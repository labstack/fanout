package panel

import (
	"fmt"
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

var (
	measurePattern = regexp.MustCompile(`^\s*([A-Za-z_0-9]+)\s*\((.*)\)\s*(?:(?i:as)\s+(\S+))?\s*$`)
	aliasPattern   = regexp.MustCompile(`^[a-z][a-z0-9_]{0,39}$`)
)

func parseMeasure(sig *signal, text string) (Measure, string, string) {
	m := measurePattern.FindStringSubmatch(text)
	if m == nil {
		return Measure{}, "is not fn(field) [as alias]", "e.g. p95(duration_ms) or count()"
	}
	name := strings.ToLower(m[1])
	spec, ok := measureFuncs[name]
	if !ok {
		return Measure{}, fmt.Sprintf("unknown function %s", name), suggestOr(name, measureNames(), "functions: "+strings.Join(measureNames(), ", "))
	}
	if len(spec.signals) > 0 && !slices.Contains(spec.signals, sig.name) {
		return Measure{}, fmt.Sprintf("%s is not available for %s", name, sig.name), "available for " + strings.Join(spec.signals, ", ")
	}
	var args []string
	if inner := strings.TrimSpace(m[2]); inner != "" {
		for _, part := range strings.Split(inner, ",") {
			args = append(args, strings.TrimSpace(part))
		}
	}
	if len(args) != spec.args {
		return Measure{}, fmt.Sprintf("%s takes %d argument(s), got %d", name, spec.args, len(args)), ""
	}
	out := Measure{Text: strings.TrimSpace(text), Func: name, Additive: spec.additive, Unit: spec.unit, Q: spec.quantile}
	if spec.args >= 1 {
		ref, err := sig.field(args[0])
		if err != nil {
			return Measure{}, err.Error(), ""
		}
		if spec.numeric && ref.Type != TypeNumber && ref.Key == "" {
			return Measure{}, fmt.Sprintf("%s needs a numeric field; %s is %s", name, ref.Text, ref.Type), ""
		}
		out.Field = &ref
		if out.Unit == "" {
			out.Unit = ref.Unit
		}
	}
	if name == "quantile" {
		q, err := strconv.ParseFloat(args[1], 64)
		if err != nil || q <= 0 || q >= 1 {
			return Measure{}, "quantile q must be a number between 0 and 1", "e.g. quantile(duration_ms, 0.999)"
		}
		out.Q = q
	}
	if alias := m[3]; alias != "" {
		if !aliasPattern.MatchString(alias) {
			return Measure{}, fmt.Sprintf("alias %q must be lowercase letters, digits and underscores", alias), ""
		}
		out.Alias = alias
	}
	return out, "", ""
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
	for i, text := range texts {
		m, message, hint := parseMeasure(sig, text)
		if message != "" {
			problems.addHint(fmt.Sprintf("%s[%d]", path, i), fmt.Sprintf("%q %s", text, message), hint)
			continue
		}
		out = append(out, m)
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
