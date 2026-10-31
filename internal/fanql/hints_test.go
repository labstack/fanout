package fanql

import (
	"context"
	"slices"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/query"
)

// Foreign forms get one problem with a fix-it that makes the query valid.
func TestCompileForeignFormsFixToFanQL(t *testing.T) {
	for _, tt := range []struct{ text, message, fixed string }{
		{"spans | percentile(duration, 0.95)", "percentile(...) is not FanQL; write quantile(duration, 0.95)", "spans | quantile(duration, 0.95)"},
		{"spans | percentile(duration, 99.9)", "percentile(...) is not FanQL; write quantile(duration, 0.999)", "spans | quantile(duration, 0.999)"},
		{"spans | percentile(duration, 95) by service", "percentile(...) is not FanQL; write p95(duration)", "spans | p95(duration) by service"},
		{"spans | percentile(duration, 95) every 1m", "percentile(...) is not FanQL; write p95(duration)", "spans | p95(duration) every 1m"},
		{"spans | count() group by service", `"group by" is not FanQL; use by after measures, e.g. | count() by service`, "spans | count() by service"},
		{"spans | count() GROUP BY service", `"group by" is not FanQL; use by after measures, e.g. | count() by service`, "spans | count() by service"},
		{"spans | count() | group by service", `"group by" is not FanQL; use by after measures, e.g. | count() by service`, "spans | count() by service"},
		{"spans take 10", `"take" is not FanQL; use limit, e.g. limit 10`, "spans limit 10"},
		{"spans where service:x head 5", `"head" is not FanQL; use limit, e.g. limit 5`, "spans where service:x limit 5"},
	} {
		c, ps := Compile(tt.text, Options{})
		if c != nil || len(ps) != 1 || ps[0].Message != tt.message || len(ps[0].FixIts) == 0 {
			t.Errorf("%q: %+v", tt.text, ps)
			continue
		}
		fix := ps[0].FixIts[0]
		runes := []rune(tt.text)
		fixed := string(runes[:fix.Span.Start]) + fix.Text + string(runes[fix.Span.End:])
		if fixed != tt.fixed {
			t.Errorf("%q fixes to %q, want %q", tt.text, fixed, tt.fixed)
		} else if _, ps := Compile(fixed, Options{}); len(ps) != 0 {
			t.Errorf("%q fix does not compile: %+v", fixed, ps)
		}
	}
}

func TestCompileFieldProblemsNameTheirCause(t *testing.T) {
	for _, tt := range []struct {
		text, message string
		fixes         []string
	}{
		{"spans | count() by $x", "by cannot use the variable $x; group by a field, e.g. by service", nil},
		{"spans where $x:foo", "the variable $x cannot name a field; use it as a value, e.g. service:$x", nil},
		{"spans where opération:x", `"opération" needs brackets`, []string{"operation", "attributes['opération']"}},
	} {
		c, ps := Compile(tt.text, Options{})
		var fixes []string
		if len(ps) == 1 {
			for _, f := range ps[0].FixIts {
				fixes = append(fixes, f.Text)
			}
		}
		if c != nil || len(ps) != 1 || ps[0].Message != tt.message || !slices.Equal(fixes, tt.fixes) {
			t.Errorf("%q: %+v", tt.text, ps)
		}
	}
}

// A near miss of a column still reads the attribute, with a notice that
// offers the column.
func TestCompileMisspelledFieldOffersTheColumn(t *testing.T) {
	c, ps := Compile("spans | p95(duraton) by service", Options{})
	if len(ps) != 0 || len(c.Notices) != 1 {
		t.Fatalf("%+v %+v", ps, c)
	}
	n := c.Notices[0]
	if !strings.HasSuffix(n.Message, "; did you mean duration?") || len(n.FixIts) != 1 || n.FixIts[0].Text != "duration" || n.FixIts[0].Span != *n.Span {
		t.Fatalf("notice = %+v", n)
	}
	if c.Query.Measures[0] != "p95(attributes['duraton'])" {
		t.Fatalf("measure = %v", c.Query.Measures)
	}
}

// A map path alone is free text, as for any word before no operator, with a
// notice saying how to filter on the field.
func TestCompileMapPathAloneSaysHowToFilter(t *testing.T) {
	for text, want := range map[string]string{
		"spans where attributes.foo": `"attributes.foo" is searched as text; for the field, write attributes.foo:<value>, or attributes.foo:* for any value`,
		"spans where attributes.":    `"attributes." is searched as text; for the field, write attributes.key:<value>, or attributes.key:* for any value`,
	} {
		c, ps := Compile(text, Options{})
		if len(ps) != 0 || len(c.Notices) != 1 || c.Notices[0].Message != want {
			t.Errorf("%q: %+v %+v", text, ps, c)
		}
	}
}

func TestCompleteAfterATermOffersConnectorsAndClausesFirst(t *testing.T) {
	schema := completionFixtureSchema()
	spans := schema.Signals["spans"]
	for i := range 80 {
		spans.Attributes = append(spans.Attributes, query.AttributeSchema{Key: "app.k" + string(rune('a'+i%26)) + string(rune('a'+i/26)), Scope: "attributes", Type: "string", Count: 100})
	}
	schema.Signals["spans"] = spans
	c := NewCompleter(completionSchemaFunc(func(context.Context, query.SchemaRequest) (*query.Schema, error) { return schema, nil }))
	waitCompletion(t, c, completionRequest("spans "), func(r CompleteResponse) bool { return r.Schema.Warm })
	if got := completionLabels(c.Complete(context.Background(), completionRequest("spans where service:cart "))); !slices.Equal(got[:5], []string{"and", "or", "|", "sort", "limit"}) {
		t.Errorf("after a term: %v", got)
	}
	if got := completionLabels(c.Complete(context.Background(), completionRequest("spans where service:cart o"))); len(got) == 0 || got[0] != "or" {
		t.Errorf("after a term, o: %v", got)
	}
	if got := completionLabels(c.Complete(context.Background(), completionRequest("spans where (service:cart "))); !slices.Equal(got[:2], []string{"and", "or"}) || slices.Contains(got, "sort") {
		t.Errorf("inside a group: %v", got)
	}
}

func TestCompleteValuesNameTheirForm(t *testing.T) {
	schema := completionFixtureSchema()
	spans := schema.Signals["spans"]
	spans.Attributes = append(spans.Attributes, query.AttributeSchema{Key: "http.status_code", Scope: "attributes", Type: "string", Types: []string{"number", "string"}, Count: 50,
		Values: []query.Option{{Value: "200", Count: 40}, {Value: "308", Count: 3}}})
	spans.Columns = []query.ColumnSchema{{Field: query.Field{Name: "http_route"}, Values: []query.Option{{Value: "/api/cart", Count: 9}}}}
	schema.Signals["spans"] = spans
	c := NewCompleter(completionSchemaFunc(func(context.Context, query.SchemaRequest) (*query.Schema, error) { return schema, nil }))
	waitCompletion(t, c, completionRequest("spans "), func(r CompleteResponse) bool { return r.Schema.Warm })
	r := c.Complete(context.Background(), completionRequest("spans where http.status_code:"))
	var got []string
	for _, item := range r.Items {
		got = append(got, item.Label+" "+item.Detail)
	}
	if want := []string{"200 matches numbers", `"200" matches text`, "308 matches numbers", `"308" matches text`, "* "}; !slices.Equal(got, want) {
		t.Errorf("mixed values = %q", got)
	}
	if got := completionLabels(c.Complete(context.Background(), completionRequest("spans where http_route:"))); !slices.Contains(got, `"/api/cart"`) && !slices.Contains(got, "/api/cart") {
		t.Errorf("column values = %v", got)
	}
	for _, item := range c.Complete(context.Background(), completionRequest("spans where ki")).Items {
		if item.Label == "kind" && item.Detail != "client, consumer, internal, producer, server, unspecified" {
			t.Errorf("kind detail = %q", item.Detail)
		}
	}
}
