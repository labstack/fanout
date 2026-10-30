package fanql

import (
	"context"
	"encoding/json"
	"fmt"
	"go/ast"
	goparser "go/parser"
	"go/token"
	"math/rand"
	"os"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/dashboard"
	"github.com/labstack/fanout/internal/panel"
)

type storedQueryFixture struct {
	Name      string
	Panel     panel.Panel
	Variables []panel.Variable
}

// Read the actual product fixtures, so additions to any dashboard automatically
// join the round-trip contract without a second manually maintained copy.
func storedQueryFixtures(t testing.TB) []storedQueryFixture {
	t.Helper()
	var out []storedQueryFixture
	add := func(name string, p panel.Panel, vars []panel.Variable) {
		if p.Query == nil {
			return
		}
		d := panel.Dashboard{Name: name, Variables: vars, Panels: []panel.Panel{p}}
		panel.Normalize(&d)
		out = append(out, storedQueryFixture{Name: name, Panel: d.Panels[0], Variables: d.Variables})
	}
	d := dashboard.DefaultSpec()
	for _, p := range d.Panels {
		add("default/"+p.ID, p, d.Variables)
	}
	raw, err := os.ReadFile("../../scripts/dashboard-eval/testdata/server.json")
	if err != nil {
		t.Fatal(err)
	}
	var root any
	if err := json.Unmarshal(raw, &root); err != nil {
		t.Fatal(err)
	}
	var walk func(any, string, []panel.Variable)
	walk = func(v any, path string, vars []panel.Variable) {
		switch x := v.(type) {
		case map[string]any:
			if value, ok := x["variables"]; ok {
				b, _ := json.Marshal(value)
				if err := json.Unmarshal(b, &vars); err != nil {
					t.Fatal(err)
				}
			}
			if _, ok := x["query"]; ok {
				b, _ := json.Marshal(x)
				var p panel.Panel
				if err := json.Unmarshal(b, &p); err != nil {
					t.Fatal(err)
				}
				add(path, p, vars)
			}
			for _, key := range referenceKeys(x) {
				walk(x[key], path+"/"+key, vars)
			}
		case []any:
			for i, item := range x {
				walk(item, fmt.Sprintf("%s/%d", path, i), vars)
			}
		}
	}
	walk(root, "eval", nil)
	source, err := goparser.ParseFile(token.NewFileSet(), "../mcp/panel_examples.go", nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	ast.Inspect(source, func(n ast.Node) bool {
		decl, ok := n.(*ast.ValueSpec)
		if !ok || len(decl.Values) != 1 || !strings.HasSuffix(decl.Names[0].Name, "Example") {
			return true
		}
		literal, ok := decl.Values[0].(*ast.BasicLit)
		if !ok || literal.Kind != token.STRING {
			return true
		}
		text, err := strconv.Unquote(literal.Value)
		if err != nil {
			t.Fatal(err)
		}
		var d panel.Dashboard
		if err := json.Unmarshal([]byte(text), &d); err != nil {
			t.Fatal(err)
		}
		for _, p := range d.Panels {
			add("mcp/"+decl.Names[0].Name+"/"+p.ID, p, d.Variables)
		}
		return false
	})
	if len(out) < 30 {
		t.Fatalf("only %d stored fixture queries found", len(out))
	}
	return out
}

func canonicalSQL(t testing.TB, p panel.Parser, signal, filter string) string {
	t.Helper()
	n, err := p.ParseSQL(context.Background(), "SELECT 1 FROM "+signal+" WHERE ("+filter+")")
	if err != nil {
		t.Fatal(err)
	}
	s, err := p.RenderSQL(context.Background(), n)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

func measureMeaning(t testing.TB, p panel.Parser, signal string, expr panel.MeasureExpr) any {
	t.Helper()
	switch x := expr.(type) {
	case panel.MeasureCall:
		if x.Field != nil {
			f := *x.Field
			f.Text = ""
			x.Field = &f
		}
		return x
	case panel.MeasureCountIf:
		return canonicalSQL(t, p, signal, x.Filter)
	case panel.MeasureConst:
		return x.Value
	case panel.MeasureNeg:
		return []any{"neg", measureMeaning(t, p, signal, x.X)}
	case panel.MeasureBinary:
		return []any{x.Op, measureMeaning(t, p, signal, x.L), measureMeaning(t, p, signal, x.R)}
	default:
		t.Fatalf("unknown measure expression %T", expr)
		return nil
	}
}

func assertQueryTier(t testing.TB, p panel.Parser, stored, lowered panel.Query, tier string) {
	t.Helper()
	if len(stored.Where) != len(lowered.Where) {
		t.Fatalf("entry boundaries changed: %+v -> %+v", stored, lowered)
	}
	if tier == "exact" {
		if !reflect.DeepEqual(stored, lowered) {
			t.Fatalf("not byte-exact: %#v -> %#v", stored, lowered)
		}
		return
	}
	if tier != "normalized" {
		t.Fatalf("unexpected public tier %q", tier)
	}
	a, b := stored, lowered
	a.Where, b.Where, a.Measures, b.Measures = nil, nil, nil, nil
	if !reflect.DeepEqual(a, b) {
		t.Fatalf("query structure changed: %#v -> %#v", a, b)
	}
	for i := range stored.Where {
		if canonicalSQL(t, p, stored.From, stored.Where[i]) != canonicalSQL(t, p, lowered.From, lowered.Where[i]) {
			t.Fatalf("filter meaning changed: %q -> %q", stored.Where[i], lowered.Where[i])
		}
	}
	if len(stored.Measures) != len(lowered.Measures) {
		t.Fatal("measure count changed")
	}
	for i, text := range stored.Measures {
		a, err := panel.ParseMeasure(stored.From, text)
		if err != nil {
			t.Fatal(err)
		}
		b, err := panel.ParseMeasure(lowered.From, lowered.Measures[i])
		if err != nil {
			t.Fatal(err)
		}
		if a.Func != b.Func || a.Alias != b.Alias || a.Unit != b.Unit || !reflect.DeepEqual(measureMeaning(t, p, stored.From, a.Expr), measureMeaning(t, p, stored.From, b.Expr)) {
			t.Fatalf("measure meaning changed: %q -> %q", text, lowered.Measures[i])
		}
	}
}

func TestRoundTripCorpusFixedPoint(t *testing.T) {
	engine, _ := newTestEngine(t)
	for _, fixture := range readCorpus(t) {
		if fixture.Query == nil || fixture.GateProblems {
			continue
		}
		c, ps := Compile(fixture.Text, Options{Viz: fixture.Viz})
		if len(ps) != 0 {
			t.Fatal(ps)
		}
		// Section 0.11 explicitly keeps raw SQL and string-transform operands
		// under the engine's authority. The structured Query has no origin
		// metadata that could distinguish those authored forms from stored SQL.
		p := &countingParser{Parser: engine, fail: !strings.ContainsRune(fixture.Text, '`') && !strings.Contains(strings.ToLower(fixture.Text), "lower(") && !strings.Contains(strings.ToLower(fixture.Text), "upper(")}
		f, ps, err := Format(t.Context(), p, c.Query, FormatOptions{Viz: fixture.Viz})
		// Compile can parse filter-only metrics without a viz. There is no
		// metrics row viz, so these are outside Format's valid-query contract.
		if len(panel.CompatibleVizzes(&c.Query, nil)) == 0 {
			if f != nil || len(ps) == 0 || err != nil {
				t.Fatalf("invalid shape %q: %+v %+v %v", fixture.Text, f, ps, err)
			}
			continue
		}
		if err != nil || len(ps) != 0 {
			t.Fatalf("%q: %+v %v", fixture.Text, ps, err)
		}
		if !f.Editable || f.Exactness != "exact" || p.fail && (f.Text != c.Canonical || p.calls != 0) {
			t.Fatalf("%q: %+v; calls=%d; canonical %q", fixture.Text, f, p.calls, c.Canonical)
		}
		// A raw fragment may lower identically to native shorthand. Without
		// origin metadata Format chooses that shorthand, preserving exact SQL.
		if !p.fail {
			again, ps := Compile(f.Text, Options{Viz: fixture.Viz})
			if len(ps) != 0 {
				t.Fatal(ps)
			}
			assertQueryTier(t, engine, c.Query, again.Query, f.Exactness)
		}
	}
}

func TestRoundTripStoredDashboards(t *testing.T) {
	engine, _ := newTestEngine(t)
	for _, fixture := range storedQueryFixtures(t) {
		t.Run(fixture.Name, func(t *testing.T) {
			p := fixture.Panel
			stored := *p.Query
			f, ps, err := Format(t.Context(), engine, stored, FormatOptions{Viz: p.Viz, Variables: fixture.Variables})
			if err != nil || len(ps) != 0 {
				t.Fatalf("format: %+v %v", ps, err)
			}
			if !f.Editable {
				for _, entry := range stored.Where {
					if !strings.Contains(f.Text, entry) && !strings.Contains(f.Text, strings.ReplaceAll(entry, "`", "``")) {
						t.Fatalf("oversized text lost %q", entry)
					}
				}
				return
			}
			c, ps := Compile(f.Text, Options{Viz: p.Viz, Variables: fixture.Variables})
			if len(ps) != 0 {
				t.Fatalf("%q: %+v", f.Text, ps)
			}
			assertQueryTier(t, engine, stored, c.Query, f.Exactness)
			d := panel.Dashboard{Version: 1, Name: "round trip", Variables: fixture.Variables, Panels: []panel.Panel{p}}
			panel.Normalize(&d)
			_, before, err := panel.Check(t.Context(), engine, &d)
			if err != nil {
				t.Fatal(err)
			}
			d.Panels[0].Query = &c.Query
			_, after, err := panel.Check(t.Context(), engine, &d)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("Check changed: %+v -> %+v", before, after)
			}
		})
	}
}

func ownQueryText(r *rand.Rand, i int) string {
	signal := panel.SignalNames()[r.Intn(3)]
	columns, _ := panel.SignalColumns(signal)
	var fields []panel.Field
	for _, f := range columns {
		if f.Type == panel.TypeNumber || f.Type == panel.TypeString {
			fields = append(fields, f)
		}
	}
	f := fields[r.Intn(len(fields))]
	value := `"api*$'𝔘"`
	if f.Type == panel.TypeNumber {
		value = strconv.Itoa(r.Intn(1000) - 100)
	}
	filter := f.Name + ":" + value
	if signal == "metrics" {
		filter = "name:fixture " + filter
	}
	switch i % 8 {
	case 0:
		filter += " service:$svc"
	case 1:
		filter += " (attributes['flag']:true or resource['user name']:x)"
	case 2:
		filter += " -attributes['missing.key']:*"
	case 3:
		filter += " `coalesce(service, '``') = 'api'`"
	case 4:
		filter += " service in $svcs"
	case 5:
		filter += " -(attributes['n']>2 attributes['n']<9)"
	}
	if i%11 == 0 && signal != "logs" {
		field := "duration"
		if signal == "metrics" {
			field = "value"
		}
		return signal + " where " + filter + " | histogram(" + field + ") by service"
	}
	var measures []panel.MeasureSpec
	for _, m := range panel.MeasureSpecs() {
		if m.Name == "count_if" {
			continue
		}
		if len(m.Signals) == 0 || containsString(m.Signals, signal) {
			measures = append(measures, m)
		}
	}
	m := measures[r.Intn(len(measures))]
	measure := m.Name + "()"
	arg := "duration"
	if signal == "logs" {
		arg = "severity_number"
	} else if signal == "metrics" {
		arg = "value"
	}
	if m.Args == 1 {
		measure = m.Name + "(" + arg + ")"
	} else if m.Args == 2 {
		measure = m.Name + "(" + arg + ", 0.95)"
	}
	if i%7 == 0 && signal != "metrics" {
		measure = "count_if(service:\"api\") / count() * 100 as ratio"
	}
	return signal + " where " + filter + " | " + measure + " by service"
}

func containsString(values []string, value string) bool {
	for _, item := range values {
		if item == value {
			return true
		}
	}
	return false
}

func TestRoundTripProperties(t *testing.T) {
	engine, _ := newTestEngine(t)
	r := rand.New(rand.NewSource(20261029))
	vars := []panel.Variable{{Name: "svc", Kind: "text"}, {Name: "svcs", Kind: "custom", Options: []string{"api", "worker"}, Multi: true}}
	for i := range 500 {
		text := ownQueryText(r, i)
		c, ps := Compile(text, Options{Variables: vars})
		if len(ps) != 0 {
			t.Fatalf("A[%d] %q: %+v", i, text, ps)
		}
		p := &countingParser{Parser: engine, fail: !strings.ContainsRune(text, '`')}
		f, ps, err := Format(t.Context(), p, c.Query, FormatOptions{Variables: vars})
		if err != nil || len(ps) != 0 || f == nil {
			t.Fatalf("A[%d] %q: %+v %v", i, text, ps, err)
		}
		if !f.Editable || f.Exactness != "exact" || f.Text != c.Canonical || p.fail && p.calls != 0 {
			t.Fatalf("A[%d] %q: %+v; calls=%d", i, text, f, p.calls)
		}
		again, ps := Compile(f.Text, Options{Variables: vars})
		if len(ps) != 0 {
			t.Fatal(ps)
		}
		assertQueryTier(t, engine, c.Query, again.Query, f.Exactness)
		bytes := make([]byte, 32)
		_, _ = r.Read(bytes)
		stored := generatedStructuredQuery(bytes)
		formatted, ps, err := Format(t.Context(), engine, stored, FormatOptions{})
		if err != nil || len(ps) != 0 {
			t.Fatalf("B[%d]: %+v %v", i, ps, err)
		}
		// Count runes/bytes/tokens independently; parser bounds cover AST size
		// and depth in their own tests. Generator B stays within those bounds.
		within := len([]rune(formatted.Text)) <= 4000 && len(formatted.Text) <= 8192 && len(Lex(formatted.Text))-1 <= 1024
		if formatted.Editable != within {
			t.Fatalf("B[%d] editable disagrees with independent bounds: %+v", i, formatted)
		}
		if formatted.Editable {
			lowered, ps := Compile(formatted.Text, Options{})
			if len(ps) != 0 {
				t.Fatal(ps)
			}
			assertQueryTier(t, engine, stored, lowered.Query, formatted.Exactness)
			p := panel.Panel{ID: "p", Title: "p", Viz: "table", Query: &stored}
			d := panel.Dashboard{Name: "property", Panels: []panel.Panel{p}}
			panel.Normalize(&d)
			_, before, err := panel.Check(t.Context(), engine, &d)
			if err != nil {
				t.Fatal(err)
			}
			d.Panels[0].Query = &lowered.Query
			_, after, err := panel.Check(t.Context(), engine, &d)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(before, after) {
				t.Fatalf("B[%d] Check changed: %+v -> %+v", i, before, after)
			}
		}
	}
}

func TestEntryBoundaryShapesDiffer(t *testing.T) {
	engine, _ := newTestEngine(t)
	queries := []panel.Query{{From: "spans", Where: []string{`attributes['a'] = 1`, `attributes['b'] = 2`}}, {From: "spans", Where: []string{`attributes['a'] = 1 AND attributes['b'] = 2`}}}
	texts := []string{}
	for _, q := range queries {
		f, ps, err := Format(t.Context(), engine, q, FormatOptions{})
		if err != nil || len(ps) != 0 {
			t.Fatal(ps, err)
		}
		texts = append(texts, f.Text)
		c, ps := Compile(f.Text, Options{})
		if len(ps) != 0 {
			t.Fatal(ps)
		}
		assertQueryTier(t, engine, q, c.Query, f.Exactness)
	}
	if texts[0] == texts[1] || texts[0] != `spans where attributes['a']:1 attributes['b']:2` || texts[1] != `spans where (attributes['a']:1 attributes['b']:2)` {
		t.Fatal(texts)
	}
}

func TestEditableFalsePreservationThroughEnvelope(t *testing.T) {
	q := panel.Query{From: "logs"}
	for i := range 16 {
		q.Where = append(q.Where, "body = '"+strings.Repeat("x", 480)+strconv.Itoa(i)+"'")
	}
	p := &countingParser{fail: true}
	f, ps, err := Format(t.Context(), p, q, FormatOptions{Viz: "logs"})
	if err != nil || len(ps) != 0 || f == nil || f.Editable || f.Reason == "" || p.calls != 0 {
		t.Fatalf("oversized format: %+v %+v %v; engine calls=%d", f, ps, err, p.calls)
	}
	type envelope struct {
		FanQL    int         `json:"fanql"`
		Text     string      `json:"text"`
		Editable bool        `json:"editable"`
		Query    panel.Query `json:"query"`
		Viz      string      `json:"viz"`
		Time     panel.Time  `json:"time"`
	}
	original := envelope{FanQL: Version, Text: f.Text, Query: q, Viz: "logs", Time: panel.Time{Range: "1h"}}
	raw, err := json.Marshal(original)
	if err != nil {
		t.Fatal(err)
	}
	// The backend envelope carries the structured representation. Noneditable
	// text is never submitted to the bounded public Parse interface here.
	var decoded envelope
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	raw, err = json.Marshal(decoded)
	if err != nil {
		t.Fatal(err)
	}
	var saved envelope
	if err := json.Unmarshal(raw, &saved); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(original, saved) || saved.Editable || p.calls != 0 {
		t.Fatalf("envelope lost structured state: %+v", saved)
	}
	for i := range 16 {
		if !strings.Contains(saved.Text, strings.Repeat("x", 480)+strconv.Itoa(i)) {
			t.Fatalf("lost literal %d", i)
		}
	}
}
