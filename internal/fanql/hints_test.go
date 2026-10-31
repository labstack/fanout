package fanql

import (
	"slices"
	"strings"
	"testing"
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
