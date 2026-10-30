package fanql

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/panel"
)

type completionSchemaFunc func(context.Context, panel.SchemaRequest) (*panel.Schema, error)

// expectAt replaces a whole lexical token even when the cursor is in its middle.
// Punctuation is consumed by the grammar: a cursor after ':' expects a value.
func expectAt(text string, cursor int) (lexToken, []expect) {
	token, p := completionPrefix(text, cursor, "")
	return token, p.expected
}

func TestCompleteBoundsDistinctNamespaceFetchAdmission(t *testing.T) {
	gate := make(chan struct{})
	source := completionSchemaFunc(func(context.Context, panel.SchemaRequest) (*panel.Schema, error) {
		<-gate
		return completionFixtureSchema(), nil
	})
	completers := []*Completer{NewCompleter(source), NewCompleter(source)}
	defer func() {
		var pending []chan struct{}
		for _, c := range completers {
			c.mu.Lock()
			for _, done := range c.inflight {
				pending = append(pending, done)
			}
			c.mu.Unlock()
		}
		close(gate)
		for _, done := range pending {
			<-done
		}
	}()
	for i := 0; i < 256; i++ {
		completers[i%2].Complete(context.Background(), CompleteRequest{
			Text: "logs ", Cursor: 5, Window: "1h", Namespace: fmt.Sprintf("namespace-%d", i),
		})
		pending := 0
		for _, c := range completers {
			c.mu.Lock()
			pending += len(c.inflight)
			c.mu.Unlock()
		}
		if pending > 4 {
			t.Fatalf("%d distinct namespaces admitted %d fetches; cap is 4", i+1, pending)
		}
	}
}

func (f completionSchemaFunc) Schema(ctx context.Context, req panel.SchemaRequest) (*panel.Schema, error) {
	return f(ctx, req)
}

func completionFixtureSchema() *panel.Schema {
	return &panel.Schema{Signals: map[string]panel.SignalSchema{
		"logs": {Columns: []panel.ColumnSchema{
			{Field: panel.Field{Name: "severity"}, Values: []panel.Option{{Value: "ERROR"}, {Value: "INFO"}, {Value: "WARN"}}},
			{Field: panel.Field{Name: "service"}, Values: []panel.Option{{Value: "api"}, {Value: "checkout"}}},
		}},
		"spans": {Attributes: []panel.AttributeSchema{
			{Key: "http.target", Scope: "attributes", Type: "string", Count: 900},
			{Key: "http.method", Scope: "attributes", Type: "string", Count: 500},
			{Key: "user.score", Scope: "attributes", Type: "number", Count: 20},
			{Key: "k8s.pod.name", Scope: "resource", Type: "string", Count: 10},
			{Key: "enabled", Scope: "attributes", Type: "boolean", Count: 5},
		}},
		"metrics": {Metrics: []panel.MetricSchema{{Name: "http.server.duration", Count: 100}}},
	}}
}

func completionRequest(marked string) CompleteRequest {
	at := strings.Index(marked, "¦")
	if at < 0 {
		return CompleteRequest{Text: marked, Cursor: len([]rune(marked)), Window: "1h"}
	}
	return CompleteRequest{Text: strings.Replace(marked, "¦", "", 1), Cursor: len([]rune(marked[:at])), Window: "1h"}
}

func completionLabels(r CompleteResponse) []string {
	out := make([]string, len(r.Items))
	for i, item := range r.Items {
		out[i] = item.Label
	}
	return out
}

func waitCompletion(t *testing.T, c *Completer, req CompleteRequest, ready func(CompleteResponse) bool) CompleteResponse {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		r := c.Complete(context.Background(), req)
		if ready(r) {
			return r
		}
		time.Sleep(time.Millisecond)
	}
	t.Fatal("background schema fetch did not finish")
	return CompleteResponse{}
}

func warmCompleter(t *testing.T) *Completer {
	t.Helper()
	c := NewCompleter(completionSchemaFunc(func(context.Context, panel.SchemaRequest) (*panel.Schema, error) {
		return completionFixtureSchema(), nil
	}))
	waitCompletion(t, c, completionRequest("spans "), func(r CompleteResponse) bool { return r.Schema.Warm })
	return c
}

func TestExpectAtRecordsClasses(t *testing.T) {
	tests := []struct {
		text            string
		classes         []string
		field, function string
		arg             int
	}{
		{"", []string{"source"}, "", "", 0},
		{"spans ", []string{"clause", "field", "free_text"}, "", "", 0},
		{"spans where service", []string{"field", "free_text"}, "", "", 0},
		{"spans where service:", []string{"value"}, "service", "", 0},
		{"spans | ", []string{"measure"}, "", "", 0},
		{"spans | quantile(duration, ", []string{"marg"}, "", "quantile", 1},
		{"spans | count() every ", []string{"bucket"}, "", "", 0},
		{"spans | count() by service sort ", []string{"sort_key"}, "", "", 0},
		{"spans | count() limit ", []string{"integer"}, "", "", 0},
		{"spans where attributes['ht", []string{"bracket_key"}, "", "", 0},
	}
	for _, tt := range tests {
		t.Run(tt.text, func(t *testing.T) {
			token, got := expectAt(tt.text, len([]rune(tt.text)))
			var classes []string
			for _, e := range got {
				classes = append(classes, e.Class)
			}
			for _, class := range tt.classes {
				if !slices.Contains(classes, class) {
					t.Fatalf("classes %v, want %s", classes, class)
				}
			}
			if tt.field != "" && (got[0].Field == nil || got[0].Field.Key != tt.field) {
				t.Fatalf("field = %+v", got)
			}
			if tt.function != "" && (got[0].Func != tt.function || got[0].Arg != tt.arg) {
				t.Fatalf("argument = %+v", got)
			}
			if strings.HasSuffix(tt.text, "service") && token.Span != (span{12, 19}) {
				t.Fatalf("replace token = %+v", token)
			}
			if strings.Contains(tt.text, "['ht") && (token.Kind != tokenBracketKey || got[0].Scope != "attributes") {
				t.Fatalf("bracket = %+v %+v", token, got)
			}
		})
	}
}

func TestCompleteFixtures(t *testing.T) {
	c := warmCompleter(t)
	tests := []struct {
		text, viz                 string
		exact, includes, excludes []string
		first, insert, detail     string
		replace                   *panel.TextSpan
	}{
		{text: "logs where sev¦erity:error", first: "severity", replace: &panel.TextSpan{Start: 11, End: 19}},
		{text: "logs where severity:¦", includes: []string{"trace", "debug", "info", "warn", "error", "fatal", "*"}, excludes: []string{"ERROR", "INFO", "WARN"}},
		{text: "logs where severity>=¦", exact: []string{"trace", "debug", "info", "warn", "error", "fatal"}},
		{text: "spans where http.¦", exact: []string{"http.target", "http.method"}, insert: "http.target"},
		{text: "spans where attributes['htt¦", first: "http.target", insert: "http.target']", replace: &panel.TextSpan{Start: 24, End: 27}},
		{text: "spans where resource[\"k8s.¦", first: "k8s.pod.name", insert: "k8s.pod.name\"]"},
		{text: "logs where service in (api, ¦", exact: []string{"checkout", "api"}},
		{text: "logs where ser¦", includes: []string{"service"}, detail: "free text searches body"},
		{text: "spans | p9¦", exact: []string{"p90", "p95", "p99"}},
		{text: "metrics | ¦", includes: []string{"last"}, excludes: []string{"error_rate"}},
		{text: "spans | ¦", includes: []string{"histogram", "count_if"}, excludes: []string{"last"}},
		{text: "spans | quantile(duration, ¦", exact: []string{"0.5", "0.9", "0.95", "0.99"}},
		{text: "spans | count() by service, ¦", viz: "table", includes: []string{"operation", "http.target", "user.score"}, excludes: []string{"duration", "start_time"}},
		{text: "spans | count() by service, ¦", viz: "timeseries", detail: "timeseries groups by at most 1 field"},
		{text: "spans | count() every ¦", viz: "bar", detail: "bar panels have no bucket"},
		{text: "spans | count() every ¦", exact: []string{"auto", "10s", "30s", "1m", "5m", "10m", "15m", "30m", "1h", "3h", "6h", "12h", "1d"}},
		{text: "spans sort ¦", viz: "traces", exact: []string{"duration", "+start", "errors"}, detail: "filters to errors, orders by duration"},
		{text: "spans | count() by service sort ¦", exact: []string{"count", "+count"}},
		{text: "spans where service:$ser¦", exact: []string{"$service"}},
		{text: "logs where \"unclosed ¦", exact: []string{}},
		{text: "spans | share() ¦", excludes: []string{"+", "-", "*", "/"}},
		{text: "spans | count() ¦", includes: []string{"+", "-", "*", "/", ",", "as", "by", "every", "sort", "limit"}},
		{text: "spans where duration ¦", exact: []string{">", ">=", "<", "<=", "between", ":"}},
		{text: "spans where start_time ¦", exact: []string{"=", "!=", ">", ">=", "<", "<="}},
		{text: "logs where severity ¦", exact: []string{">", ">=", "<", "<=", ":"}},
		{text: "spans where service:api ¦", includes: []string{"service", "http.target", "-", "(", "not", "$service"}},
		{text: "spans | sum(¦", first: "duration", includes: []string{"user.score", "http.target"}},
		{text: "spans | count_distinct(¦", includes: []string{"service", "duration", "http.target"}},
		{text: "metrics where name:¦", includes: []string{"http.server.duration", "*"}},
		{text: "spans where enabled:¦", includes: []string{"true", "false", "*"}},
		{text: "spans where duration:¦", includes: []string{"500ms", "1s", "5s", "*"}},
		{text: "spans | count() limit ¦", exact: []string{"10", "50", "100", "1000"}},
		{text: "logs limit ¦", viz: "log_patterns", exact: []string{"10", "50"}},
		{text: "logs where `coalesce(body,'|') ¦", exact: []string{}},
	}
	for _, tt := range tests {
		t.Run(tt.text+tt.viz, func(t *testing.T) {
			req := completionRequest(tt.text)
			req.Viz = tt.viz
			req.Variables = []panel.Variable{{Name: "service"}}
			r := c.Complete(context.Background(), req)
			labels := completionLabels(r)
			if len(r.Items) > 50 || r.Schema.Window != "1h" || !r.Schema.Warm {
				t.Fatalf("response bounds/status: %+v", r)
			}
			if tt.exact != nil && !reflect.DeepEqual(labels, tt.exact) {
				t.Errorf("labels %v, want %v", labels, tt.exact)
			}
			for _, s := range tt.includes {
				if !slices.Contains(labels, s) {
					t.Errorf("%v missing %q", labels, s)
				}
			}
			for _, s := range tt.excludes {
				if slices.Contains(labels, s) {
					t.Errorf("%v includes %q", labels, s)
				}
			}
			if tt.first != "" && (len(labels) == 0 || labels[0] != tt.first) {
				t.Errorf("first = %v, want %s", labels, tt.first)
			}
			if tt.insert != "" && (len(r.Items) == 0 || r.Items[0].Insert != tt.insert) {
				t.Errorf("insert = %+v, want %q", r.Items, tt.insert)
			}
			if tt.detail != "" && !slices.ContainsFunc(r.Items, func(i Item) bool { return i.Detail == tt.detail }) {
				t.Errorf("missing detail %q: %+v", tt.detail, r.Items)
			}
			if tt.replace != nil && r.Replace != *tt.replace {
				t.Errorf("replace = %+v, want %+v", r.Replace, tt.replace)
			}
			if strings.Contains(tt.text, "sev¦erity") && (len(r.Items) == 0 || r.Items[0].Kind != "field") {
				t.Error("severity must be a field")
			}
			if strings.Contains(tt.text, "http.¦") && slices.ContainsFunc(r.Items, func(i Item) bool { return i.Kind != "attribute" }) {
				t.Error("expected attribute kinds")
			}
			if tt.text == "logs where ser¦" && r.Items[len(r.Items)-1].Detail != "free text searches body" {
				t.Error("free text hint must be last")
			}
		})
	}
	for _, marked := range []string{"spans | count(¦", "spans | rate(¦"} {
		r := c.Complete(context.Background(), completionRequest(marked))
		if len(r.Items) != 0 {
			t.Errorf("zero argument function: %+v", r.Items)
		}
	}
	r := c.Complete(context.Background(), completionRequest("spans where attributes['😀¦"))
	if r.Replace != (panel.TextSpan{Start: 24, End: 25}) {
		t.Errorf("code point replace %+v", r.Replace)
	}
	page := completionRequest("ser¦")
	page.Source = "logs"
	if !slices.Contains(completionLabels(c.Complete(context.Background(), page)), "service") {
		t.Error("page-supplied source completion")
	}
}

func TestCompleteRecentFlag(t *testing.T) {
	for _, tt := range []struct {
		window, want string
		recent       bool
	}{{"7d", "24h", true}, {"", "24h", true}, {"1d", "24h", false}, {"1h", "1h", false}, {"30m", "1h", false}} {
		t.Run(tt.window, func(t *testing.T) {
			requests := make(chan panel.SchemaRequest, 1)
			c := NewCompleter(completionSchemaFunc(func(_ context.Context, r panel.SchemaRequest) (*panel.Schema, error) {
				requests <- r
				return completionFixtureSchema(), nil
			}))
			r := c.Complete(context.Background(), CompleteRequest{Window: tt.window, Namespace: "prod"})
			if r.Schema.Window != tt.want || r.Schema.Recent != tt.recent {
				t.Errorf("status %+v", r.Schema)
			}
			select {
			case req := <-requests:
				if req.Window != tt.want || req.Namespace != "prod" {
					t.Errorf("schema request %+v", req)
				}
			case <-time.After(time.Second):
				t.Fatal("no schema fetch")
			}
		})
	}
}

func TestCompleteColdCacheCoalesces(t *testing.T) {
	gate := make(chan struct{})
	var calls, active, maximum atomic.Int32
	c := NewCompleter(completionSchemaFunc(func(ctx context.Context, _ panel.SchemaRequest) (*panel.Schema, error) {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		deadline, ok := ctx.Deadline()
		if !ok || time.Until(deadline) > 20*time.Second {
			t.Error("missing 20s detached deadline")
		}
		<-gate
		if ctx.Err() != nil {
			t.Errorf("fetch inherited request cancellation: %v", ctx.Err())
		}
		return completionFixtureSchema(), nil
	}))
	ctx, cancel := context.WithCancel(context.Background())
	var wg sync.WaitGroup
	for i := 0; i < 50; i++ {
		wg.Go(func() {
			r := c.Complete(ctx, CompleteRequest{Window: "1h", Namespace: string(rune('a' + i%6))})
			if r.Schema.Warm {
				t.Error("cold cache returned warm")
			}
		})
	}
	done := make(chan struct{})
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(time.Second):
		close(gate)
		t.Fatal("cold completion blocked")
	}
	cancel()
	close(gate)
	for i := 0; i < 6; i++ {
		waitCompletion(t, c, CompleteRequest{Window: "1h", Namespace: string(rune('a' + i))}, func(r CompleteResponse) bool { return r.Schema.Warm })
	}
	if calls.Load() != 6 || maximum.Load() > 4 {
		t.Fatalf("calls %d, max active %d", calls.Load(), maximum.Load())
	}
	var failed atomic.Int32
	bad := NewCompleter(completionSchemaFunc(func(context.Context, panel.SchemaRequest) (*panel.Schema, error) {
		failed.Add(1)
		return nil, errors.New("unavailable")
	}))
	now := time.Date(2026, 10, 29, 0, 0, 0, 0, time.UTC)
	bad.now = func() time.Time { return now }
	waitCompletion(t, bad, CompleteRequest{Window: "1h"}, func(r CompleteResponse) bool { return r.Schema.Error })
	r := bad.Complete(context.Background(), CompleteRequest{Window: "1h"})
	if r.Schema.Warm || !r.Schema.Error || failed.Load() != 1 {
		t.Fatalf("negative cache %+v calls %d", r.Schema, failed.Load())
	}
	now = now.Add(10 * time.Second)
	waitCompletion(t, bad, CompleteRequest{Window: "1h"}, func(r CompleteResponse) bool { return r.Schema.Error })
	if failed.Load() != 2 {
		t.Fatalf("negative expiry calls %d", failed.Load())
	}
}

func TestCompleteItemsRankByPrefixThenSubstringThenCount(t *testing.T) {
	c := warmCompleter(t)
	for _, tt := range []struct {
		text string
		want []string
	}{{"spans where htt¦", []string{"http.target", "http.method"}}, {"spans where tar¦", []string{"start_time", "http.target"}}} {
		labels := completionLabels(c.Complete(context.Background(), completionRequest(tt.text)))
		var attrs []string
		for _, s := range labels {
			if slices.Contains(tt.want, s) {
				attrs = append(attrs, s)
			}
		}
		if !reflect.DeepEqual(attrs, tt.want) {
			t.Errorf("%s = %v, want %v", tt.text, attrs, tt.want)
		}
	}
}

func TestCompleteVocabularyPrecedesSampledValues(t *testing.T) {
	schema := completionFixtureSchema()
	logs := schema.Signals["logs"]
	logs.Columns[0].Values = append(logs.Columns[0].Values, panel.Option{Value: "CUSTOM", Count: 999})
	schema.Signals["logs"] = logs
	c := NewCompleter(completionSchemaFunc(func(context.Context, panel.SchemaRequest) (*panel.Schema, error) { return schema, nil }))
	r := waitCompletion(t, c, completionRequest("logs where severity:¦"), func(r CompleteResponse) bool { return r.Schema.Warm })
	if got := completionLabels(r); len(got) < 7 || !slices.Equal(got[:7], []string{"trace", "debug", "info", "warn", "error", "fatal", "CUSTOM"}) {
		t.Fatalf("vocabulary before samples: %v", got)
	}
}

func TestCompleteSortAliasesLower(t *testing.T) {
	c := warmCompleter(t)
	req := completionRequest("spans | avg(duration), avg(user.score) sort ¦")
	r := c.Complete(context.Background(), req)
	if got := completionLabels(r); !slices.Equal(got, []string{"avg_duration_ms", "+avg_duration_ms", "avg_user_score", "+avg_user_score"}) {
		t.Fatalf("sort aliases %v", got)
	}
	for _, item := range r.Items {
		q, problems := parse(req.Text+item.Insert, "")
		if len(problems) > 0 {
			t.Fatal(problems)
		}
		resolved, problems := resolve(q, Options{Viz: "table"})
		if len(problems) > 0 {
			t.Fatal(problems)
		}
		if _, problems := lower(resolved); len(problems) > 0 {
			t.Fatal(problems)
		}
	}
}

func TestCompleteServiceMetadataBoostsWithoutHiding(t *testing.T) {
	schema := completionFixtureSchema()
	spans := schema.Signals["spans"]
	spans.Attributes[1].Services = "checkout, api"
	schema.Signals["spans"] = spans
	c := NewCompleter(completionSchemaFunc(func(context.Context, panel.SchemaRequest) (*panel.Schema, error) { return schema, nil }))
	r := waitCompletion(t, c, completionRequest("spans where service:checkout http.¦"), func(r CompleteResponse) bool { return r.Schema.Warm })
	if !slices.Equal(completionLabels(r), []string{"http.method", "http.target"}) {
		t.Fatalf("service boost: %v", completionLabels(r))
	}
	plain := c.Complete(context.Background(), completionRequest("spans where http.¦"))
	if !slices.Equal(completionLabels(plain), []string{"http.target", "http.method"}) {
		t.Fatalf("unscoped counts: %v", completionLabels(plain))
	}
}

func TestCompleteSchemaConcurrencyIsProcessWide(t *testing.T) {
	gate := make(chan struct{})
	started := make(chan struct{}, 12)
	var active, maximum, calls atomic.Int32
	source := completionSchemaFunc(func(context.Context, panel.SchemaRequest) (*panel.Schema, error) {
		calls.Add(1)
		n := active.Add(1)
		defer active.Add(-1)
		for old := maximum.Load(); n > old && !maximum.CompareAndSwap(old, n); old = maximum.Load() {
		}
		started <- struct{}{}
		<-gate
		return completionFixtureSchema(), nil
	})
	a, b := NewCompleter(source), NewCompleter(source)
	for i := 0; i < 6; i++ {
		a.Complete(context.Background(), CompleteRequest{Window: "1h", Namespace: string(rune('a' + i))})
		b.Complete(context.Background(), CompleteRequest{Window: "1h", Namespace: string(rune('a' + i))})
	}
	for i := 0; i < 4; i++ {
		select {
		case <-started:
		case <-time.After(time.Second):
			close(gate)
			t.Fatal("schema workers did not start")
		}
	}
	select {
	case <-started:
		close(gate)
		t.Fatal("more than four schema workers started across completers")
	case <-time.After(20 * time.Millisecond):
	}
	close(gate)
	for _, c := range []*Completer{a, b} {
		for i := 0; i < 6; i++ {
			waitCompletion(t, c, CompleteRequest{Window: "1h", Namespace: string(rune('a' + i))}, func(r CompleteResponse) bool { return r.Schema.Warm })
		}
	}
	if calls.Load() != 12 || maximum.Load() > 4 {
		t.Fatalf("process-wide calls %d max %d", calls.Load(), maximum.Load())
	}
}

func TestCompleteRecoversInvalidFiltersBeforeStage(t *testing.T) {
	c := NewCompleter(nil)
	r := c.Complete(context.Background(), completionRequest("logs where service:api and severity:error or body:timeout | count() by ¦"))
	if !slices.Contains(completionLabels(r), "service") {
		t.Fatalf("completion after invalid filter: %v", completionLabels(r))
	}
}

func TestCompleteInsertedValuesPreserveLiteralMeaning(t *testing.T) {
	schema := completionFixtureSchema()
	logs := schema.Signals["logs"]
	logs.Columns[1].Values = []panel.Option{{Value: "checkout api"}, {Value: "checkout:api"}, {Value: "count | limit 1"}, {Value: `say "hello"`}, {Value: "$service"}, {Value: "*"}, {Value: "500ms"}, {Value: "true"}, {Value: "api"}}
	schema.Signals["logs"] = logs
	c := NewCompleter(completionSchemaFunc(func(context.Context, panel.SchemaRequest) (*panel.Schema, error) { return schema, nil }))
	req := completionRequest("logs where service:¦")
	r := waitCompletion(t, c, req, func(r CompleteResponse) bool { return r.Schema.Warm })
	for _, tt := range []struct{ label, sql string }{{"checkout api", `service = 'checkout api'`}, {"checkout:api", `service = 'checkout:api'`}, {"count | limit 1", `service = 'count | limit 1'`}, {`say "hello"`, `service = 'say "hello"'`}, {"$service", `service = '$service'`}, {"*", `service = '*'`}, {"500ms", `service = '500ms'`}, {"true", `service = 'true'`}, {"api", `service = 'api'`}} {
		t.Run(tt.label, func(t *testing.T) {
			at := slices.IndexFunc(r.Items, func(item Item) bool { return item.Label == tt.label })
			if at < 0 {
				t.Fatalf("missing readable value %q", tt.label)
			}
			text := applyCompletion(req.Text, r.Replace, r.Items[at].Insert)
			if tt.label == "api" && r.Items[at].Insert != "api" {
				t.Errorf("safe value insert %q", r.Items[at].Insert)
			}
			compiled, problems := Compile(text, Options{})
			if len(problems) > 0 {
				t.Fatalf("inserted %q: %v", text, problems)
			}
			if !slices.Equal(compiled.Query.Where, []string{tt.sql}) {
				t.Fatalf("inserted %q lowers to %v, want %s", text, compiled.Query.Where, tt.sql)
			}
		})
	}
}

func TestCompleteInsertedAttributeFieldsPreserveScope(t *testing.T) {
	schema := completionFixtureSchema()
	spans := schema.Signals["spans"]
	spans.Attributes = []panel.AttributeSchema{{Key: "resource.foo", Scope: "attributes"}, {Key: "attributes.foo", Scope: "attributes"}, {Key: "service-name", Scope: "resource"}, {Key: "owner's.name", Scope: "attributes"}, {Key: `owner"s.name`, Scope: "resource"}, {Key: "where", Scope: "resource"}, {Key: "service", Scope: "resource"}}
	schema.Signals["spans"] = spans
	c := NewCompleter(completionSchemaFunc(func(context.Context, panel.SchemaRequest) (*panel.Schema, error) { return schema, nil }))
	for _, tt := range []struct{ text, key, scope, label string }{
		{"spans where res¦", "resource.foo", "attributes", "attributes['resource.foo']"},
		{"spans where attr¦", "attributes.foo", "attributes", "attributes['attributes.foo']"},
		{"spans where service-¦", "service-name", "resource", "resource['service-name']"},
		{"spans where resource.wh¦", "where", "resource", "resource.where"},
		{"spans where resource.serv¦", "service", "resource", "resource.service"},
		{"spans where attributes['own¦", "owner's.name", "attributes", "owner's.name"},
		{"spans where attributes['own¦']:api", "owner's.name", "attributes", "owner's.name"},
		{"spans where attributes['owner''¦", "owner's.name", "attributes", "owner's.name"},
		{`spans where resource["own¦`, `owner"s.name`, "resource", `owner"s.name`},
		{`spans where resource["own¦"]:api`, `owner"s.name`, "resource", `owner"s.name`},
		{`spans where resource["owner""¦`, `owner"s.name`, "resource", `owner"s.name`},
	} {
		t.Run(tt.text, func(t *testing.T) {
			req := completionRequest(tt.text)
			r := waitCompletion(t, c, req, func(r CompleteResponse) bool { return r.Schema.Warm })
			at := slices.IndexFunc(r.Items, func(item Item) bool {
				return item.Kind == "attribute" && item.Label == tt.label
			})
			if at < 0 {
				t.Fatalf("no completion for %q: %+v", tt.key, r.Items)
			}
			text := applyCompletion(req.Text, r.Replace, r.Items[at].Insert)
			if !strings.HasSuffix(text, ":api") {
				text += ":api"
			}
			compiled, problems := Compile(text, Options{})
			if len(problems) > 0 {
				t.Fatalf("inserted %q: %v", text, problems)
			}
			want := tt.scope + "['" + strings.ReplaceAll(tt.key, "'", "''") + "'] = 'api'"
			if !slices.Equal(compiled.Query.Where, []string{want}) {
				t.Fatalf("inserted %q lowers to %v, want %s", text, compiled.Query.Where, want)
			}
		})
	}
}

func applyCompletion(text string, replace panel.TextSpan, insert string) string {
	runes := []rune(text)
	return string(runes[:replace.Start]) + insert + string(runes[replace.End:])
}
