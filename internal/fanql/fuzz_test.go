package fanql

import (
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"unicode/utf8"

	"github.com/labstack/fanout/internal/panel"
)

func FuzzLexParsePrint(f *testing.F) {
	for _, fixture := range readCorpus(f) {
		f.Add(fixture.Text)
	}
	for _, text := range []string{"", "logs where \"api*\"", "logs where \"api$svc\"", "spans " + strings.Repeat("a:0 ", 256), "logs -(" + strings.Repeat("a ", 256) + ")", strings.Repeat("a", 4001), strings.Repeat("𝔘", 2049)} {
		f.Add(text)
	}
	f.Fuzz(func(t *testing.T, text string) {
		if len(text) > 65536 {
			return
		}
		tokens := Lex(text)
		runes := utf8.RuneCountInString(text)
		if len(tokens) > runes+1 || len(tokens) == 0 || tokens[len(tokens)-1].Kind != EOF {
			t.Fatalf("nonlinear or unterminated token stream: %d tokens for %d runes", len(tokens), runes)
		}
		for _, tok := range tokens {
			if tok.Span.Start < 0 || tok.Span.End < tok.Span.Start || tok.Span.End > runes {
				t.Fatalf("invalid token span: %+v", tok)
			}
		}
		q, ps := Parse(text, "")
		if len(ps) != 0 {
			if q != nil {
				t.Fatal("problems returned an executable tree")
			}
			if len(ps) > MaxProblems+1 {
				t.Fatalf("too many problems: %d", len(ps))
			}
			if runes > 4000 || len(text) > 8192 {
				if len(ps) != 1 || ps[0].Message != "a query is at most 4000 characters (8192 bytes)" {
					t.Fatalf("text bound: %+v", ps)
				}
			} else if len(tokens)-1 > 1024 {
				if len(ps) != 1 || ps[0].Message != "a query is at most 1024 tokens" {
					t.Fatalf("token bound: %+v", ps)
				}
			}
			return
		}
		printed := Print(q)
		var again *Query
		if editableReason(printed) != "" {
			// Printing is total and may expand short attribute spellings past an
			// interactive bound, or De Morgan negation past the node bound. The
			// internal exactness parser remains unbounded.
			if rejected, problems := Parse(printed, ""); rejected != nil || len(problems) == 0 {
				t.Fatal("expanded text ignored its bound")
			}
			p := &parser{runes: []rune(printed), tokens: Lex(printed), heights: make(map[any]int)}
			again = p.query("")
			if problems := p.finish(); len(problems) != 0 {
				t.Fatalf("unbounded print reparse %q: %+v", printed, problems)
			}
		} else {
			again, ps = Parse(printed, "")
			if len(ps) != 0 {
				t.Fatalf("print reparse %q: %+v", printed, ps)
			}
		}
		if next := Print(again); next != printed {
			t.Fatalf("not a fixed point: %q -> %q -> %q", text, printed, next)
		}
	})
}

func TestStructuredGeneratorCoversConditionalMeasuresAndArithmetic(t *testing.T) {
	engine, _ := newTestEngine(t)
	conditional, arithmetic := false, false
	for source := range 2 {
		for choice := range 16 {
			// Fixed bytes keep the generated filters short while varying measures.
			q := generatedStructuredQuery([]byte{byte(source), byte(choice), 1, 1, 1, 1, 1, 1})
			m, err := panel.ParseMeasure(q.From, q.Measures[0])
			if err != nil {
				t.Fatal(err)
			}
			switch x := m.Expr.(type) {
			case panel.MeasureCountIf:
				conditional = true
			case panel.MeasureBinary:
				arithmetic = true
				if _, ok := x.L.(panel.MeasureCountIf); ok {
					conditional = true
				}
			}
			f, ps, err := Format(t.Context(), engine, q, FormatOptions{})
			if err != nil || len(ps) != 0 || !f.Editable {
				t.Fatalf("format %#v: %+v %v", q, ps, err)
			}
			c, ps := Compile(f.Text, Options{})
			if len(ps) != 0 {
				t.Fatal(ps)
			}
			assertQueryTier(t, engine, q, c.Query, f.Exactness)
		}
	}
	if !conditional || !arithmetic {
		t.Fatalf("generator coverage: conditional=%t arithmetic=%t", conditional, arithmetic)
	}
}

// generatedStructuredQuery is generator B: a bounded grammar of SQL accepted
// by the existing gate, independent of FanQL's own lowerer and printer.
func generatedStructuredQuery(data []byte) panel.Query {
	byteAt := func(i int) byte {
		if len(data) == 0 {
			return 0
		}
		return data[i%len(data)]
	}
	source := []string{"spans", "logs", "metrics"}[int(byteAt(0))%3]
	measures := []string{"count()", "count(  )", "  count()  ", "count ( )"}
	if source != "metrics" {
		measures = append(measures,
			"count_if(service = '(')",
			"count_if(service = ')') as n",
			"count_if(attributes['( ) , |'] = ',') as n",
			"count_if(service = 'api') / count() as ratio",
			"(count_if(service = 'api') + count_if(service = 'worker')) / count() as ratio",
			"count() * 2 as doubled",
			"count() - count() as zero",
			"count() / 2 as half",
		)
	}
	q := panel.Query{From: source, Measures: []string{measures[int(byteAt(1))%len(measures)]}}
	if byteAt(2)%5 == 0 {
		// Every entry remains under 500 bytes; their complete text exceeds the
		// interactive character bound without invalidating the structured Query.
		for i := range 16 {
			q.Where = append(q.Where, "service = '"+strings.Repeat("x", 475)+fmt.Sprint(i)+"'")
		}
		return q
	}
	count := 1 + int(byteAt(3))%16
	for i := range count {
		value := []string{"api", "worker", "user's api", "api`worker", "api*", "$literal", "𝔘 [name]", "line\nvalue"}[int(byteAt(i+4))%8]
		literal := "'" + strings.ReplaceAll(value, "'", "''") + "'"
		var sql string
		switch byteAt(i+5) % 13 {
		case 0:
			sql = "service = " + literal
		case 1:
			sql = "service=" + literal
		case 2:
			sql = fmt.Sprintf("attributes['k'] = %d", byteAt(i+6))
		case 3:
			sql = "resource['k'] IS NULL"
		case 4:
			sql = "(service IS NULL OR service != " + literal + ")"
		case 5:
			sql = "service IN (" + literal + ", 'other')"
		case 6:
			sql = "coalesce(service, '') = " + literal
		case 7:
			sql = "NOT (attributes['k'] = " + literal + ")"
		case 8:
			sql = "lower(service) = " + literal
		case 9:
			sql = "CAST(service AS VARCHAR) = " + literal
		case 10:
			sql = "contains(lower(service),lower(" + literal + "))"
		case 11:
			sql = "service LIKE " + literal
		case 12:
			sql = "attributes['user''s name'] = " + literal
		}
		q.Where = append(q.Where, sql)
	}
	if byteAt(4)%3 == 0 {
		q.By = []string{[]string{"service", "attributes['user name']", "resource['𝔘 [name]']"}[int(byteAt(5))%3]}
	}
	return q
}

func FuzzFormatRoundTrip(f *testing.F) {
	type seed struct {
		Query     panel.Query      `json:"query"`
		Variables []panel.Variable `json:"variables,omitempty"`
		Viz       string           `json:"viz,omitempty"`
	}
	for _, fixture := range storedQueryFixtures(f) {
		if fixture.Panel.Query == nil {
			continue
		}
		raw, err := json.Marshal(seed{Query: *fixture.Panel.Query, Variables: fixture.Variables, Viz: fixture.Panel.Viz})
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	for _, fixture := range readCorpus(f) {
		if fixture.Query == nil {
			continue
		}
		raw, err := json.Marshal(seed{Query: *fixture.Query, Viz: fixture.Viz})
		if err != nil {
			f.Fatal(err)
		}
		f.Add(raw)
	}
	for _, data := range [][]byte{{0}, {1, 2, 3, 4, 5}, {2, 1, 9, 15, 17}} {
		raw, _ := json.Marshal(seed{Query: generatedStructuredQuery(data)})
		f.Add(raw)
	}
	engine, _ := newTestEngine(f)
	f.Fuzz(func(t *testing.T, data []byte) {
		if len(data) > 65536 {
			return
		}
		var input seed
		if json.Unmarshal(data, &input) != nil || input.Query.From == "" {
			input = seed{Query: generatedStructuredQuery(data)}
		}
		stored := input.Query
		if len(panel.CompatibleVizzes(&stored, nil)) == 0 {
			return
		}
		viz := input.Viz
		if viz == "" {
			viz = panel.CompatibleVizzes(&stored, nil)[0].Viz
		}
		checkedQuery := stored
		dashboard := panel.Dashboard{Name: "Fuzz", Variables: input.Variables, Panels: []panel.Panel{{ID: "fuzz", Title: "Fuzz", Viz: viz, Query: &checkedQuery}}}
		panel.Normalize(&dashboard)
		// JSON mutation can produce syntactically invalid SQL in a shape-valid
		// Query. The round-trip promise applies only to the gate's accepted input.
		if _, problems, err := panel.Check(t.Context(), engine, &dashboard); err != nil {
			t.Fatal(err)
		} else if len(problems) != 0 {
			return
		}
		formatted, ps, err := Format(t.Context(), engine, stored, FormatOptions{Variables: input.Variables})
		if err != nil || len(ps) != 0 || formatted == nil {
			t.Fatalf("format %#v: %+v %v", stored, ps, err)
		}
		if !formatted.Editable {
			// Complete, noneditable text is never sent through public Parse.
			before, _ := json.Marshal(stored)
			raw, err := json.Marshal(struct {
				FanQL    int         `json:"fanql"`
				Text     string      `json:"text"`
				Editable bool        `json:"editable"`
				Query    panel.Query `json:"query"`
			}{Version, formatted.Text, false, stored})
			if err != nil {
				t.Fatal(err)
			}
			var envelope struct {
				Query panel.Query `json:"query"`
			}
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatal(err)
			}
			after, _ := json.Marshal(envelope.Query)
			if string(after) != string(before) {
				t.Fatalf("envelope changed query: %s != %s", after, before)
			}
			return
		}
		compiled, ps := Compile(formatted.Text, Options{})
		if len(ps) != 0 || compiled == nil {
			t.Fatalf("recompile %q: %+v", formatted.Text, ps)
		}
		assertQueryTier(t, engine, stored, compiled.Query, formatted.Exactness)
		if len(stored.Where) != len(compiled.Query.Where) {
			t.Fatal("entry count changed")
		}
	})
}
