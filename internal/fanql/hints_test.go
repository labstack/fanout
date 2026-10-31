package fanql

import "testing"

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
