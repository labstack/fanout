package dashboard

import (
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/labstack/fanout/internal/panel"
)

func editBase() panel.Dashboard {
	return panel.Dashboard{Name: "Ops", Time: panel.Time{Range: "1h"}, Panels: []panel.Panel{
		{ID: "a", Title: "A", Viz: "text", Content: "a", Grid: &panel.Grid{W: 4, H: 3}},
		{ID: "b", Title: "B", Viz: "stat", Query: &panel.Query{From: "spans", Measures: []string{"count()"}}, Grid: &panel.Grid{X: 4, W: 3, H: 3}},
	}}
}

func TestApplyOperations(t *testing.T) {
	description := "New purpose"
	out, repack, err := Apply(editBase(), []Operation{
		{Op: "update_panel", ID: "b", Set: map[string]any{"title": "Requests", "thresholds": []any{map[string]any{"value": 10.0, "status": "warn"}}}},
		{Op: "add_panel", After: "a", Panel: &panel.Panel{ID: "c", Title: "C", Viz: "text", Content: "c"}},
		{Op: "move_panel", ID: "a"},
		{Op: "set_variable", Variable: &panel.Variable{Name: "service", Kind: "constant", Value: "checkout"}},
		{Op: "rename", Name: "Operations", Description: &description},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := out.Panels[0].ID + "," + out.Panels[1].ID + "," + out.Panels[2].ID; got != "a,c,b" {
		t.Fatalf("order = %s", got)
	}
	if out.Panels[2].Title != "Requests" || out.Panels[2].Thresholds[0].Status != "warn" || out.Panels[2].Query.Measures[0] != "count()" {
		t.Fatalf("update_panel = %+v", out.Panels[2])
	}
	if !repack || out.Name != "Operations" || out.Description != description || len(out.Variables) != 1 {
		t.Fatalf("repack %v spec %+v", repack, out)
	}
	if editBase().Panels[1].Title != "B" {
		t.Fatal("Apply mutated its input")
	}
}

func TestApplyUpdateWithoutResizeKeepsLayout(t *testing.T) {
	_, repack, err := Apply(editBase(), []Operation{{Op: "update_panel", ID: "b", Set: map[string]any{"title": "Requests"}}})
	if err != nil || repack {
		t.Fatalf("repack %v err %v", repack, err)
	}
	_, repack, _ = Apply(editBase(), []Operation{{Op: "update_panel", ID: "b", Set: map[string]any{"width": 6.0}}})
	if !repack {
		t.Fatal("a width change must repack")
	}
}

func TestApplyRejectsBadOperations(t *testing.T) {
	cases := [][]Operation{
		{{Op: "update_panel", ID: "missing", Set: map[string]any{"title": "x"}}},
		{{Op: "update_panel", ID: "b", Set: map[string]any{"id": "c"}}},
		{{Op: "update_panel", ID: "b", Set: map[string]any{"colour": "red"}}},
		{{Op: "add_panel", Panel: &panel.Panel{ID: "a", Title: "dup", Viz: "text", Content: "x"}}},
		{{Op: "remove_variable", Name: "nope"}},
		{{Op: "explode"}},
		{{Op: "update_panel", ID: "b", Set: map[string]any{"Title": "X"}}},
		{{Op: "update_panel", ID: "b", Set: map[string]any{"ID": "z"}}},
		{{Op: "update_panel", ID: "b", Set: map[string]any{"Grid": map[string]any{"w": 1.0}}}},
	}
	for _, ops := range cases {
		_, _, err := Apply(editBase(), ops)
		var problems panel.Problems
		if !errors.As(err, &problems) || problems[0].Path != "operations[0]" {
			t.Errorf("%+v: err = %v", ops, err)
		}
	}
}

func TestApplyBoundsWorkBeforeValidation(t *testing.T) {
	spec := DefaultSpec()
	ops := make([]Operation, 0, maxOperations+1)
	for i := range maxOperations + 1 {
		ops = append(ops, Operation{Op: "rename", Name: fmt.Sprintf("n%d", i)})
	}
	if _, _, err := Apply(spec, ops); err == nil || !strings.Contains(err.Error(), "at most 64 operations") {
		t.Fatalf("too many operations: err = %v", err)
	}
	adds := make([]Operation, 0, maxPanels)
	for i := range maxPanels {
		adds = append(adds, Operation{Op: "add_panel", Panel: &panel.Panel{ID: fmt.Sprintf("extra%d", i), Title: "x", Viz: "text", Content: "x"}})
	}
	adds = append(adds, Operation{Op: "remove_panel", ID: "extra0"})
	start := time.Now()
	_, _, err := Apply(spec, adds)
	if err == nil || !strings.Contains(err.Error(), "at most 40 panels") {
		t.Fatalf("panel cap: err = %v", err)
	}
	if elapsed := time.Since(start); elapsed > 100*time.Millisecond {
		t.Fatalf("Apply took %v before rejecting", elapsed)
	}
}
