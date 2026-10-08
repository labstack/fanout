package mcp

import (
	"github.com/labstack/fanout/internal/panel"
	"testing"
)

func TestFix1FragmentIdentityIncludesRelativeWindowAndVars(t *testing.T) {
	f := PanelFragment{Dashboard: panel.Dashboard{Name: "Map", Time: panel.Time{Range: "1h"}, Panels: []panel.Panel{{ID: "a", Title: "A", Viz: "service_map", Query: &panel.Query{From: "spans"}}}}, Results: []panel.Result{{ID: "a", FromMS: 1, ToMS: 2}}}
	original := fragmentIdentity(f)
	f.Dashboard.Name = "Another title"
	f.Dashboard.Panels[0].ID = "b"
	f.Dashboard.Panels[0].Title = "B"
	if original != fragmentIdentity(f) {
		t.Fatal("cosmetic fields prevented dedupe")
	}
	f.Results[0].FromMS = 3
	if original == fragmentIdentity(f) {
		t.Fatal("different captured windows deduped")
	}
	f.Results[0].FromMS = 1
	f.Vars = map[string]panel.Value{"service": panel.Value{Values: []string{"checkout"}}}
	if original == fragmentIdentity(f) {
		t.Fatal("different vars deduped")
	}
}

func TestFix1MapIdentityIgnoresLimitsOnlyForIdenticalCompleteProjections(t *testing.T) {
	f := PanelFragment{Dashboard: panel.Dashboard{Time: panel.Time{Range: "1h"}, Panels: []panel.Panel{{ID: "map", Title: "Map", Viz: "service_map", Query: &panel.Query{From: "spans", Limit: 20}}}}, Results: []panel.Result{{ID: "map", FromMS: 1, ToMS: 2, Frame: &panel.Frame{Columns: []panel.Column{{Name: "service", Type: "string", Role: "dimension"}}, Values: [][]any{{"checkout"}}, Rows: 1}}}}
	first := fragmentIdentity(f)
	f.Dashboard.Panels[0].Query.Limit = 400
	if first != fragmentIdentity(f) {
		t.Fatal("custom map and topology limits created duplicate complete maps")
	}
	f.Results[0].Frame.Values[0][0] = "payment"
	if first == fragmentIdentity(f) {
		t.Fatal("different maps deduped")
	}
}
