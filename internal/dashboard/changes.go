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
// once; authored fields and relative order of surviving panels get panel chips.
func Changes(before, after panel.Dashboard) ChangeSet {
	old := map[string]panel.Panel{}
	for _, p := range before.Panels {
		old[p.ID] = p
	}
	nextIDs := map[string]bool{}
	for _, p := range after.Panels {
		nextIDs[p.ID] = true
	}
	oldOrder := map[string]int{}
	nextOrder := map[string]int{}
	for _, p := range before.Panels {
		if nextIDs[p.ID] {
			oldOrder[p.ID] = len(oldOrder)
		}
	}
	for _, p := range after.Panels {
		if _, ok := old[p.ID]; ok {
			nextOrder[p.ID] = len(nextOrder)
		}
	}
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
		position := oldOrder[p.ID] != nextOrder[p.ID]
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
	return ChangeSet{Panels: out, LayoutChanged: layout, DashboardFields: changedFields(before, after)}
}

func mustJSON(v any) []byte {
	b, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return b
}
