package fanql

import (
	"context"
	"fmt"
	"math"
	"reflect"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/config"
	"github.com/labstack/fanout/internal/ingest"
	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/query"
	"github.com/labstack/fanout/internal/telemetry"
	telemetrystore "github.com/labstack/fanout/internal/telemetry/store"
	collectorlogs "go.opentelemetry.io/proto/otlp/collector/logs/v1"
	commonpb "go.opentelemetry.io/proto/otlp/common/v1"
	logspb "go.opentelemetry.io/proto/otlp/logs/v1"
	"google.golang.org/grpc"
)

var semanticStart = time.Date(2026, 10, 1, 12, 0, 0, 0, time.UTC)

func semanticSpan(namespace string, index int, attributes map[string]any) telemetry.Span {
	at := semanticStart.Add(time.Duration(index) * time.Second).UnixNano()
	return telemetry.Span{
		Namespace: namespace, ServiceName: "api", TraceID: fmt.Sprintf("%s-trace-%d", namespace, index), SpanID: fmt.Sprintf("span-%d", index),
		Name: "GET /api", Kind: "SPAN_KIND_SERVER", StatusCode: "STATUS_CODE_OK", HTTPRoute: "/api", StartUnixNanos: at, EndUnixNanos: at + 1e6,
		DurationMS: 1, Attributes: attributes, IngestedAt: at,
	}
}

func semanticRun(t *testing.T, executor *panel.Executor, q panel.Query, variables []panel.Variable, vars map[string]panel.Value) panel.Result {
	return semanticRunViz(t, executor, q, "table", variables, vars)
}

func semanticRunViz(t *testing.T, executor *panel.Executor, q panel.Query, viz string, variables []panel.Variable, vars map[string]panel.Value) panel.Result {
	t.Helper()
	end := semanticStart.Add(time.Hour)
	results, err := executor.Run(t.Context(), panel.RunRequest{
		Dashboard: panel.Dashboard{Name: "Semantics", Time: panel.Time{From: &semanticStart, To: &end}, Variables: variables,
			Panels: []panel.Panel{{ID: "semantic", Title: "Semantics", Viz: viz, Query: &q}}}, Vars: vars,
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(results) != 1 || results[0].Status == panel.StatusError || results[0].Frame == nil {
		t.Fatalf("query %+v: %+v", q, results)
	}
	return results[0]
}

func semanticValue(t *testing.T, result panel.Result, name string) float64 {
	t.Helper()
	for i, column := range result.Frame.Columns {
		if column.Name == name {
			if len(result.Frame.Values[i]) != 1 {
				t.Fatalf("expected one aggregate value: %+v", result.Frame)
			}
			switch value := result.Frame.Values[i][0].(type) {
			case float64:
				return value
			case int64:
				return float64(value)
			default:
				t.Fatalf("unexpected numeric value %T: %v", value, value)
			}
		}
	}
	t.Fatalf("missing %s in %+v", name, result.Frame)
	return 0
}

func semanticCompile(t *testing.T, text string, variables []panel.Variable) panel.Query {
	t.Helper()
	c, problems := Compile(text, Options{Viz: "table", Variables: variables})
	if len(problems) != 0 {
		t.Fatalf("%q: %+v", text, problems)
	}
	return c.Query
}

func TestTruthTable(t *testing.T) {
	d, repo := newTestEngine(t)
	var spans []telemetry.Span
	for i, attributes := range []map[string]any{{"k": "v"}, {"k": "w"}, {}} {
		spans = append(spans, semanticSpan("values", i, attributes))
	}
	for i, code := range []string{"404", "200", "abc", ""} {
		span := semanticSpan("http", i, nil)
		span.HTTPStatusCode = code
		spans = append(spans, span)
	}
	for i, attributes := range []map[string]any{{"k": "GET /x"}, {"k": "POST /x"}, {}} {
		spans = append(spans, semanticSpan("patterns", i, attributes))
	}
	for i, attributes := range []map[string]any{{"a": 1, "b": 2}, {"a": 1, "b": 3}, {"a": 0, "b": 2}, {}} {
		spans = append(spans, semanticSpan("matrix", i, attributes))
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "truth-table", Spans: spans}); err != nil {
		t.Fatal(err)
	}
	executor := panel.NewExecutor(d, 30)
	cases := []struct {
		namespace, filter string
		want              int
	}{
		{"values", "k:v", 1}, {"values", "-k:v", 2}, {"values", "k!=v", 1}, {"values", "-k!=v", 2},
		{"values", "k ~ v", 1}, {"values", "-k ~ v", 2}, {"values", "k !~ v", 2},
		{"values", "k in (v, x)", 1}, {"values", "-k in (v, x)", 2}, {"values", "k not in (v, x)", 1}, {"values", "-k not in (v, x)", 2},
		{"values", "-k:*", 1}, {"values", "k:*", 2}, {"values", "k is null", 1}, {"values", "k is not null", 2},
		{"http", "http_status_code between 400 and 599", 1}, {"http", "-http_status_code between 400 and 599", 3},
		{"http", "http_status_code not between 400 and 599", 1}, {"http", "-http_status_code not between 400 and 599", 3},
		{"http", "http_status_code>=500", 0}, {"http", "-http_status_code>=500", 4},
		{"patterns", `k like "GET %"`, 1}, {"patterns", `k not like "GET %"`, 1}, {"patterns", `-k like "GET %"`, 2}, {"patterns", `-k not like "GET %"`, 2},
		{"matrix", "-(a:1 b:2)", 3}, {"matrix", "-(a:1 or b:2)", 1},
		{"values", "-`attributes['k'] = 'v'`", 2},
	}
	for _, tc := range cases {
		t.Run(tc.namespace+"/"+tc.filter, func(t *testing.T) {
			q := semanticCompile(t, "spans where namespace:"+tc.namespace+" "+tc.filter+" | count()", nil)
			result := semanticRun(t, executor, q, nil, nil)
			if got := semanticValue(t, result, "count"); got != float64(tc.want) {
				t.Fatalf("got %v, want %d; %s", got, tc.want, result.SQL)
			}
		})
	}
	t.Run("stored SQL negative stays SQL", func(t *testing.T) {
		q := panel.Query{From: "spans", Where: []string{"namespace = 'values'", "NOT (attributes['k'] = 'v')"}, Measures: []string{"count()"}}
		f, problems, err := Format(t.Context(), d, q, Options{Viz: "table"})
		if err != nil || len(problems) != 0 {
			t.Fatalf("format: %+v %v", problems, err)
		}
		if !strings.Contains(f.Text, "`NOT (attributes['k'] = 'v')`") {
			t.Fatal(f.Text)
		}
		result := semanticRun(t, executor, semanticCompile(t, f.Text, nil), nil, nil)
		if got := semanticValue(t, result, "count"); got != 1 {
			t.Fatalf("stored NOT matched %v, want 1", got)
		}
	})
	t.Run("numeric variable uses literal ordering", func(t *testing.T) {
		variables := []panel.Variable{{Name: "code", Kind: "text"}}
		q := semanticCompile(t, "spans where namespace:http http_status_code>=$code | count()", variables)
		result := semanticRun(t, executor, q, variables, map[string]panel.Value{"code": {Values: []string{"500"}}})
		if got := semanticValue(t, result, "count"); got != 0 {
			t.Fatalf("string variable matched %v, want 0", got)
		}
	})
	// Canonical telemetry's body and HTTP columns are non-nullable strings.
	// VALUES supplies actual SQL NULLs to exercise the complete leaf rule too.
	for _, tc := range []struct {
		text, values, column string
		want                 int
	}{
		{`logs where -length(body)>3`, "('long'), ('x'), (NULL)", "body", 2},
		{`spans where -starts_with(http_route, "/api")`, "('/api/cart'), ('/other'), (NULL)", "http_route", 2},
		{`spans where http_status_code between 400 and 599`, "('404'), ('200'), ('abc'), (NULL)", "http_status_code", 1},
		{`spans where -http_status_code between 400 and 599`, "('404'), ('200'), ('abc'), (NULL)", "http_status_code", 3},
		{`spans where http_status_code not between 400 and 599`, "('404'), ('200'), ('abc'), (NULL)", "http_status_code", 1},
		{`spans where -http_status_code not between 400 and 599`, "('404'), ('200'), ('abc'), (NULL)", "http_status_code", 3},
		{`spans where http_status_code>=500`, "('404'), ('200'), ('abc'), (NULL)", "http_status_code", 0},
		{`spans where -http_status_code>=500`, "('404'), ('200'), ('abc'), (NULL)", "http_status_code", 4},
	} {
		t.Run("actual NULL/"+tc.text, func(t *testing.T) {
			c, problems := Compile(tc.text, Options{})
			if len(problems) != 0 {
				t.Fatal(problems)
			}
			var count int
			if err := d.QueryRowScan(t.Context(), []any{&count}, "SELECT count(*) FROM (VALUES "+tc.values+") t("+tc.column+") WHERE "+c.Query.Where[0]); err != nil {
				t.Fatal(err)
			}
			if count != tc.want {
				t.Fatalf("got %d, want %d", count, tc.want)
			}
		})
	}
}

func TestVariablesUnderAllPerConstruct(t *testing.T) {
	d, repo := newTestEngine(t)
	var spans []telemetry.Span
	for i, status := range []string{"STATUS_CODE_ERROR", "STATUS_CODE_OK", "STATUS_CODE_ERROR", "STATUS_CODE_UNSET"} {
		span := semanticSpan("all", i, map[string]any{"x": true})
		span.StatusCode = status
		if i > 1 {
			span.ServiceName = "worker"
		}
		spans = append(spans, span)
	}
	if err := repo.Commit(t.Context(), telemetrystore.Batch{ID: "all-constructs", Spans: spans}); err != nil {
		t.Fatal(err)
	}
	executor := panel.NewExecutor(d, 30)
	variables := []panel.Variable{{Name: "service", Kind: "query", From: "spans", Field: "service", IncludeAll: true}}
	for _, tc := range []struct {
		filter                   string
		entries, remaining, rows int
	}{
		{"service:$service status:error", 2, 1, 2},
		{"(service:$service status:error)", 1, 0, 4},
		{"service:$service or service:worker", 1, 0, 4},
		{"service:$service (status:error or status:unset)", 2, 1, 3},
		{"-service:$service", 1, 0, 4},
		{"`service = $service AND attributes['x'] = true`", 1, 0, 4},
	} {
		t.Run(tc.filter, func(t *testing.T) {
			q := semanticCompile(t, "spans where "+tc.filter+" | count()", variables)
			if len(q.Where) != tc.entries {
				t.Fatalf("authored entries %d, want %d", len(q.Where), tc.entries)
			}
			result := semanticRun(t, executor, q, variables, map[string]panel.Value{"service": {All: true}})
			if got := semanticValue(t, result, "count"); got != float64(tc.rows) {
				t.Fatalf("got %v, want %d", got, tc.rows)
			}
			if got := strings.Count(result.SQL, " AND (("); got != tc.remaining {
				t.Fatalf("remaining filters %d, want %d: %s", got, tc.remaining, result.SQL)
			}
			if strings.Contains(result.SQL, "$service") {
				t.Fatal("All variable reached execution SQL")
			}
		})
	}
	c, problems := Compile("spans | count_if(service:$service)", Options{Variables: variables})
	if c != nil || len(problems) != 1 || problems[0].Message != "variables are not allowed inside count_if" {
		t.Fatalf("count_if: %+v %+v", c, problems)
	}
}

type semanticSubmitter struct {
	repo  *telemetrystore.Repository
	bands []string
}

func (s *semanticSubmitter) Submit(ctx context.Context, batch telemetrystore.Batch) error {
	for _, row := range batch.Logs {
		s.bands = append(s.bands, row.Severity)
	}
	batch.ID = "severity-ingress"
	return s.repo.Commit(ctx, batch)
}

type semanticRegistrar struct {
	logs collectorlogs.LogsServiceServer
}

func (r *semanticRegistrar) RegisterService(_ *grpc.ServiceDesc, service any) {
	if logs, ok := service.(collectorlogs.LogsServiceServer); ok {
		r.logs = logs
	}
}

func TestSeverityBandsEndToEnd(t *testing.T) {
	d, repo := newTestEngine(t)
	submitter := &semanticSubmitter{repo: repo}
	registrar := &semanticRegistrar{}
	ingest.RegisterOTLP(registrar, ingest.NewServer(config.Config{DefaultNamespace: "severity"}, submitter))
	pairs := []struct {
		text   string
		number int32
	}{{"WARN2", 14}, {"ERROR", 9}, {"FATAL", 0}, {"INFO2", 0}, {"custom", 0}, {"", 0}, {"info", 9}, {"error", 17}}
	var records []*logspb.LogRecord
	for i, pair := range pairs {
		records = append(records, &logspb.LogRecord{TimeUnixNano: uint64(semanticStart.Add(time.Duration(i) * time.Second).UnixNano()),
			SeverityText: pair.text, SeverityNumber: logspb.SeverityNumber(pair.number), Body: &commonpb.AnyValue{Value: &commonpb.AnyValue_StringValue{StringValue: fmt.Sprintf("row-%d", i)}}})
	}
	_, err := registrar.logs.Export(t.Context(), &collectorlogs.ExportLogsServiceRequest{ResourceLogs: []*logspb.ResourceLogs{{ScopeLogs: []*logspb.ScopeLogs{{LogRecords: records}}}}})
	if err != nil {
		t.Fatal(err)
	}
	wantBands := []string{"WARN", "INFO", "FATAL", "INFO", "CUSTOM", "UNSPECIFIED", "INFO", "ERROR"}
	if !reflect.DeepEqual(submitter.bands, wantBands) {
		t.Fatalf("ingress bands %v, want %v", submitter.bands, wantBands)
	}
	executor := panel.NewExecutor(d, 30)
	for _, tc := range []struct {
		filter string
		want   int
	}{
		{"severity>=warn", 3}, {"severity<error", 4}, {"severity between info and error", 5}, {"severity>=error", 2}, {"severity:custom", 1},
		{"severity>=trace", 6}, {"severity>=trace severity:custom", 0}, {"severity>=trace severity:unspecified", 0},
	} {
		t.Run(tc.filter, func(t *testing.T) {
			q := semanticCompile(t, "logs where "+tc.filter+" | count()", nil)
			result := semanticRun(t, executor, q, nil, nil)
			if got := semanticValue(t, result, "count"); got != float64(tc.want) {
				t.Fatalf("got %v, want %d", got, tc.want)
			}
		})
	}
	q := semanticCompile(t, "logs | error_rate()", nil)
	if got := semanticValue(t, semanticRun(t, executor, q, nil, nil), "error_rate"); math.Abs(got-25) > 1e-9 {
		t.Fatalf("error_rate = %v, want 25%% (2 of 8 rows)", got)
	}
}

var semanticVariables = regexp.MustCompile(`\$([A-Za-z_][A-Za-z_0-9]*)`)

func semanticCheckQuery(t *testing.T, d *query.Duck, q panel.Query) {
	t.Helper()
	var variables []panel.Variable
	seen := map[string]bool{}
	for _, match := range semanticVariables.FindAllStringSubmatch(strings.Join(q.Where, " "), -1) {
		name := match[1]
		if !seen[name] {
			variables = append(variables, panel.Variable{Name: name, Kind: "text"})
			seen[name] = true
		}
	}
	dashboard := panel.Dashboard{Name: "Binding", Variables: variables, Panels: []panel.Panel{{ID: "binding", Title: "Binding", Viz: "table", Query: &q}}}
	panel.Normalize(&dashboard)
	if _, problems, err := panel.Check(t.Context(), d, &dashboard); err != nil || len(problems) != 0 {
		t.Fatalf("query %+v: %+v %v", q, problems, err)
	}
}

func TestEngineBindingOfEveryLoweredShape(t *testing.T) {
	d, _ := newTestEngine(t)
	seen := map[string]bool{}
	for _, fixture := range readCorpus(t) {
		if fixture.Query == nil || fixture.GateProblems {
			continue
		}
		for _, entry := range fixture.Query.Where {
			key := fixture.Query.From + "\x00" + entry
			if seen[key] {
				continue
			}
			seen[key] = true
			t.Run(fixture.Query.From+"/"+entry, func(t *testing.T) {
				semanticCheckQuery(t, d, panel.Query{From: fixture.Query.From, Where: []string{entry}, Measures: []string{"count()"}})
			})
		}
		for _, measure := range fixture.Query.Measures {
			if !strings.Contains(measure, "count_if(") {
				continue
			}
			key := fixture.Query.From + "\x00measure:" + measure
			if seen[key] {
				continue
			}
			seen[key] = true
			t.Run(fixture.Query.From+"/"+measure, func(t *testing.T) {
				semanticCheckQuery(t, d, panel.Query{From: fixture.Query.From, Measures: []string{measure}})
			})
		}
	}
	if len(seen) < 20 {
		t.Fatalf("only %d distinct corpus shapes were bound", len(seen))
	}
	c, problems := Compile("spans where k in ("+strings.TrimSuffix(strings.Repeat("0,", 100), ",")+") | count()", Options{Viz: "table"})
	if len(problems) != 0 {
		t.Fatal(problems)
	}
	if len(c.Query.Where[0]) > 500 {
		t.Fatal("the 100-value fixture crossed the filter byte bound")
	}
	semanticCheckQuery(t, d, c.Query)
}

func TestWorkloadsEndToEnd(t *testing.T) {
	d, repo := newTestEngine(t)
	batch := telemetrystore.Batch{ID: "v1-workloads"}
	for minute := range 60 {
		attributes := map[string]any{"http.target": "/cart", "db.statement": "SELECT * FROM orders"}
		if minute%10 != 0 {
			attributes["user.id"] = fmt.Sprintf("user-%d", minute%3)
		}
		span := semanticSpan("workloads", minute, attributes)
		span.StartUnixNanos = semanticStart.Add(time.Duration(minute) * time.Minute).UnixNano()
		span.ServiceName, span.HTTPRoute, span.TraceID = "checkout", "/cart", fmt.Sprintf("trace-%d", minute)
		span.DurationMS = 100
		if minute%2 == 0 {
			span.DurationMS = 900
		} else {
			span.HTTPRoute = "/payment"
		}
		span.EndUnixNanos = span.StartUnixNanos + int64(span.DurationMS*1e6)
		switch minute % 3 {
		case 0:
			span.HTTPStatusCode, span.StatusCode, span.DBSystem, span.Name = "500", "STATUS_CODE_ERROR", "postgresql", "SELECT orders"
		case 1:
			span.HTTPStatusCode, span.StatusCode = "404", "STATUS_CODE_UNSET"
		default:
			span.HTTPStatusCode, span.StatusCode = "200", "STATUS_CODE_OK"
		}
		batch.Spans = append(batch.Spans, span)
	}
	for minute := range 12 {
		at := semanticStart.Add(time.Duration(minute) * time.Minute).UnixNano()
		severity := "INFO"
		if minute%3 == 0 {
			severity = "ERROR"
		}
		batch.Logs = append(batch.Logs, telemetry.Log{Namespace: "workloads", ServiceName: "api", EventUnixNanos: at, TimeUnixNanos: at, IngestedAt: at,
			Severity: severity, Body: fmt.Sprintf("timeout request %d failed", minute), BodyTemplate: "timeout request <*> failed", TraceID: fmt.Sprintf("trace-%d", minute)})
	}
	for scrape := range 3 {
		// last(value) deliberately observes only the final five minutes.
		at := semanticStart.Add(time.Duration(scrape+57) * time.Minute).UnixNano()
		base := telemetry.Metric{Namespace: "workloads", ServiceName: "checkout", EventUnixNanos: at, TimeUnixNanos: at, IngestedAt: at,
			Resource: map[string]any{"k8s.pod.name": "checkout-0"}}
		counter := base
		counter.Name, counter.Type, counter.Monotonic, counter.Temporality = "http.server.requests", "sum", true, "cumulative"
		counter.StartTimeUnixNanos, counter.Value = semanticStart.UnixNano(), float64((scrape+1)*10)
		queue := base
		queue.Name, queue.Type, queue.Value = "queue.lag", "gauge", float64(4-scrape)
		restarts := base
		restarts.Name, restarts.Type, restarts.Value = "k8s.container.restarts", "gauge", float64(2+scrape)
		histogram := base
		histogram.Name, histogram.Type, histogram.Temporality, histogram.Unit = "http.server.duration", "histogram", "delta", "ms"
		histogram.StartTimeUnixNanos = semanticStart.Add(time.Duration(scrape+56) * time.Minute).UnixNano()
		histogram.HistBoundsJSON, histogram.HistCountsJSON, histogram.HistCount, histogram.HistSum = "[100,500]", "[2,3,5]", 10, 1500
		for _, metric := range []telemetry.Metric{counter, queue, restarts, histogram} {
			metric.SeriesID = telemetry.MetricSeriesID(metric)
			batch.Metrics = append(batch.Metrics, metric)
		}
	}
	if err := repo.Commit(t.Context(), batch); err != nil {
		t.Fatal(err)
	}
	executor := panel.NewExecutor(d, 30)
	variables := []panel.Variable{{Name: "service", Kind: "query", From: "spans", Field: "service", IncludeAll: true}}
	vars := map[string]panel.Value{"service": {Values: []string{"checkout"}}}
	vizzes := []string{"timeseries", "traces", "table", "table", "stat", "histogram", "table", "table", "logs", "log_patterns", "timeseries", "table"}
	cases := make([]struct{ text, viz string }, 0, len(glanceExamples)+10)
	for i, text := range glanceExamples {
		cases = append(cases, struct{ text, viz string }{text, vizzes[i]})
	}
	cases = append(cases, []struct{ text, viz string }{
		{`spans where kind:server | p99(duration) by http_route every 1m`, "timeseries"},
		{`spans | p95(duration) by operation sort p95 limit 10`, "table"},
		{`logs where trace_id:trace-0`, "logs"},
		{`metrics where name:http.server.duration | histogram(value)`, "histogram"},
		{`spans | count_if(http_status_code>=400 http_status_code<500) as client_errors, count_if(http_status_code>=500) as server_errors by http_route`, "table"},
		{`spans where db_system:postgresql duration>500ms | count() by attributes['db.statement']`, "table"},
		{`metrics where name:queue.lag | last(value) by service`, "table"},
		{`spans | count_if(status:error) / count() * 100 as error_pct by http_route`, "table"},
		{`spans | count_if(status:error) as errors, count() as total, count_if(status:error) / count() * 100 as error_pct by http_route`, "table"},
		{`spans where -user.id:* | count()`, "table"},
	}...)
	for _, tc := range cases {
		t.Run(tc.text, func(t *testing.T) {
			c, problems := Compile(tc.text, Options{Viz: tc.viz, Variables: variables})
			if len(problems) != 0 {
				t.Fatalf("compile: %+v", problems)
			}
			dashboard := panel.Dashboard{Name: "V1 workload", Variables: variables, Panels: []panel.Panel{{ID: "workload", Title: "Workload", Viz: tc.viz, Query: &c.Query}}}
			panel.Normalize(&dashboard)
			if _, problems, err := panel.Check(t.Context(), d, &dashboard); len(problems) != 0 || err != nil {
				t.Fatalf("check: %+v %v", problems, err)
			}
			result := semanticRunViz(t, executor, c.Query, tc.viz, variables, vars)
			if result.Status != panel.StatusOK || result.Frame.Rows == 0 {
				t.Fatalf("workload returned no matching data: %+v", result)
			}
			if strings.Contains(tc.text, "name:queue.lag") && semanticValue(t, result, "last") != 2 {
				t.Fatalf("queue lag: %+v", result.Frame)
			}
			if strings.Contains(tc.text, "name:k8s.container.restarts") && semanticValue(t, result, "last") != 4 {
				t.Fatalf("pod restarts: %+v", result.Frame)
			}
			if tc.text == `spans where -user.id:* | count()` && semanticValue(t, result, "count") != 6 {
				t.Fatalf("missing users: %+v", result.Frame)
			}
		})
	}
}
