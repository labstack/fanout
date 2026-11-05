// semconvgen applies the dedicated-column key rule of proposal 0005 to a
// weaver-resolved semantic conventions registry and writes
// internal/telemetry/semconv/keys_gen.go. scripts/semconv-generate.sh pins
// and verifies its inputs; run it through `just semconv-generate`.
package main

import (
	"bytes"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"go/format"
	"io"
	"os"
	"slices"
	"strings"

	"github.com/labstack/fanout/internal/telemetry/semconv"
)

// Rule A: the Stable duration metrics whose non-Opt-In attributes are span keys.
var metrics = []string{
	"http.server.request.duration",
	"http.client.request.duration",
	"db.client.operation.duration",
	"rpc.server.call.duration",
	"rpc.client.call.duration",
}

// Rule C: the namespaces whose Stable registry attributes are resource keys.
var namespaces = []string{"service", "deployment", "k8s", "container", "host", "cloud"}

// The service and namespace identity columns already store these.
var identity = []string{"service.name", "service.namespace"}

type registry struct {
	Groups []group `json:"groups"`
}

type group struct {
	ID         string      `json:"id"`
	Type       string      `json:"type"`
	Stability  string      `json:"stability"`
	MetricName string      `json:"metric_name"`
	Attributes []attribute `json:"attributes"`
}

type attribute struct {
	Name       string          `json:"name"`
	Type       json.RawMessage `json:"type"`
	Level      json.RawMessage `json:"requirement_level"`
	Stability  string          `json:"stability"`
	Deprecated json.RawMessage `json:"deprecated"`
}

func main() {
	in := flag.String("in", "", "weaver-resolved registry JSON")
	out := flag.String("out", "internal/telemetry/semconv/keys_gen.go", "generated Go file")
	tag := flag.String("tag", "", "semantic conventions tag")
	weaver := flag.String("weaver", "", "weaver version that resolved the registry")
	sum := flag.String("sha256", "", "sha256 of the semantic conventions tag archive")
	flag.Parse()
	if err := run(*in, *out, *tag, *weaver, *sum, os.Stderr); err != nil {
		fmt.Fprintln(os.Stderr, "semconvgen:", err)
		os.Exit(1)
	}
}

func run(in, out, tag, weaver, sum string, log io.Writer) error {
	if in == "" || tag == "" || weaver == "" || sum == "" {
		return errors.New("-in, -tag, -weaver and -sha256 are required")
	}
	data, err := os.ReadFile(in)
	if err != nil {
		return err
	}
	var reg registry
	if err := json.Unmarshal(data, &reg); err != nil {
		return fmt.Errorf("parse %s: %w", in, err)
	}
	keys, err := selectKeys(reg)
	if err != nil {
		return err
	}
	src, err := render(keys, fmt.Sprintf("semantic conventions %s (archive sha256 %s), resolved by weaver %s", tag, sum, weaver))
	if err != nil {
		return err
	}
	if changes := diff(keys); len(changes) > 0 {
		for _, c := range changes {
			fmt.Fprintln(log, c)
		}
		fmt.Fprintln(log, "The key list changed: this is a schema change and needs a new batch format version.")
	}
	return os.WriteFile(out, src, 0o644)
}

// selectKeys applies rules A and C. Span keys come first, then resource keys,
// each sorted by name; a key's bit is its index.
func selectKeys(reg registry) ([]semconv.Key, error) {
	var span, resource []semconv.Key
	seen := map[string]bool{}
	for _, name := range metrics {
		i := slices.IndexFunc(reg.Groups, func(g group) bool { return g.Type == "metric" && g.MetricName == name })
		if i < 0 {
			return nil, fmt.Errorf("metric %s is missing from the registry", name)
		}
		g := reg.Groups[i]
		if g.Stability != "stable" {
			continue
		}
		for _, a := range g.Attributes {
			if seen[a.Name] || !stable(a) {
				continue
			}
			level, err := requirement(a.Level)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", g.ID, a.Name, err)
			}
			if level == "opt_in" {
				continue
			}
			typ, scalar, err := valueType(a.Type)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", g.ID, a.Name, err)
			}
			if !scalar {
				continue
			}
			seen[a.Name] = true
			span = append(span, semconv.Key{Name: a.Name, Scope: semconv.Span, Type: typ, Source: g.ID})
		}
	}
	seen = map[string]bool{}
	for _, g := range reg.Groups {
		if g.Type != "attribute_group" || !strings.HasPrefix(g.ID, "registry.") {
			continue
		}
		for _, a := range g.Attributes {
			ns, _, _ := strings.Cut(a.Name, ".")
			if seen[a.Name] || !slices.Contains(namespaces, ns) || slices.Contains(identity, a.Name) || !stable(a) {
				continue
			}
			typ, scalar, err := valueType(a.Type)
			if err != nil {
				return nil, fmt.Errorf("%s %s: %w", g.ID, a.Name, err)
			}
			if !scalar {
				continue
			}
			seen[a.Name] = true
			resource = append(resource, semconv.Key{Name: a.Name, Scope: semconv.Resource, Type: typ, Source: g.ID})
		}
	}
	byName := func(a, b semconv.Key) int { return strings.Compare(a.Name, b.Name) }
	slices.SortFunc(span, byName)
	slices.SortFunc(resource, byName)
	keys := append(span, resource...)
	if len(keys) > 64 {
		return nil, fmt.Errorf("%d keys exceed the 64-key limit", len(keys))
	}
	for i := range keys {
		prefix := "_a_"
		if keys[i].Scope == semconv.Resource {
			prefix = "_r_"
		}
		keys[i].Column = prefix + strings.ReplaceAll(keys[i].Name, ".", "_")
		keys[i].Bit = uint8(i)
	}
	return keys, nil
}

func stable(a attribute) bool {
	deprecated := len(a.Deprecated) > 0 && string(a.Deprecated) != "null"
	return a.Stability == "stable" && !deprecated
}

// requirement returns the level of "required" or {"conditionally_required": note}.
func requirement(raw json.RawMessage) (string, error) {
	var level string
	if json.Unmarshal(raw, &level) == nil {
		return level, nil
	}
	var noted map[string]json.RawMessage
	if err := json.Unmarshal(raw, &noted); err != nil || len(noted) != 1 {
		return "", fmt.Errorf("unknown requirement level %s", raw)
	}
	for level := range noted {
		return level, nil
	}
	return "", nil
}

// valueType maps a semconv type to a column type. Arrays, templates and any
// are not scalar; other scalar types have no column type yet and fail.
func valueType(raw json.RawMessage) (semconv.Type, bool, error) {
	var name string
	if json.Unmarshal(raw, &name) == nil {
		switch name {
		case "string":
			return semconv.String, true, nil
		case "int":
			return semconv.Int, true, nil
		case "boolean", "double":
			return 0, false, fmt.Errorf("type %s has no column type", name)
		}
		return 0, false, nil
	}
	var enum struct {
		Members []struct {
			Value json.RawMessage `json:"value"`
		} `json:"members"`
	}
	if err := json.Unmarshal(raw, &enum); err != nil || len(enum.Members) == 0 {
		return 0, false, fmt.Errorf("unknown type %s", raw)
	}
	var strs, ints int
	for _, m := range enum.Members {
		var s string
		var n int64
		switch {
		case json.Unmarshal(m.Value, &s) == nil:
			strs++
		case json.Unmarshal(m.Value, &n) == nil:
			ints++
		}
	}
	switch len(enum.Members) {
	case strs:
		return semconv.String, true, nil
	case ints:
		return semconv.Int, true, nil
	}
	return 0, false, fmt.Errorf("enum members mix types in %s", raw)
}

func render(keys []semconv.Key, source string) ([]byte, error) {
	var b bytes.Buffer
	fmt.Fprintf(&b, "// Code generated by internal/cmd/semconvgen; DO NOT EDIT.\n// Source: %s.\n\npackage semconv\n\n", source)
	b.WriteString("// Keys lists the dedicated columns in bit order.\nvar Keys = [...]Key{\n")
	for _, k := range keys {
		fmt.Fprintf(&b, "\t{Name: %q, Scope: %s, Type: %s, Column: %q, Bit: %d, Source: %q},\n",
			k.Name, scopeName(k.Scope), typeName(k.Type), k.Column, k.Bit, k.Source)
	}
	b.WriteString("}\n")
	return format.Source(b.Bytes())
}

func scopeName(s semconv.Scope) string {
	if s == semconv.Resource {
		return "Resource"
	}
	return "Span"
}

func typeName(t semconv.Type) string {
	if t == semconv.Int {
		return "Int"
	}
	return "String"
}

// diff reports how keys differ from the committed list.
func diff(keys []semconv.Key) []string {
	var changes []string
	next := map[string]bool{}
	for _, k := range keys {
		next[scopeName(k.Scope)+" "+k.Name] = true
		old, ok := semconv.Lookup(k.Scope, k.Name)
		switch {
		case !ok:
			changes = append(changes, "added "+scopeName(k.Scope)+" "+k.Name)
		case old != k:
			changes = append(changes, "changed "+scopeName(k.Scope)+" "+k.Name)
		}
	}
	for _, k := range semconv.Keys {
		if !next[scopeName(k.Scope)+" "+k.Name] {
			changes = append(changes, "removed "+scopeName(k.Scope)+" "+k.Name)
		}
	}
	return changes
}
