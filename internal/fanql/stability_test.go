package fanql

import (
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/panel"
)

func TestCompileNestedMeasureNegation(t *testing.T) {
	for _, text := range []string{
		"logs | -(-count()) as n",
		"logs | count_if(`body = 'x' -- )\n`) as n",
	} {
		t.Run(text, func(t *testing.T) {
			c, ps := Compile(text, Options{})
			if len(ps) != 0 || c == nil {
				t.Fatalf("compile %q: %+v", text, ps)
			}
			if _, err := panel.ParseMeasure(c.Query.From, c.Query.Measures[0]); err != nil {
				t.Fatalf("lowered measure %q: %v", c.Query.Measures[0], err)
			}
			again, ps := Compile(c.Canonical, Options{})
			if len(ps) != 0 || again == nil || !reflect.DeepEqual(c.Query, again.Query) {
				t.Fatalf("canonical %q changed query: %+v %+v", c.Canonical, again, ps)
			}
		})
	}
}

func TestPrintExactIntegerThresholds(t *testing.T) {
	engine, _ := newTestEngine(t)
	for _, number := range []string{"9007199254740991", "9007199254740992", "9007199254740993", "-9007199254740991", "-9007199254740993", "9223372036854775807", "-9223372036854775808"} {
		for _, path := range []string{
			"spans where duration_ms>%s",
			"logs where severity_number>%s",
			"logs where attributes['n']>%s",
			"logs where resource['n']:%s",
			"logs where severity_number in (%s, 0)",
			"spans where duration_ms in (%s, 0)",
			"logs where attributes['n'] between %s and 9223372036854775807",
			"spans where duration_ms between %s and 9223372036854775807",
			"logs where length(body)>%s",
		} {
			text := fmt.Sprintf(path, number)
			t.Run(text, func(t *testing.T) {
				c, ps := Compile(text, Options{})
				if len(ps) != 0 || c == nil {
					t.Fatalf("compile: %+v", ps)
				}
				if !strings.Contains(strings.Join(c.Query.Where, " "), number) {
					t.Fatalf("lost integer %s: %+v", number, c.Query)
				}
				again, ps := Compile(c.Canonical, Options{})
				if len(ps) != 0 || again == nil || !reflect.DeepEqual(c.Query, again.Query) {
					t.Fatalf("canonical %q changed query: %+v %+v", c.Canonical, again, ps)
				}
				f, ps, err := Format(t.Context(), engine, c.Query, FormatOptions{})
				if err != nil || len(ps) != 0 || f == nil {
					t.Fatalf("format: %+v %v", ps, err)
				}
				again, ps = Compile(f.Text, Options{})
				if len(ps) != 0 || again == nil || !reflect.DeepEqual(c.Query, again.Query) {
					t.Fatalf("formatted %q changed query: %+v %+v", f.Text, again, ps)
				}
			})
		}
	}
}

func TestFormatCountIfSQLComments(t *testing.T) {
	engine, _ := newTestEngine(t)
	for _, text := range []string{
		"logs | count_if(`body = 'x' /* ) */`) as n",
		"logs | count_if(`body = 'x' -- ) ( , |\n`) as n",
		"logs | count_if(`body = 'x' /* ( /* ) */ ( */`) as n",
		"logs | count_if(`body = 'it''s ) /* --' /* ) ' */`) as n",
		"logs | count_if(`body = 'x' -- ' /* )\r\n`) as n",
		"logs | count_if(`body='x'/*)*/`) as n",
	} {
		t.Run(text, func(t *testing.T) {
			c, ps := Compile(text, Options{})
			if len(ps) != 0 || c == nil {
				t.Fatalf("compile: %+v", ps)
			}
			f, ps, err := Format(t.Context(), engine, c.Query, FormatOptions{})
			if err != nil || len(ps) != 0 || f == nil {
				t.Fatalf("format: %+v %v", ps, err)
			}
			again, ps := Compile(f.Text, Options{})
			if len(ps) != 0 || again == nil || !reflect.DeepEqual(c.Query, again.Query) {
				t.Fatalf("formatted %q changed query: %+v %+v", f.Text, again, ps)
			}
		})
	}
}
