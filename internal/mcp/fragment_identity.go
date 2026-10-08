package mcp

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"sort"
)

func fragmentView(fragment PanelFragment, kind string) FragmentView {
	return FragmentView{Kind: kind, Key: fragmentIdentity(fragment)}
}

// Titles and transport IDs do not make two projections different views. A
// custom service map followed by the topology preset gets one app activity;
// both tool results still reach the model. Scope, vars and rendering options
// remain part of the identity.
func fragmentIdentity(value any) string {
	raw, err := json.Marshal(value)
	if err != nil {
		return ""
	}
	var fragment PanelFragment
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	if err = decoder.Decode(&fragment); err != nil || len(fragment.Dashboard.Panels) == 0 {
		return ""
	}
	fragment.Dashboard.Name = ""
	var mapData any
	if len(fragment.Dashboard.Panels) == 1 && fragment.Dashboard.Panels[0].Viz == "service_map" && len(fragment.Results) == 1 && fragment.Results[0].Frame != nil && !fragment.Results[0].Frame.Truncated {
		// Different safe caps can return exactly the same complete graph. Require
		// identical projected data before treating those limits as equivalent.
		if query := fragment.Dashboard.Panels[0].Query; query != nil {
			query.Limit = 0
		}
		mapData = fragment.Results[0].Frame
	}
	for i := range fragment.Dashboard.Panels {
		p := &fragment.Dashboard.Panels[i]
		p.ID = ""
		p.Title = ""
		if p.Query != nil {
			sort.Strings(p.Query.Where)
		}
	}
	var vars any = fragment.Vars
	if fragment.Vars == nil {
		vars = map[string]any{}
	}
	var windows [][2]int64
	if fragment.Dashboard.Time.From == nil {
		for _, result := range fragment.Results {
			windows = append(windows, [2]int64{result.FromMS, result.ToMS})
		}
	}
	if fragment.Dashboard.Time.From != nil {
		at := fragment.Dashboard.Time.From.UTC()
		fragment.Dashboard.Time.From = &at
	}
	if fragment.Dashboard.Time.To != nil {
		at := fragment.Dashboard.Time.To.UTC()
		fragment.Dashboard.Time.To = &at
	}
	key, err := json.Marshal(struct {
		Dashboard any        `json:"dashboard"`
		Vars      any        `json:"vars"`
		Windows   [][2]int64 `json:"windows,omitempty"`
		MapData   any        `json:"map,omitempty"`
	}{fragment.Dashboard, vars, windows, mapData})
	if err != nil {
		return ""
	}
	hash := sha256.Sum256(key)
	return hex.EncodeToString(hash[:])
}
