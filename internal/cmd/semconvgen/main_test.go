package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/telemetry/semconv"
)

// A miniature resolved registry with one case per rule clause.
const fixture = `{"groups": [
 {"id": "metric.http.server.request.duration", "type": "metric", "stability": "stable",
  "metric_name": "http.server.request.duration", "attributes": [
   {"name": "http.route", "type": "string", "stability": "stable", "requirement_level": {"conditionally_required": "If available."}},
   {"name": "http.response.status_code", "type": "int", "stability": "stable", "requirement_level": "required"},
   {"name": "server.address", "type": "string", "stability": "stable", "requirement_level": "opt_in"},
   {"name": "url.template", "type": "string", "stability": "development", "requirement_level": {"recommended": "If available."}},
   {"name": "db.query.text", "type": "string", "stability": "stable", "requirement_level": "opt_in"},
   {"name": "http.request.method", "type": {"members": [{"id": "get", "value": "GET"}]}, "stability": "stable", "requirement_level": "required"}]},
 {"id": "metric.http.client.request.duration", "type": "metric", "stability": "stable",
  "metric_name": "http.client.request.duration", "attributes": [
   {"name": "server.address", "type": "string", "stability": "stable", "requirement_level": "required"},
   {"name": "http.old", "type": "string", "stability": "stable", "deprecated": {"reason": "renamed"}, "requirement_level": "recommended"}]},
 {"id": "metric.db.client.operation.duration", "type": "metric", "stability": "stable",
  "metric_name": "db.client.operation.duration", "attributes": [
   {"name": "db.code", "type": {"members": [{"id": "a", "value": 1}, {"id": "b", "value": 2}]}, "stability": "stable", "requirement_level": "recommended"}]},
 {"id": "metric.rpc.server.call.duration", "type": "metric", "stability": "release_candidate",
  "metric_name": "rpc.server.call.duration", "attributes": [
   {"name": "rpc.method", "type": "string", "stability": "release_candidate", "requirement_level": "required"}]},
 {"id": "metric.rpc.client.call.duration", "type": "metric", "stability": "release_candidate",
  "metric_name": "rpc.client.call.duration", "attributes": []},
 {"id": "registry.service", "type": "attribute_group", "attributes": [
   {"name": "service.name", "type": "string", "stability": "stable"},
   {"name": "service.namespace", "type": "string", "stability": "stable"},
   {"name": "service.version", "type": "string", "stability": "stable"}]},
 {"id": "registry.k8s", "type": "attribute_group", "attributes": [
   {"name": "k8s.pod.name", "type": "string", "stability": "stable"},
   {"name": "k8s.pod.label", "type": "template[string]", "stability": "stable"},
   {"name": "k8s.container.restart_count", "type": "int", "stability": "stable"},
   {"name": "k8s.pod.status.phase", "type": "string", "stability": "development"}]},
 {"id": "registry.container", "type": "attribute_group", "attributes": [
   {"name": "container.image.tags", "type": "string[]", "stability": "stable"}]},
 {"id": "registry.cloudevents", "type": "attribute_group", "attributes": [
   {"name": "cloudevents.event_id", "type": "string", "stability": "stable"}]},
 {"id": "registry.url", "type": "attribute_group", "attributes": [
   {"name": "url.path", "type": "string", "stability": "stable"}]},
 {"id": "span.k8s", "type": "span", "attributes": [
   {"name": "k8s.extra", "type": "string", "stability": "stable"}]}
]}`

func load(t *testing.T, text string) registry {
	t.Helper()
	var reg registry
	if err := json.Unmarshal([]byte(text), &reg); err != nil {
		t.Fatal(err)
	}
	return reg
}

func TestSelectKeysAppliesRules(t *testing.T) {
	keys, err := selectKeys(load(t, fixture))
	if err != nil {
		t.Fatal(err)
	}
	want := []semconv.Key{
		{Name: "db.code", Scope: semconv.Span, Type: semconv.Int, Column: "_a_db_code", Bit: 0, Source: "metric.db.client.operation.duration"},
		{Name: "http.request.method", Scope: semconv.Span, Type: semconv.String, Column: "_a_http_request_method", Bit: 1, Source: "metric.http.server.request.duration"},
		{Name: "http.response.status_code", Scope: semconv.Span, Type: semconv.Int, Column: "_a_http_response_status_code", Bit: 2, Source: "metric.http.server.request.duration"},
		{Name: "http.route", Scope: semconv.Span, Type: semconv.String, Column: "_a_http_route", Bit: 3, Source: "metric.http.server.request.duration"},
		{Name: "server.address", Scope: semconv.Span, Type: semconv.String, Column: "_a_server_address", Bit: 4, Source: "metric.http.client.request.duration"},
		{Name: "k8s.container.restart_count", Scope: semconv.Resource, Type: semconv.Int, Column: "_r_k8s_container_restart_count", Bit: 5, Source: "registry.k8s"},
		{Name: "k8s.pod.name", Scope: semconv.Resource, Type: semconv.String, Column: "_r_k8s_pod_name", Bit: 6, Source: "registry.k8s"},
		{Name: "service.version", Scope: semconv.Resource, Type: semconv.String, Column: "_r_service_version", Bit: 7, Source: "registry.service"},
	}
	if len(keys) != len(want) {
		t.Fatalf("got %d keys %+v, want %d", len(keys), keys, len(want))
	}
	for i := range want {
		if keys[i] != want[i] {
			t.Errorf("key %d = %+v, want %+v", i, keys[i], want[i])
		}
	}
}

func TestSelectKeysRejectsUnknownInputs(t *testing.T) {
	cases := map[string]string{
		"missing metric": strings.Replace(fixture, `"metric_name": "rpc.client.call.duration"`, `"metric_name": "rpc.other"`, 1),
		"boolean key":    strings.Replace(fixture, `"name": "url.path", "type": "string"`, `"name": "cloud.flag", "type": "boolean"`, 1),
		"mixed enum":     strings.Replace(fixture, `"value": 2`, `"value": "two"`, 1),
	}
	for name, text := range cases {
		t.Run(name, func(t *testing.T) {
			if _, err := selectKeys(load(t, text)); err == nil {
				t.Fatal("expected an error")
			}
		})
	}
}

func TestRunWritesHeaderAndReportsChanges(t *testing.T) {
	dir := t.TempDir()
	in := filepath.Join(dir, "resolved.json")
	out := filepath.Join(dir, "keys_gen.go")
	if err := os.WriteFile(in, []byte(fixture), 0o600); err != nil {
		t.Fatal(err)
	}
	var log strings.Builder
	if err := run(in, out, "v9.9.9", "0.0.1", "abc123", &log); err != nil {
		t.Fatal(err)
	}
	src, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{
		"// Code generated by internal/cmd/semconvgen; DO NOT EDIT.\n",
		"semantic conventions v9.9.9 (archive sha256 abc123), resolved by weaver 0.0.1",
		`{Name: "k8s.pod.name", Scope: Resource, Type: String, Column: "_r_k8s_pod_name", Bit: 6, Source: "registry.k8s"},`,
	} {
		if !strings.Contains(string(src), want) {
			t.Errorf("output lacks %q:\n%s", want, src)
		}
	}
	// The fixture drops most committed keys and adds db.code.
	for _, want := range []string{"added Span db.code", "removed Resource k8s.pod.uid", "needs a new batch format version"} {
		if !strings.Contains(log.String(), want) {
			t.Errorf("log lacks %q:\n%s", want, log.String())
		}
	}
}
