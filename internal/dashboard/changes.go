package dashboard

import (
	"bytes"
	"encoding/json"
	"sort"

	"github.com/labstack/fanout/internal/panel"
)

type PanelChange struct {
	PanelID         string   `json:"panel_id"`
	Title           string   `json:"title"`
	Kind            string   `json:"kind"`
	Fields          []string `json:"fields,omitempty"`
	PositionChanged bool     `json:"position_changed,omitempty"`
}

func changedFields(a, b any) []string {
	rawA, _ := json.Marshal(a)
	rawB, _ := json.Marshal(b)
	var x, y map[string]json.RawMessage
	_ = json.Unmarshal(rawA, &x)
	_ = json.Unmarshal(rawB, &y)
	keys := map[string]bool{}
	for k := range x {
		keys[k] = true
	}
	for k := range y {
		keys[k] = true
	}
	fields := []string{}
	for k := range keys {
		if !bytes.Equal(x[k], y[k]) {
			fields = append(fields, k)
		}
	}
	sort.Strings(fields)
	return fields
}

type ChangeSet struct {
	Panels          []PanelChange `json:"panels"`
	LayoutChanged   bool          `json:"layout_changed"`
	DashboardFields []string      `json:"dashboard_fields,omitempty"`
}

// Changes compares normalized committed snapshots. Physical packing is reported
// once; authored fields and only panels outside the longest common surviving
// order get panel chips. Normalization applies to copies for diffing only.
func Changes(before, after panel.Dashboard) ChangeSet {
	// The zero base is a create: only added panel chips apply, for every consumer.
	created := before.Version == 0 && before.Name == "" && len(before.Panels) == 0
	before = normalizedForDiff(before)
	after = normalizedForDiff(after)
	old := map[string]panel.Panel{}
	for _, p := range before.Panels {
		old[p.ID] = p
	}
	nextIDs := map[string]bool{}
	for _, p := range after.Panels {
		nextIDs[p.ID] = true
	}
	beforeOrder, afterOrder := []string{}, []string{}
	for _, p := range before.Panels {
		if nextIDs[p.ID] {
			beforeOrder = append(beforeOrder, p.ID)
		}
	}
	for _, p := range after.Panels {
		if _, ok := old[p.ID]; ok {
			afterOrder = append(afterOrder, p.ID)
		}
	}
	retained := retainedOrder(beforeOrder, afterOrder)
	seen := map[string]bool{}
	out := []PanelChange{}
	layout := false
	for _, p := range after.Panels {
		seen[p.ID] = true
		prior, exists := old[p.ID]
		if !exists {
			layout = layout || p.Grid != nil
			out = append(out, PanelChange{PanelID: p.ID, Title: p.Title, Kind: "added"})
			continue
		}
		if !bytes.Equal(mustJSON(prior.Grid), mustJSON(p.Grid)) {
			layout = true
		}
		prior.Grid = nil
		p.Grid = nil
		fields := changedFields(prior, p)
		position := !retained[p.ID]
		if len(fields) > 0 || position {
			out = append(out, PanelChange{PanelID: p.ID, Title: p.Title, Kind: "changed", Fields: fields, PositionChanged: position})
		}
	}
	for _, p := range before.Panels {
		if !seen[p.ID] {
			layout = layout || p.Grid != nil
			out = append(out, PanelChange{PanelID: p.ID, Title: p.Title, Kind: "removed"})
		}
	}
	before.Panels = nil
	after.Panels = nil
	fields := []string{}
	for _, field := range changedFields(before, after) {
		if field != "version" {
			fields = append(fields, field)
		}
	}
	if created {
		layout = false
		fields = nil
	}
	return ChangeSet{Panels: out, LayoutChanged: layout, DashboardFields: fields}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}

func normalizedForDiff(spec panel.Dashboard) panel.Dashboard {
	copy, err := clone(spec)
	if err != nil {
		panic(err)
	}
	panel.Normalize(&copy)
	return copy
}

// On equal-length choices retain the earlier after-index. This picks the
// same minimal moved set deterministically, including an ambiguous two-panel swap.
func retainedOrder(before, after []string) map[string]bool {
	lengths := make([][]int, len(before)+1)
	for i := range lengths {
		lengths[i] = make([]int, len(after)+1)
	}
	for i := len(before) - 1; i >= 0; i-- {
		for j := len(after) - 1; j >= 0; j-- {
			if before[i] == after[j] {
				lengths[i][j] = 1 + lengths[i+1][j+1]
			} else {
				lengths[i][j] = max(lengths[i+1][j], lengths[i][j+1])
			}
		}
	}
	retained := map[string]bool{}
	for i, j := 0, 0; i < len(before) && j < len(after); {
		if before[i] == after[j] {
			retained[before[i]] = true
			i++
			j++
		} else if lengths[i+1][j] >= lengths[i][j+1] {
			i++
		} else {
			j++
		}
	}
	return retained
}
