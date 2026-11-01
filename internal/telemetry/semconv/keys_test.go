package semconv

import (
	"strings"
	"testing"
)

// The 47 keys of proposal 0005, written out from the proposal text rather
// than from the generator, so a generator change cannot silently move them.
var proposal = map[string]Type{
	// Rule A, span keys.
	"span http.request.method":       String,
	"span http.response.status_code": Int,
	"span http.route":                String,
	"span url.scheme":                String,
	"span server.address":            String,
	"span server.port":               Int,
	"span error.type":                String,
	"span network.protocol.name":     String,
	"span network.protocol.version":  String,
	"span db.system.name":            String,
	"span db.namespace":              String,
	"span db.collection.name":        String,
	"span db.operation.name":         String,
	"span db.response.status_code":   String,
	"span db.query.summary":          String,
	"span db.stored_procedure.name":  String,
	"span network.peer.address":      String,
	"span network.peer.port":         Int,
	// Rule C, resource keys.
	"resource service.version":             String,
	"resource service.instance.id":         String,
	"resource deployment.environment.name": String,
	"resource container.id":                String,
	"resource container.image.name":        String,
	"resource k8s.cluster.name":            String,
	"resource k8s.cluster.uid":             String,
	"resource k8s.node.name":               String,
	"resource k8s.node.uid":                String,
	"resource k8s.deployment.name":         String,
	"resource k8s.deployment.uid":          String,
	"resource k8s.replicaset.name":         String,
	"resource k8s.replicaset.uid":          String,
	"resource k8s.statefulset.name":        String,
	"resource k8s.statefulset.uid":         String,
	"resource k8s.daemonset.name":          String,
	"resource k8s.daemonset.uid":           String,
	"resource k8s.job.name":                String,
	"resource k8s.job.uid":                 String,
	"resource k8s.cronjob.name":            String,
	"resource k8s.cronjob.uid":             String,
	"resource k8s.namespace.name":          String,
	"resource k8s.pod.name":                String,
	"resource k8s.pod.uid":                 String,
	"resource k8s.pod.hostname":            String,
	"resource k8s.pod.ip":                  String,
	"resource k8s.pod.start_time":          String,
	"resource k8s.container.name":          String,
	"resource k8s.container.restart_count": Int,
}

func TestKeysMatchProposal(t *testing.T) {
	if len(proposal) != 47 {
		t.Fatalf("oracle has %d keys, want 47", len(proposal))
	}
	seen := map[string]bool{}
	for _, k := range Keys {
		id := map[Scope]string{Span: "span", Resource: "resource"}[k.Scope] + " " + k.Name
		want, ok := proposal[id]
		if !ok {
			t.Errorf("unexpected key %s", id)
			continue
		}
		if k.Type != want {
			t.Errorf("%s has type %v, want %v", id, k.Type, want)
		}
		seen[id] = true
	}
	for id := range proposal {
		if !seen[id] {
			t.Errorf("missing key %s", id)
		}
	}
	if len(Keys) != len(proposal) {
		t.Errorf("got %d keys, want %d", len(Keys), len(proposal))
	}
}

func TestKeysHaveDistinctBitsInOrder(t *testing.T) {
	for i, k := range Keys {
		if int(k.Bit) != i || k.Bit >= 64 {
			t.Errorf("%s has bit %d at index %d", k.Name, k.Bit, i)
		}
	}
}

func TestColumnsDeriveFromNames(t *testing.T) {
	columns := map[string]string{}
	for _, k := range Keys {
		prefix := map[Scope]string{Span: "_a_", Resource: "_r_"}[k.Scope]
		if want := prefix + strings.ReplaceAll(k.Name, ".", "_"); k.Column != want {
			t.Errorf("%s has column %q, want %q", k.Name, k.Column, want)
		}
		if prev, ok := columns[k.Column]; ok {
			t.Errorf("%s and %s share column %s", prev, k.Name, k.Column)
		}
		columns[k.Column] = k.Name
		if strings.ContainsAny(k.Name, "<>") || strings.HasSuffix(k.Name, ".") {
			t.Errorf("%s is a template", k.Name)
		}
		if k.Source == "" {
			t.Errorf("%s has no source", k.Name)
		}
	}
}

func TestLookupMatchesScope(t *testing.T) {
	k, ok := Lookup(Span, "http.route")
	if !ok || k.Column != "_a_http_route" {
		t.Fatalf("Lookup(Span, http.route) = %+v, %v", k, ok)
	}
	if _, ok := Lookup(Resource, "http.route"); ok {
		t.Error("http.route resolved as a resource key")
	}
	if _, ok := Lookup(Span, "service.version"); ok {
		t.Error("service.version resolved as a span key")
	}
	for _, name := range []string{"service.name", "service.namespace", "http.method", "rpc.method", "url.path", "k8s.pod.label.app"} {
		if _, ok := Lookup(Span, name); ok {
			t.Errorf("%s resolved as a span key", name)
		}
		if _, ok := Lookup(Resource, name); ok {
			t.Errorf("%s resolved as a resource key", name)
		}
	}
	for _, want := range Keys {
		if got, ok := Lookup(want.Scope, want.Name); !ok || got != want {
			t.Errorf("Lookup(%v, %s) = %+v, %v", want.Scope, want.Name, got, ok)
		}
	}
}
