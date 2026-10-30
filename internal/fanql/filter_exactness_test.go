package fanql

import (
	"database/sql"
	"reflect"
	"strconv"
	"testing"

	"github.com/labstack/fanout/internal/panel"
)

func TestRawFragmentsPreservePrecedence(t *testing.T) {
	c, ps := Compile("logs where (`body = 'a' OR body = 'b'` service:api)", Options{})
	if len(ps) != 0 {
		t.Fatal(ps)
	}
	want := []string{"((body = 'a' OR body = 'b') AND service = 'api')"}
	if !reflect.DeepEqual(c.Query.Where, want) {
		t.Fatalf("got %q, want %q", c.Query.Where, want)
	}
}

func TestPrintQuotedLexicalCharacters(t *testing.T) {
	for _, text := range []string{`logs where "api*"`, `logs where "api$svc"`, `logs where service:"api*"`, `logs where service:"api$svc"`} {
		t.Run(text, func(t *testing.T) {
			a, ps := Compile(text, Options{})
			if len(ps) != 0 {
				t.Fatal(ps)
			}
			b, ps := Compile(a.Canonical, Options{})
			if len(ps) != 0 {
				t.Fatalf("canonical %q: %+v", a.Canonical, ps)
			}
			if b.Canonical != a.Canonical || !reflect.DeepEqual(a.Query, b.Query) {
				t.Fatalf("not a fixed point: %+v -> %+v", a, b)
			}
		})
	}
}

func TestPrintParsePrintFixtureCorpus(t *testing.T) {
	texts := append([]string(nil), glanceExamples...)
	for _, fixture := range lowerFixtures {
		texts = append(texts, fixture.text)
	}
	for _, fixture := range canonicalCases {
		texts = append(texts, fixture[0])
	}
	for _, text := range texts {
		a, ps := Compile(text, Options{})
		if len(ps) != 0 {
			t.Fatal(text, ps)
		}
		b, ps := Compile(a.Canonical, Options{})
		if len(ps) != 0 {
			t.Fatalf("%q -> %q: %+v", text, a.Canonical, ps)
		}
		if a.Canonical != b.Canonical || !reflect.DeepEqual(a.Query, b.Query) {
			t.Fatalf("not a fixed point: %q -> %q -> %q", text, a.Canonical, b.Canonical)
		}
	}
}

func TestPrintFunctionSearchWordsBeforeGroups(t *testing.T) {
	for _, name := range panel.FilterFunctionNames() {
		standalone, ps := Compile("logs "+name, Options{})
		if len(ps) != 0 || standalone.Canonical != "logs where "+name {
			t.Fatalf("standalone search spelling: %+v %+v", standalone, ps)
		}
		text := "logs " + name + " -(a b)"
		a, ps := Compile(text, Options{})
		if len(ps) != 0 {
			t.Fatal(text, ps)
		}
		b, ps := Compile(a.Canonical, Options{})
		if len(ps) != 0 || b == nil {
			t.Fatalf("%q -> %q: %+v", text, a.Canonical, ps)
		}
		if b.Canonical != a.Canonical || !reflect.DeepEqual(a.Query, b.Query) {
			t.Fatalf("not fixed: %+v -> %+v", a, b)
		}
	}
}

func TestPrintContainsKeepsStoredLiteralText(t *testing.T) {
	for _, text := range []string{`spans where k ~ TRUE`, `spans where k !~ FALSE`, `logs where severity ~ TRUE`, `logs where service ~ FALSE`, `spans where k ~ 2E3`} {
		a, ps := Compile(text, Options{})
		if len(ps) != 0 {
			t.Fatal(text, ps)
		}
		b, ps := Compile(a.Canonical, Options{})
		if len(ps) != 0 || b == nil || !reflect.DeepEqual(a.Query, b.Query) {
			t.Fatalf("%q -> %q: %+v %+v", text, a.Canonical, b, ps)
		}
	}
}

func TestPrintDurationMSKeepsUnit(t *testing.T) {
	a, ps := Compile(`spans where duration_ms>500`, Options{})
	if len(ps) != 0 {
		t.Fatal(ps)
	}
	if a.Canonical != `spans where duration>500ms` {
		t.Fatalf("canonical: %q", a.Canonical)
	}
	b, ps := Compile(a.Canonical, Options{})
	if len(ps) != 0 || !reflect.DeepEqual(a.Query, b.Query) {
		t.Fatalf("round trip: %+v %+v", b, ps)
	}
}

func TestDurationSpellingPreservesMillisecondThresholds(t *testing.T) {
	for _, ms := range []float64{1001, 1002, 1003, 60001, 3600001, .1, .001, .0001, 1.3, 1e100, 1e308} {
		text := "spans where duration_ms>" + strconv.FormatFloat(ms, 'g', -1, 64)
		a, ps := Compile(text, Options{})
		if len(ps) != 0 {
			t.Fatal(text, ps)
		}
		b, ps := Compile(a.Canonical, Options{})
		if len(ps) != 0 || b == nil || !reflect.DeepEqual(a.Query, b.Query) {
			t.Fatalf("%q -> %q: %+v %+v", text, a.Canonical, b, ps)
		}
	}
}

func TestPrintNativeNegativeDurationRemainsNumeric(t *testing.T) {
	for _, text := range []string{`spans where duration_ms>-3`, `spans where duration_ms between -3 and 5`, `spans where duration_ms in (-3, 5)`} {
		a, ps := Compile(text, Options{})
		if len(ps) != 0 {
			t.Fatal(ps)
		}
		b, ps := Compile(a.Canonical, Options{})
		if len(ps) != 0 || b == nil {
			t.Fatalf("%q -> %q: %+v", text, a.Canonical, ps)
		}
		if !reflect.DeepEqual(a.Query, b.Query) {
			t.Fatalf("query changed: %+v -> %+v", a.Query, b.Query)
		}
	}
}

func TestSortPreservesMeasureAliasDuration(t *testing.T) {
	for _, alias := range []string{"duration", "duration_ms"} {
		text := "spans | count() as " + alias + " sort " + alias
		a, ps := Compile(text, Options{Viz: "table"})
		if len(ps) != 0 {
			t.Fatal(text, ps)
		}
		if a.Query.Sort != alias || a.Canonical != text {
			t.Fatalf("alias rewritten: %+v", a)
		}
	}
}

func TestHTTPStatusVariableRangeUsesNumericCast(t *testing.T) {
	c, ps := Compile(`spans where http_status_code>=$code`, Options{Variables: []panel.Variable{{Name: "code"}}})
	if len(ps) != 0 {
		t.Fatal(ps)
	}
	if got := c.Query.Where[0]; got != `TRY_CAST(http_status_code AS INTEGER) >= $code` {
		t.Fatal(got)
	}
	// The string dashboard value "500" must have the same numeric ordering as
	// the literal 500, including a status such as "60" that sorts after "500".
	d, _ := newTestEngine(t)
	var n int
	err := d.QueryRowScan(t.Context(), []any{&n}, `SELECT count(*) AS n FROM (VALUES ('500'), ('60')) t(http_status_code) WHERE `+c.Query.Where[0], sql.Named("code", "500"))
	if err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatalf("matched %d rows, want 1", n)
	}
}

func TestComparedCallVariablesBindAndRespectCountIf(t *testing.T) {
	t.Run("bind", func(t *testing.T) {
		c, ps := Compile(`logs where lower(service)=$svc`, Options{Variables: []panel.Variable{{Name: "svc"}}})
		if len(ps) != 0 {
			t.Fatal(ps)
		}
		if got := c.Query.Where[0]; got != `lower(service) = $svc` {
			t.Fatal(got)
		}
	})
	t.Run("count_if", func(t *testing.T) {
		c, ps := Compile(`spans | count_if(length(operation)>$x)`, Options{})
		if c != nil || len(ps) != 1 || ps[0].Message != "variables are not allowed inside count_if" || ps[0].Span == nil {
			t.Fatalf("got %+v %+v", c, ps)
		}
	})
}

func TestNonFiniteLiteralsHavePositionedProblems(t *testing.T) {
	for _, text := range []string{`spans where duration_ms>1e309`, `spans where duration>1e308h`} {
		t.Run(text, func(t *testing.T) {
			c, ps := Compile(text, Options{})
			if c != nil || len(ps) != 1 || ps[0].Message != "number must be finite" || ps[0].Span == nil || ps[0].Span.End != len(text) {
				t.Fatalf("got %+v %+v", c, ps)
			}
		})
	}
}

func TestEmptyBracketKeyIsRejected(t *testing.T) {
	c, ps := Compile(`logs where attributes['']:x`, Options{})
	if c != nil || len(ps) != 1 || ps[0].Message != "attribute keys must not be empty" || ps[0].Span == nil {
		t.Fatalf("got %+v %+v", c, ps)
	}
}

func TestGroupByAcceptsLiteralKeys(t *testing.T) {
	engine, _ := newTestEngine(t)
	for _, text := range []string{`spans | count() by attributes['user name']`, `spans | count() by attributes['user''s name']`, `spans | count() by resource['𝔘 [name]']`} {
		c, ps := Compile(text, Options{Viz: "table"})
		if len(ps) != 0 {
			t.Fatal(text, ps)
		}
		if len(c.Query.By) != 1 {
			t.Fatal(c)
		}
		if text == `spans | count() by attributes['user name']` && c.Query.By[0] != `attributes['user name']` {
			t.Fatal(c.Query.By)
		}
		d, ps := Compile(c.Canonical, Options{Viz: "table"})
		if len(ps) != 0 || !reflect.DeepEqual(c.Query, d.Query) {
			t.Fatalf("round trip: %+v %+v", d, ps)
		}
		dashboard := panel.Dashboard{Name: "keys", Panels: []panel.Panel{{ID: "keys", Title: "Keys", Viz: "table", Query: &c.Query}}}
		panel.Normalize(&dashboard)
		if _, ps, err := panel.Check(t.Context(), engine, &dashboard); len(ps) != 0 || err != nil {
			t.Fatalf("engine check for %q: %+v %v", text, ps, err)
		}
	}
}

func TestLiteralKeyQuotesSurviveCheckedMetadata(t *testing.T) {
	c, ps := Compile(`spans where attributes['user''s name']:api | count()`, Options{Viz: "table"})
	if len(ps) != 0 {
		t.Fatal(ps)
	}
	engine, _ := newTestEngine(t)
	d := panel.Dashboard{Name: "Keys", Panels: []panel.Panel{{ID: "keys", Title: "Keys", Viz: "table", Query: &c.Query}}}
	panel.Normalize(&d)
	checked, ps, err := panel.Check(t.Context(), engine, &d)
	if err != nil || len(ps) != 0 {
		t.Fatal(ps, err)
	}
	if field := checked.Filters["keys"][0].EqField; field != `attributes['user''s name']` {
		t.Fatalf("unsafe metadata field: %q", field)
	}
}
