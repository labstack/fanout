package fanql

import (
	"fmt"
	"strings"
	"testing"

	"github.com/labstack/fanout/internal/panel"
	"github.com/labstack/fanout/internal/telemetry"
)

func TestReferenceCoversEveryRegistryEntry(t *testing.T) {
	ref := Reference()
	check := func(entry string) {
		t.Helper()
		if !strings.Contains(ref, "`"+entry+"`") {
			t.Errorf("reference omits %q", entry)
		}
	}
	for _, m := range panel.MeasureSpecs() {
		check(m.Name)
	}
	for _, bucket := range panel.BucketNames() {
		check(bucket)
	}
	for _, name := range panel.FilterFunctionNames() {
		check(name)
	}
	for _, signal := range panel.SignalNames() {
		columns, _ := panel.SignalColumns(signal)
		for _, column := range columns {
			if !strings.Contains(ref, "| `"+column.Name+"` | `"+string(column.Type)+"` |") {
				t.Errorf("missing %s column %s with type", signal, column.Name)
			}
		}
	}
	for _, table := range []map[string]string{statusAliases, kindAliases, typeAliases, telemetry.SeverityAliases()} {
		for alias, stored := range table {
			check(alias)
			check(stored)
		}
	}
	for _, band := range telemetry.SeverityBands {
		check(band)
	}
	for _, op := range []string{":", "=", "!=", "<>", ">", ">=", "<", "<=", "~", "!~", "in", "not in", "between", "not between", "is null", "is not null", "like", "ilike", "not like", "not ilike"} {
		check(op)
	}
	for _, table := range []map[string]bool{sources, clauses, expressionWords, modifiers, reserved} {
		for word := range table {
			check(word)
		}
	}
}

func TestReferenceIsDeterministicAndMarksPreview(t *testing.T) {
	ref := Reference()
	for range 10 {
		if Reference() != ref {
			t.Fatal("reference order is unstable")
		}
	}
	if !strings.HasPrefix(ref, fmt.Sprintf("# FanQL %d\n", Version)) || !strings.Contains(ref, "Preview: the syntax may change before GA") {
		t.Fatal("missing version/preview")
	}
}

func TestReferenceExamplesCompile(t *testing.T) {
	inFence, examples := false, 0
	for _, line := range strings.Split(Reference(), "\n") {
		if strings.HasPrefix(line, "```") {
			inFence = !inFence
			continue
		}
		if !inFence || strings.TrimSpace(line) == "" {
			continue
		}
		examples++
		if _, problems := Compile(line, Options{}); len(problems) != 0 {
			t.Errorf("%q: %+v", line, problems)
		}
	}
	if examples != len(glanceExamples) {
		t.Fatalf("got %d examples, want all %d at-a-glance queries", examples, len(glanceExamples))
	}
	for _, example := range glanceExamples {
		if !strings.Contains(Reference(), example+"\n") {
			t.Errorf("missing at-a-glance example %q", example)
		}
	}
}

func TestReferenceStatesTheNullRule(t *testing.T) {
	for _, rule := range []string{"rows without `k` are excluded; `-k:v` includes them", "SQL spelling, SQL meaning", "a filter that mentions a variable set to All is removed; keep fixed conditions in their own terms", "no stored log row bypasses", "root service", "root duration", "nullif", "256 tokens", "24 aggregates"} {
		if !strings.Contains(Reference(), rule) {
			t.Errorf("missing rule %q", rule)
		}
	}
}
