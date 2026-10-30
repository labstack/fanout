package fanql

import (
	"encoding/json"
	"reflect"
	"slices"
	"testing"

	"github.com/labstack/fanout/internal/panel"
)

type registryMeaning struct {
	query    string
	problems string
}

func registrySnapshot(fixtures []corpusFixture) map[string]registryMeaning {
	out := make(map[string]registryMeaning, len(fixtures))
	for _, fixture := range fixtures {
		compiled, problems := Compile(fixture.Text, Options{Viz: fixture.Viz})
		var query []byte
		if compiled != nil {
			query, _ = json.Marshal(compiled.Query)
		}
		raw, _ := json.Marshal(problems)
		key := fixture.Text
		if fixture.Viz != "" {
			key += "\n[viz=" + fixture.Viz + "]"
		}
		out[key] = registryMeaning{query: string(query), problems: string(raw)}
	}
	return out
}

// Previously accepted text whose meaning changes is a version-bump event;
// newly accepted syntax is additive. The canonical attribute form stays stable.
func registryVersionBumpEvents(before, after map[string]registryMeaning) []string {
	var changes []string
	for text, original := range before {
		if original.query != "" && original != after[text] {
			changes = append(changes, text)
		}
	}
	slices.Sort(changes)
	return changes
}

func TestRegistryGrowth(t *testing.T) {
	fixtures := readCorpus(t)
	fixtures = append(slices.Clone(fixtures),
		corpusFixture{Text: `spans where value>-3`},
		corpusFixture{Text: `spans where attributes['value']>-3`},
	)
	baseline := registrySnapshot(fixtures)
	columns, measures, functions := resolveColumns, resolveMeasures, resolveFilterFunctions
	oldBuckets := buckets
	t.Cleanup(func() {
		resolveColumns, resolveMeasures, resolveFilterFunctions, buckets = columns, measures, functions, oldBuckets
	})
	resolveMeasures = func() []panel.MeasureSpec {
		out := measures()
		out = append(out, panel.MeasureSpec{Name: "p42", Args: 1, Numeric: true, Quantile: .42, Signals: []string{"spans", "logs"}})
		return out
	}
	resolveFilterFunctions = func() []string { return append(functions(), "ltrim") }
	buckets = make(map[string]bool, len(oldBuckets)+1)
	for name, enabled := range oldBuckets {
		buckets[name] = enabled
	}
	buckets["2m"] = true
	afterAdditive := registrySnapshot(fixtures)
	if !reflect.DeepEqual(baseline, afterAdditive) {
		for text, before := range baseline {
			if before != afterAdditive[text] {
				// The corpus deliberately includes the previously unknown 2m
				// bucket. Making rejected syntax valid is additive (section 0.13).
				if text == "spans | count() every 2m" && before.query == "" && afterAdditive[text].query != "" {
					continue
				}
				t.Errorf("additive registry growth changed %q: %+v -> %+v", text, before, afterAdditive[text])
			}
		}
	}
	if events := registryVersionBumpEvents(baseline, afterAdditive); len(events) != 0 {
		t.Fatalf("additive changes requested a version bump: %q", events)
	}
	for _, text := range []string{`spans | p42(duration)`, `spans | count() every 2m`, `logs where ltrim(service)="api"`} {
		if c, ps := Compile(text, Options{}); len(ps) != 0 || c == nil {
			t.Fatalf("synthetic registry hook didn't reach %q: %+v", text, ps)
		}
	}
	resolveColumns = func(source string) ([]panel.Field, bool) {
		out, ok := columns(source)
		if source == "spans" {
			out = append(out, panel.Field{Name: "value", Type: panel.TypeNumber, Unit: "none"})
		}
		return out, ok
	}
	afterColumn := registrySnapshot(fixtures)
	events := registryVersionBumpEvents(baseline, afterColumn)
	if !slices.Contains(events, `spans where value>-3`) {
		t.Fatalf("new column must report a version-bump event; got %q", events)
	}
	if baseline[`spans where attributes['value']>-3`] != afterColumn[`spans where attributes['value']>-3`] {
		t.Fatal("new column changed canonical bracket meaning")
	}
	before := baseline[`spans where value>-3`].query
	after := afterColumn[`spans where value>-3`].query
	if before == after {
		t.Fatal("column hook did not change hand-typed bare text")
	}
	t.Logf("registry detector: adding spans.value changes accepted bare text (%d corpus version-bump events)", len(events))
}
