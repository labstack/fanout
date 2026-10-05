package panel

import "testing"

func TestParseMeasures(t *testing.T) {
	spans, _ := lookupSignal("spans")
	var problems Problems
	got := parseMeasures(spans, []string{"p50(duration_ms)", "p95(duration_ms)", "count()", "rate() as rps", "error_rate()", "quantile(duration_ms, 0.999)", "avg(attributes['db.rows'])"}, "m", &problems)
	if len(problems) > 0 {
		t.Fatal(problems)
	}
	want := []struct{ alias, fn, unit string }{
		{"p50", "p50", "ms"}, {"p95", "p95", "ms"}, {"count", "count", "count"}, {"rps", "rate", "per_second"},
		{"error_rate", "error_rate", "percent"}, {"quantile", "quantile", "ms"}, {"avg", "avg", "none"},
	}
	for i, w := range want {
		if got[i].Alias != w.alias || got[i].Func != w.fn || got[i].Unit != w.unit {
			t.Fatalf("measure %d = %+v, want %+v", i, got[i], w)
		}
	}
	if got[5].Q != 0.999 {
		t.Fatalf("quantile q = %v", got[5].Q)
	}
}

func TestMeasureAliasCollisions(t *testing.T) {
	metrics, _ := lookupSignal("metrics")
	var problems Problems
	got := parseMeasures(metrics, []string{"avg(value)", "avg(hist_sum)"}, "m", &problems)
	if len(problems) > 0 || got[0].Alias != "avg_value" || got[1].Alias != "avg_hist_sum" {
		t.Fatalf("aliases = %q %q, problems %v", got[0].Alias, got[1].Alias, problems)
	}
}

func TestMeasureErrors(t *testing.T) {
	spans, _ := lookupSignal("spans")
	logs, _ := lookupSignal("logs")
	cases := []struct {
		sig  *signal
		text string
	}{
		{spans, "median(duration_ms)"},
		{spans, "p95()"},
		{spans, "p95(service)"},
		{spans, "count(duration_ms)"},
		{spans, "quantile(duration_ms, 2)"},
		{spans, "last(duration_ms)"},
		{logs, "p95(body)"},
		{spans, "p95(duration_ms) as Bad-Alias"},
	}
	for _, tc := range cases {
		var problems Problems
		parseMeasures(tc.sig, []string{tc.text}, "m", &problems)
		if len(problems) == 0 {
			t.Errorf("%s accepted", tc.text)
		}
	}
}

func TestFieldRefs(t *testing.T) {
	spans, _ := lookupSignal("spans")
	ref, err := spans.field("attributes['http.route']")
	if err != nil || ref.Column != "attributes" || ref.Key != "http.route" {
		t.Fatalf("ref = %+v err %v", ref, err)
	}
	if ref.stringSQL() != "TRY_CAST(attributes['http.route'] AS VARCHAR)" {
		t.Fatal(ref.stringSQL())
	}
	col, _ := spans.field("duration_ms")
	if col.numberSQL() != `"duration_ms"` || col.Unit != "ms" {
		t.Fatalf("column = %+v", col)
	}
	if _, err := spans.field("attributes"); err == nil {
		t.Fatal("bare map column accepted")
	}
	if _, err := spans.field("attributes['a'] OR 1=1"); err == nil {
		t.Fatal("injected attribute accepted")
	}
}
