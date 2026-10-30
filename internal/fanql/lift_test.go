package fanql

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/panel"
)

type countingParser struct {
	panel.Parser
	calls   int
	fail    bool
	failErr error
}

func (p *countingParser) ParseSQL(ctx context.Context, text string) (map[string]any, error) {
	p.calls++
	if p.failErr != nil {
		return nil, p.failErr
	}
	if p.fail {
		return nil, context.DeadlineExceeded
	}
	return p.Parser.ParseSQL(ctx, text)
}

func TestFormatOwnLoweringNeedsNoEngine(t *testing.T) {
	for _, text := range glanceExamples {
		t.Run(text, func(t *testing.T) {
			c, problems := Compile(text, Options{})
			if len(problems) != 0 {
				t.Fatalf("compile: %+v", problems)
			}
			p := &countingParser{fail: true}
			f, problems, err := Format(t.Context(), p, c.Query, Options{})
			if err != nil || len(problems) != 0 {
				t.Fatalf("format: %+v %v", problems, err)
			}
			if f.Exactness != "exact" || !f.Editable || f.Text != c.Canonical || p.calls != 0 {
				t.Fatalf("format: %+v, calls %d; want %q", f, p.calls, c.Canonical)
			}
		})
	}
}

func TestFormatCountIfQuotedDelimiters(t *testing.T) {
	for _, text := range []string{
		`spans | count_if(service:"(")`,
		`spans | count_if(service:")")`,
		`spans | count_if(service:",")`,
		`spans | count_if(service:"|")`,
		`spans | count_if(attributes['(']:x)`,
		`spans | count_if(attributes[')']:x)`,
		`spans | count_if(attributes[',']:x)`,
		`spans | count_if(attributes['|']:x)`,
		`spans | count_if(attributes['( ) , |']:"( ) , |") / count() as ratio`,
	} {
		t.Run(text, func(t *testing.T) {
			c, ps := Compile(text, Options{})
			if len(ps) != 0 {
				t.Fatal(ps)
			}
			p := &countingParser{fail: true}
			f, ps, err := Format(t.Context(), p, c.Query, Options{})
			if err != nil || len(ps) != 0 {
				t.Fatalf("format: %v %v", ps, err)
			}
			if f.Exactness != "exact" || !f.Editable || f.Text != c.Canonical || p.calls != 0 {
				t.Fatalf("format: %+v, calls %d; want %q", f, p.calls, c.Canonical)
			}
			again, ps := Compile(f.Text, Options{})
			if len(ps) != 0 || !reflect.DeepEqual(again.Query, c.Query) {
				t.Fatalf("recompile: %+v %v", again, ps)
			}
		})
	}
}

func TestLiftStoredSQLShapes(t *testing.T) {
	d, _ := newTestEngine(t)
	for _, tt := range []struct{ signal, sql, text, tier string }{
		{"spans", `service = 'api'`, `service:api`, "exact"},
		{"spans", `service='api'`, `service:api`, "normalized"},
		{"spans", `status = 'STATUS_CODE_ERROR'`, `status:error`, "exact"},
		{"spans", `kind = 'SPAN_KIND_SERVER'`, `kind:server`, "exact"},
		{"spans", `duration_ms > 500`, `duration>500ms`, "exact"},
		{"spans", `duration_ms > 1500`, `duration>1.5s`, "exact"},
		{"spans", `attributes['http.target'] != '/healthz'`, `http.target!=/healthz`, "exact"},
		{"spans", `(attributes['k'] IS NULL OR attributes['k'] != 'v')`, `-attributes['k']:v`, "exact"},
		{"spans", `NOT (attributes['k'] = 'v')`, "`NOT (attributes['k'] = 'v')`", "exact"},
		{"logs", `NOT (body ~~ 'x%')`, "`NOT (body ~~ 'x%')`", "exact"},
		{"logs", `body !~~ 'x%'`, `body not like "x%"`, "exact"},
		{"spans", `service NOT IN ('a', 'b')`, `service not in (a, b)`, "exact"},
		{"spans", `NOT coalesce((attributes['k'] = 'v'), FALSE)`, "`NOT coalesce((attributes['k'] = 'v'), FALSE)`", "exact"},
		{"spans", `NOT coalesce((TRY_CAST(http_status_code AS INTEGER) BETWEEN 400 AND 599), FALSE)`, `-http_status_code between 400 and 599`, "exact"},
		{"spans", `(service IS NULL OR service IN ('a', 'b'))`, `-service not in (a, b)`, "exact"},
		{"spans", `TRY_CAST(http_status_code AS INTEGER) >= 500`, `http_status_code>=500`, "exact"},
		{"logs", `severity IN ('WARN', 'ERROR', 'FATAL')`, `severity>=warn`, "exact"},
		{"logs", `severity IN ('INFO', 'ERROR')`, `severity in (info, error)`, "exact"},
		{"logs", `severity IN ('INFO', 'WARN', 'ERROR')`, `severity between info and error`, "exact"},
		{"logs", `contains(lower(body), lower('timeout'))`, `timeout`, "exact"},
		{"spans", `contains(lower(CAST(attributes['k'] AS VARCHAR)), lower('v'))`, `attributes['k'] ~ v`, "exact"},
		{"spans", `start_time > '2026-10-29T10:00:00Z'::TIMESTAMPTZ_NS`, `start_time>"2026-10-29T10:00:00Z"`, "exact"},
		{"spans", `service IN $svcs`, `service in $svcs`, "exact"},
		{"spans", `service = $service`, `service:$service`, "exact"},
		{"spans", `lower(operation) = 'v'`, "`lower(operation) = 'v'`", "exact"},
		{"spans", `starts_with(http_route, '/api')`, `starts_with(http_route, "/api")`, "exact"},
		{"logs", `length(body) > 1000`, `length(body)>1000`, "exact"},
		{"spans", `service = $service AND status = 'STATUS_CODE_ERROR'`, `(service:$service status:error)`, "normalized"},
	} {
		t.Run(tt.sql, func(t *testing.T) {
			q := panel.Query{From: tt.signal, Where: []string{tt.sql}}
			f, problems, err := Format(t.Context(), d, q, Options{})
			if err != nil || len(problems) != 0 {
				t.Fatalf("format: %+v %v", problems, err)
			}
			if f.Text != tt.signal+" where "+tt.text || f.Exactness != Exactness(tt.tier) {
				t.Fatalf("got %+v, want %q %s", f, tt.text, tt.tier)
			}
			c, ps := Compile(f.Text, Options{})
			if len(ps) != 0 || len(c.Query.Where) != 1 {
				t.Fatalf("recompile: %+v %+v", c, ps)
			}
			if f.Exactness == "exact" {
				if !reflect.DeepEqual(c.Query, q) {
					t.Fatalf("not exact: %#v != %#v", c.Query, q)
				}
			} else {
				a, _ := d.ParseSQL(t.Context(), "SELECT 1 FROM "+tt.signal+" WHERE ("+tt.sql+")")
				b, _ := d.ParseSQL(t.Context(), "SELECT 1 FROM "+tt.signal+" WHERE ("+c.Query.Where[0]+")")
				x, _ := d.RenderSQL(t.Context(), a)
				y, _ := d.RenderSQL(t.Context(), b)
				if x != y {
					t.Fatalf("not normalized: %q != %q", x, y)
				}
				if tt.sql == `service='api'` && (len(f.Notices) == 0 || f.Notices[0].Message != `where[0] is shown as service:api; stored text is "service='api'"`) {
					t.Fatalf("notice: %+v", f.Notices)
				}
			}
		})
	}
	q := panel.Query{From: "spans", Where: []string{`attributes['a'] = 1 OR attributes['b'] = 2`, `service = 'api'`}}
	f, ps, err := Format(t.Context(), d, q, Options{})
	if err != nil || len(ps) > 0 || f.Text != `spans where (attributes['a']:1 or attributes['b']:2) service:api` {
		t.Fatalf("entry grouping: %+v %+v %v", f, ps, err)
	}
}

func TestLiftMeasures(t *testing.T) {
	d, _ := newTestEngine(t)
	for _, tt := range []struct{ stored, want, tier string }{
		{`p95( duration_ms )`, `p95(duration)`, "normalized"},
		{`count() as n`, `count() as n`, "exact"},
		{`count_distinct(attributes['user.id'])`, `count_distinct(user.id)`, "exact"},
		{`quantile(duration_ms, 0.999) as q`, `quantile(duration, 0.999) as q`, "exact"},
	} {
		f, ps, err := Format(t.Context(), d, panel.Query{From: "spans", Measures: []string{tt.stored}}, Options{})
		if err != nil || len(ps) > 0 || f.Text != "spans | "+tt.want || f.Exactness != Exactness(tt.tier) {
			t.Fatalf("%s: %+v %+v %v", tt.stored, f, ps, err)
		}
	}
	q := panel.Query{From: "spans", Measures: []string{"count()"}, Histogram: &panel.Histogram{Field: "duration_ms", Buckets: "log2"}}
	f, ps, err := Format(t.Context(), d, q, Options{})
	if err != nil || len(ps) > 0 || f.Text != "spans | histogram(duration)" {
		t.Fatalf("histogram: %+v %+v %v", f, ps, err)
	}
	q.Measures = []string{"p95(duration_ms)"}
	f, ps, err = Format(t.Context(), d, q, Options{})
	if err != nil || len(ps) == 0 || f != nil {
		t.Fatalf("invalid histogram: %+v %+v %v", f, ps, err)
	}
	for _, sort := range []string{"errors", "duration_ms", "+start"} {
		f, ps, err = Format(t.Context(), d, panel.Query{From: "spans", Sort: sort}, Options{Viz: "traces"})
		want := sort
		if sort == "duration_ms" {
			want = "duration"
		}
		if err != nil || len(ps) > 0 || f.Text != "spans sort "+want {
			t.Fatalf("sort: %+v %+v %v", f, ps, err)
		}
		if sort == "errors" && (len(f.Notices) == 0 || !strings.Contains(f.Notices[0].Message, "sort errors also filters")) {
			t.Fatalf("notice: %+v", f.Notices)
		}
	}
}

func TestFormatEditableFalseIsCompleteAndReasoned(t *testing.T) {
	d, _ := newTestEngine(t)
	q := panel.Query{From: "logs"}
	for i := range 16 {
		q.Where = append(q.Where, "body = '"+strings.Repeat("x", 480)+fmt.Sprint(i)+"'")
	}
	f, ps, err := Format(t.Context(), d, q, Options{})
	if err != nil || len(ps) > 0 {
		t.Fatalf("format: %+v %v", ps, err)
	}
	if f.Editable || f.Exactness != "exact" || f.Reason != "formatted text is 7,808 characters; the editor accepts 4,000" {
		t.Fatalf("oversized: %+v", f)
	}
	for i := range 16 {
		if !strings.Contains(f.Text, strings.Repeat("x", 480)+fmt.Sprint(i)) {
			t.Fatalf("lost literal %d", i)
		}
	}
	rawQuery := panel.Query{From: "logs"}
	for i := range 16 {
		rawQuery.Where = append(rawQuery.Where, "coalesce(body, '"+strings.Repeat("𝔘", 110)+strings.Repeat("`", 30)+fmt.Sprint(i)+"') = ''")
	}
	rawFormat, ps, err := Format(t.Context(), d, rawQuery, Options{})
	if err != nil || len(ps) > 0 {
		t.Fatalf("raw format: %+v %v", ps, err)
	}
	if rawFormat.Editable || rawFormat.Exactness != "exact" || rawFormat.Reason != "formatted text is 8,448 bytes; the editor accepts 8,192" {
		t.Fatalf("raw byte bound: %+v", rawFormat)
	}
	for _, entry := range rawQuery.Where {
		if !strings.Contains(rawFormat.Text, strings.ReplaceAll(entry, "`", "``")) {
			t.Fatal("lost raw literal")
		}
	}
	// Bounds are independent even when no stored, shape-valid query can generate
	// a single 4K raw entry (the filter contract caps each entry at 500 bytes).
	if reason := editableReason("logs where `" + strings.Repeat("x", 4279) + strings.Repeat("``", 10) + "`"); reason != "formatted text is 4,312 characters; the editor accepts 4,000" {
		t.Fatalf("characters: %q", reason)
	}
	if reason := editableReason("logs where " + strings.Repeat("a ", 1100)); reason != "formatted text is 1,102 tokens; the editor accepts 1,024" {
		t.Fatalf("tokens: %q", reason)
	}
	if reason := editableReason("logs where \"" + strings.Repeat("𝔘", 2100) + "\""); reason != "formatted text is 8,413 bytes; the editor accepts 8,192" {
		t.Fatalf("bytes: %q", reason)
	}
}

func TestFormatOperationalErrorIsReturnedNotAProblem(t *testing.T) {
	for _, wantErr := range []error{context.DeadlineExceeded, errors.New("disk failure")} {
		f, ps, err := Format(t.Context(), &countingParser{failErr: wantErr}, panel.Query{From: "logs", Where: []string{`coalesce(body, '') = ''`}}, Options{})
		if f != nil || len(ps) > 0 || !errors.Is(err, wantErr) {
			t.Fatalf("got %+v %+v %v", f, ps, err)
		}
	}
}

type failingRenderParser struct {
	panel.Parser
	err error
}

func (p failingRenderParser) RenderSQL(context.Context, map[string]any) (string, error) {
	return "", p.err
}

func TestFormatRenderOperationalErrorIsReturnedNotAProblem(t *testing.T) {
	d, _ := newTestEngine(t)
	wantErr := errors.New("engine serialization failed")
	f, ps, err := Format(t.Context(), failingRenderParser{Parser: d, err: wantErr}, panel.Query{From: "spans", Where: []string{`service='api'`}}, Options{})
	if f != nil || len(ps) > 0 || !errors.Is(err, wantErr) {
		t.Fatalf("got %+v %+v %v", f, ps, err)
	}
}

func TestFormatRejectsQueryInvalidUnderEveryViz(t *testing.T) {
	p := &countingParser{fail: true}
	f, ps, err := Format(t.Context(), p, panel.Query{From: "spans", Measures: []string{"count()"}, By: []string{"a", "b", "c", "d"}}, Options{})
	if f != nil || len(ps) == 0 || err != nil || p.calls != 0 {
		t.Fatalf("got %+v %+v %v calls %d", f, ps, err, p.calls)
	}
}

func TestFormatOwnLoweringFilterFixturesNeedNoEngine(t *testing.T) {
	for _, fixture := range lowerFixtures {
		if strings.ContainsRune(fixture.text, '`') {
			continue
		} // Arbitrary raw SQL needs the engine's authority.
		text := fixture.text
		if strings.HasPrefix(text, "metrics ") {
			text += " | count()"
		} // Metrics has no fixed row visualization.
		t.Run(text, func(t *testing.T) {
			c, ps := Compile(text, Options{})
			if len(ps) > 0 {
				t.Fatal(ps)
			}
			p := &countingParser{fail: true}
			f, ps, err := Format(t.Context(), p, c.Query, Options{})
			if err != nil || len(ps) > 0 {
				t.Fatalf("format %+v %v; canonical %q", ps, err, c.Canonical)
			}
			if f.Exactness != "exact" || f.Text != c.Canonical || p.calls != 0 {
				t.Fatalf("got %+v calls %d, want %q", f, p.calls, c.Canonical)
			}
			c2, ps := Compile(f.Text, Options{})
			if len(ps) > 0 || !reflect.DeepEqual(c.Query, c2.Query) || c2.Canonical != c.Canonical {
				t.Fatalf("fixed point: %+v %+v", c2, ps)
			}
		})
	}
}

func TestFormatReportsIgnoredSortAndLimit(t *testing.T) {
	for _, tt := range []struct {
		viz string
		q   panel.Query
	}{
		{"timeseries", panel.Query{From: "spans", Measures: []string{"count()"}, Bucket: "auto", Sort: "count", Limit: 10}},
		{"stat", panel.Query{From: "spans", Measures: []string{"count()"}, Limit: 10}},
		{"gauge", panel.Query{From: "spans", Measures: []string{"count()"}, Limit: 10}},
	} {
		f, ps, err := Format(t.Context(), &countingParser{fail: true}, tt.q, Options{Viz: tt.viz})
		if err != nil || len(ps) > 0 || len(f.Notices) != 1 || f.Notices[0].Message != "sort does not apply to bucketed series; series are chosen by top (options)" {
			t.Fatalf("%s: %+v %+v %v", tt.viz, f, ps, err)
		}
	}
}

func TestFormatOwnSeverityBandsAreFixedPoints(t *testing.T) {
	for _, text := range []string{
		`logs where severity not between info and error`,
		`logs where -severity>=warn`,
		`logs where severity in (info, warn, error)`,
		`logs where -severity not between info and error`,
		`logs where severity>error`,
		`logs where severity<debug`,
		`logs where -severity not in (warn, error, fatal)`,
		`logs where --severity is null`,
	} {
		t.Run(text, func(t *testing.T) {
			c, ps := Compile(text, Options{})
			if len(ps) > 0 {
				t.Fatal(ps)
			}
			p := &countingParser{fail: true}
			f, ps, err := Format(t.Context(), p, c.Query, Options{})
			if err != nil || len(ps) > 0 || f.Exactness != "exact" || f.Text != c.Canonical || p.calls != 0 {
				t.Fatalf("format %+v %+v %v calls %d, want %q", f, ps, err, p.calls, c.Canonical)
			}
			again, ps := Compile(f.Text, Options{})
			if len(ps) > 0 || !reflect.DeepEqual(again.Query, c.Query) {
				t.Fatalf("fixed point: %+v %+v", again, ps)
			}
		})
	}
}

func TestFormatOwnLiteralSpellingsAreFixedPoints(t *testing.T) {
	for _, text := range []string{
		`spans where status:"STATUS_CODE_ERROR"`,
		`spans where kind:"SPAN_KIND_SERVER"`,
		`spans where status:"error"`,
		`logs where severity:"ERROR"`,
		`logs where severity:"error"`,
		`metrics where type:COUNTER | count()`,
		`metrics where type:"counter" | count()`,
		`spans where http_status_code:2e3`,
		`logs where service:"TRUE"`,
		`spans | count(*)`,
		`logs where severity:14`,
		`logs where severity:true`,
		`logs where severity:"TRUE"`,
		`logs where severity:"1H"`,
		`logs where severity:where`,
		`spans where status:500`,
		`metrics where type:false | count()`,
	} {
		t.Run(text, func(t *testing.T) {
			c, ps := Compile(text, Options{})
			if len(ps) > 0 {
				t.Fatal(ps)
			}
			p := &countingParser{fail: true}
			f, ps, err := Format(t.Context(), p, c.Query, Options{})
			if err != nil || len(ps) > 0 || f.Exactness != "exact" || f.Text != c.Canonical || p.calls != 0 {
				t.Fatalf("format %+v %+v %v calls %d, want %q", f, ps, err, p.calls, c.Canonical)
			}
			again, ps := Compile(f.Text, Options{})
			if len(ps) > 0 || !reflect.DeepEqual(again.Query, c.Query) {
				t.Fatalf("fixed point: %+v %+v", again, ps)
			}
		})
	}
}
