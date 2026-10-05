package panel

import (
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/queryrows"
)

func TestCheckFilterAcceptsSafeExpressions(t *testing.T) {
	d, _ := newTestEngine(t)
	spans, _ := lookupSignal("spans")
	vars := map[string]Variable{"service": {Name: "service", Kind: "query"}, "routes": {Name: "routes", Kind: "query", Multi: true}}
	cases := []struct {
		expr     string
		params   []string
		inParams []string
		eq       string
	}{
		{"service = $service", []string{"service"}, nil, "service"},
		{"attributes['http.route'] = '/cart'", nil, nil, "attributes['http.route']"},
		{"attributes['http.status_code']::INTEGER >= 500", nil, nil, ""},
		{"http_route IN $routes", []string{"routes"}, []string{"routes"}, ""},
		{"kind IN ('SPAN_KIND_SERVER', 'SPAN_KIND_CONSUMER') AND NOT (status = 'STATUS_CODE_OK')", nil, nil, ""},
		{"lower(operation) LIKE 'get %' OR http_route ILIKE '%cart%'", nil, nil, ""},
		{"coalesce(http_route, '') = '/cart'", nil, nil, ""},
		{"contains($routes, http_route)", []string{"routes"}, []string{"routes"}, ""},
		{"parent_span_id IS NULL OR duration_ms BETWEEN 100 AND 2000", nil, nil, ""},
	}
	for _, tc := range cases {
		f, err := checkFilter(t.Context(), d, spans, vars, tc.expr)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		if strings.Join(f.Params, ",") != strings.Join(tc.params, ",") || f.EqField != tc.eq {
			t.Errorf("%s: params %v eq %q", tc.expr, f.Params, f.EqField)
		}
		for _, name := range tc.inParams {
			if !f.InParams[name] {
				t.Errorf("%s: %s not marked IN", tc.expr, name)
			}
		}
	}
}

func TestCheckFilterRejectsUnsafeExpressions(t *testing.T) {
	d, _ := newTestEngine(t)
	spans, _ := lookupSignal("spans")
	vars := map[string]Variable{"service": {Name: "service", Kind: "query"}, "routes": {Name: "routes", Kind: "query", Multi: true}}
	cases := map[string]string{
		"1=1) UNION SELECT * FROM users WHERE (1=1":           "single boolean expression",
		"service IN (SELECT name FROM users)":                 "not allowed",
		"read_text('/etc/passwd') <> ''":                      "read_text",
		"servce = 'checkout'":                                 "service",
		"service = $svc":                                      "$svc",
		"http_route = $routes":                                "IN $routes",
		"service = ?":                                         "variables",
		"attributes['a'] = 'x' AND attributes[service] = 'y'": "attribute",
		"service = 'a') GROUP BY (1":                          "single boolean expression",
		// C1: text that escapes its parentheses
		"1=1) GROUP BY ALL --":      "single boolean expression",
		"service = 'a') GROUP BY (": "single boolean expression",
		// I4: disallowed nodes nested inside allowed ones
		"lower(read_text('/etc/passwd')) = 'x'":            "read_text",
		"lower((SELECT 'a')) = 'a'":                        "not allowed",
		"(SELECT 1)::INTEGER = 1":                          "not allowed",
		"duration_ms BETWEEN 1 AND (SELECT 2)":             "not allowed",
		"kind IN ('a', getenv('x'))":                       "getenv",
		"service::BLOB = 'x'":                              "casting to",
		"service::INTERVAL = INTERVAL 1 DAY":               "casting to",
		"CASE WHEN service = 'a' THEN true ELSE false END": "not allowed",
		"lower(DISTINCT service) = 'x'":                    "DISTINCT",
		"lower(service ORDER BY service) = 'x'":            "ORDER BY",
		"lower(service) FILTER (WHERE true) = 'x'":         "FILTER",
		"service = $1":                                     "positional",
		"getenv('HOME') = 'x'":                             "getenv",
	}
	for expr, want := range cases {
		_, err := checkFilter(t.Context(), d, spans, vars, expr)
		if err == nil || !strings.Contains(err.Error(), want) {
			t.Errorf("%s: err = %v, want containing %q", expr, err, want)
		}
	}
}

func TestCheckFilterStoresDuckDBsCanonicalText(t *testing.T) {
	d, _ := newTestEngine(t)
	spans, _ := lookupSignal("spans")
	vars := map[string]Variable{"service": {Name: "service", Kind: "query"}}
	cases := []struct {
		expr   string
		want   []string // substrings of the canonical Expr
		params int
	}{
		{"operation = $$it's$$", []string{"'it''s'"}, 0},
		{"operation = $service$x$service$", []string{"'x'"}, 0},
		{"operation = $$'$$ OR service = $service OR operation = $$'$$", []string{"$service"}, 1},
		{"service = 'a' /* c */", []string{"'a'"}, 0},
		{"service = 'a') WINDOW w AS (", []string{"(service = 'a')"}, 0},
		{"1=1) OR (1=1", []string{"((1 = 1) OR (1 = 1))"}, 0},
	}
	for _, tc := range cases {
		f, err := checkFilter(t.Context(), d, spans, vars, tc.expr)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		for _, w := range tc.want {
			if !strings.Contains(f.Expr, w) {
				t.Errorf("%s: Expr %q lacks %q", tc.expr, f.Expr, w)
			}
		}
		if strings.Contains(f.Expr, "WINDOW") || strings.Contains(f.Expr, "GROUP") || strings.Contains(f.Expr, "--") || strings.Contains(f.Expr, "$$") || len(f.Params) != tc.params || f.Source != tc.expr && !strings.HasPrefix(tc.expr, f.Source) {
			t.Errorf("%s: %+v", tc.expr, f)
		}
		if tc.params == 1 {
			if _, _, args, err := bindParams(f.Expr, func(string) ([]any, bool, error) { return []any{"x"}, false, nil }); err != nil || len(args) != 1 {
				t.Errorf("%s: binding canonical text gave %v, %v", tc.expr, args, err)
			}
		}
		// Whatever the author wrote, the stored text is one parenthesized
		// expression, so composing it after a window predicate with AND
		// cannot let an OR escape.
		if !strings.HasPrefix(f.Expr, "(") || !strings.HasSuffix(f.Expr, ")") {
			t.Errorf("%s: Expr %q is not parenthesized", tc.expr, f.Expr)
		}
	}
}

// TestCheckedFiltersRunWithListBinds executes checked filters on the engine
// with list variables bound as strings, the way the executor binds them.
func TestCheckedFiltersRunWithListBinds(t *testing.T) {
	d, repo := newTestEngine(t)
	commit(t, repo, shopSpans(), nil)
	spans, _ := lookupSignal("spans")
	vars := map[string]Variable{
		"routes":    {Name: "routes", Kind: "query", Multi: true},
		"durations": {Name: "durations", Kind: "query", Multi: true},
		"threshold": {Name: "threshold", Kind: "query"},
		"lo":        {Name: "lo", Kind: "query"},
		"hi":        {Name: "hi", Kind: "query"},
		"route":     {Name: "route", Kind: "query"},
		"since":     {Name: "since", Kind: "query"},
		"n":         {Name: "n", Kind: "query"},
		"at":        {Name: "at", Kind: "query"},
		"times":     {Name: "times", Kind: "query", Multi: true},
		"a":         {Name: "a", Kind: "query"},
		"b":         {Name: "b", Kind: "query"},
	}
	ctx := queryrows.WithWindow(t.Context(), queryrows.Window{Start: fixtureStart, End: fixtureStart.Add(time.Hour)})
	cases := []struct {
		expr   string
		values map[string][]any
		want   int64
	}{
		{"attributes['http.route'] IN $routes", map[string][]any{"routes": {"/cart", "/quote"}}, 120},
		{"duration_ms IN $durations", map[string][]any{"durations": {"40", "20"}}, 120},
		{"http_route IN $routes", map[string][]any{"routes": {"/cart"}}, 60},
		{"contains($routes, http_route)", map[string][]any{"routes": {"/cart"}}, 60},
		{"attributes['http.route'] IN $routes", map[string][]any{"routes": {"/cart"}}, 60},
		{"duration_ms >= $threshold", map[string][]any{"threshold": {"500"}}, 30},
		{"duration_ms BETWEEN $lo AND $hi", map[string][]any{"lo": {"30"}, "hi": {"50"}}, 60},
		{"attributes['http.route'] = $route", map[string][]any{"route": {"/cart"}}, 60},
		{"lower(http_route) = $route", map[string][]any{"route": {"/quote"}}, 60},
		// Time columns: spans at or after 12:30 are minutes 30–59 of three
		// routes. A nanosecond past 12:30 drops minute 30, so the cast keeps
		// nanoseconds.
		{"start_time >= $since", map[string][]any{"since": {"2026-10-01T12:30:00Z"}}, 90},
		{"start_time >= $since", map[string][]any{"since": {"2026-10-01T12:30:00.000000001Z"}}, 87},
		{"start_time IN $times", map[string][]any{"times": {"2026-10-01T12:30:00Z", "2026-10-01T12:31:00Z"}}, 6},
		// A time parameter as the BETWEEN input, typed from its bounds: only
		// the 900 ms /cart span of minute 30 is still running at 12:30:00.05.
		{"$at BETWEEN start_time AND end_time", map[string][]any{"at": {"2026-10-01T12:30:00.05Z"}}, 1},
		// length() is numeric: /quote (6) and /api/cart (9) are longer than 5.
		{"length(http_route) > $n", map[string][]any{"n": {"5"}}, 120},
		// coalesce is numeric when an argument is, and types its parameters.
		{"coalesce(duration_ms, 0) > $threshold", map[string][]any{"threshold": {"500"}}, 30},
		{"duration_ms = coalesce($threshold, 0)", map[string][]any{"threshold": {"500"}}, 0},
		{"duration_ms = coalesce($threshold, 0)", map[string][]any{"threshold": {"40"}}, 60},
		// A parameter as the BETWEEN input, typed from its numeric bound:
		// $lo BETWEEN duration_ms AND $hi is duration_ms <= $lo.
		{"$lo BETWEEN duration_ms AND $hi", map[string][]any{"lo": {"0"}, "hi": {"10000"}}, 0},
		{"$lo BETWEEN duration_ms AND $hi", map[string][]any{"lo": {"50"}, "hi": {"10000"}}, 120},
		// A numeric constant types the parameter it is compared with; as
		// text, '10' > '5' is false.
		{"$n > 5", map[string][]any{"n": {"10"}}, 180},
		// Parameter against parameter stays text; DOUBLE would not cast "x".
		{"$a = $b", map[string][]any{"a": {"x"}, "b": {"x"}}, 180},
	}
	for _, tc := range cases {
		f, err := checkFilter(t.Context(), d, spans, vars, tc.expr)
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		bound, _, args, err := bindParams(f.Expr, func(name string) ([]any, bool, error) { return tc.values[name], vars[name].Multi, nil })
		if err != nil {
			t.Errorf("%s: %v", tc.expr, err)
			continue
		}
		rows, err := d.QueryContext(ctx, "SELECT count(*) FROM spans WHERE "+bound, args...)
		if err != nil {
			t.Errorf("%s: %q: %v", tc.expr, bound, err)
			continue
		}
		var got int64
		if rows.Next() {
			_ = rows.Scan(&got)
		}
		_ = rows.Close()
		if got != tc.want {
			t.Errorf("%s: %q counted %d, want %d", tc.expr, bound, got, tc.want)
		}
	}
}
