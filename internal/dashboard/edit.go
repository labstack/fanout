package dashboard

import (
	"bytes"
	"encoding/json"
	"fmt"
	"reflect"
	"slices"
	"strings"

	"github.com/labstack/fanout/internal/panel"
)

// Operation is one typed edit. Panels are addressed by their stable id, never
// by position, so an edit lands on exactly the panel it names.
type Operation struct {
	Op          string          `json:"op" jsonschema:"add_panel, update_panel, remove_panel, move_panel, set_variable, remove_variable, set_time or rename"`
	ID          string          `json:"id,omitempty" jsonschema:"update_panel, remove_panel, move_panel: the panel id"`
	After       string          `json:"after,omitempty" jsonschema:"add_panel: insert after this panel id (default: last); move_panel: move after this panel id (default: first)"`
	Panel       *panel.Panel    `json:"panel,omitempty" jsonschema:"add_panel: the complete new panel"`
	Set         map[string]any  `json:"set,omitempty" jsonschema:"update_panel: panel fields to replace, e.g. {\"title\": \"…\", \"thresholds\": […]}; null removes a field"`
	Variable    *panel.Variable `json:"variable,omitempty" jsonschema:"set_variable: the variable, added or replaced by name"`
	Name        string          `json:"name,omitempty" jsonschema:"remove_variable: the variable name; rename: the new dashboard name"`
	Description *string         `json:"description,omitempty" jsonschema:"rename: the new description"`
	Time        *panel.Time     `json:"time,omitempty" jsonschema:"set_time: the new default time"`
}

// maxOperations and maxPanels bound the work one edit can request before the
// spec is validated: packing an oversized panel list grows faster than
// linearly. maxPanels matches the validator's 40-panel limit.
const (
	maxOperations = 64
	maxPanels     = 40
)

func opError(i int, format string, args ...any) error {
	return panel.Problems{{Path: fmt.Sprintf("operations[%d]", i), Message: fmt.Sprintf(format, args...)}}
}

// Apply runs operations in order on a copy of spec. The bool reports whether
// panels were moved or resized, which requires a fresh pack. Additions keep
// existing coordinates and receive their own placement in prepare.
func Apply(spec panel.Dashboard, ops []Operation) (panel.Dashboard, bool, error) {
	out, err := clone(spec)
	if err != nil {
		return panel.Dashboard{}, false, err
	}
	if len(ops) > maxOperations {
		return panel.Dashboard{}, false, panel.Problems{{Path: "operations", Message: fmt.Sprintf("an edit has at most %d operations", maxOperations)}}
	}
	repack, compact := false, false
	index := func(id string) int {
		return slices.IndexFunc(out.Panels, func(p panel.Panel) bool { return p.ID == id })
	}
	for i, op := range ops {
		switch op.Op {
		case "add_panel":
			if op.Panel == nil {
				return panel.Dashboard{}, false, opError(i, "add_panel needs panel")
			}
			if index(op.Panel.ID) >= 0 {
				return panel.Dashboard{}, false, opError(i, "a panel with id %q already exists", op.Panel.ID)
			}
			if len(out.Panels) >= maxPanels {
				return panel.Dashboard{}, false, opError(i, "a dashboard has at most %d panels", maxPanels)
			}
			p := *op.Panel
			p.Grid = nil
			at := len(out.Panels)
			if op.After != "" {
				j := index(op.After)
				if j < 0 {
					return panel.Dashboard{}, false, opError(i, "no panel has id %q", op.After)
				}
				at = j + 1
			}
			out.Panels = slices.Insert(out.Panels, at, p)
		case "update_panel":
			j := index(op.ID)
			if j < 0 {
				return panel.Dashboard{}, false, opError(i, "no panel has id %q", op.ID)
			}
			for key := range op.Set {
				if !panelFields[key] {
					return panel.Dashboard{}, false, opError(i, "unknown panel field %q", key)
				}
			}
			for _, fixed := range []string{"id", "grid"} {
				if _, ok := op.Set[fixed]; ok {
					return panel.Dashboard{}, false, opError(i, "%s cannot be set; it is managed by the server", fixed)
				}
			}
			updated, err := mergePanel(out.Panels[j], op.Set)
			if err != nil {
				return panel.Dashboard{}, false, opError(i, "%v", err)
			}
			_, width := op.Set["width"]
			_, height := op.Set["height"]
			repack = repack || width || height
			out.Panels[j] = updated
		case "remove_panel":
			j := index(op.ID)
			if j < 0 {
				return panel.Dashboard{}, false, opError(i, "no panel has id %q", op.ID)
			}
			out.Panels = slices.Delete(out.Panels, j, j+1)
			compact = true
		case "move_panel":
			j := index(op.ID)
			if j < 0 {
				return panel.Dashboard{}, false, opError(i, "no panel has id %q", op.ID)
			}
			p := out.Panels[j]
			out.Panels = slices.Delete(out.Panels, j, j+1)
			at := 0
			if op.After != "" {
				k := index(op.After)
				if k < 0 {
					return panel.Dashboard{}, false, opError(i, "no panel has id %q", op.After)
				}
				at = k + 1
			}
			out.Panels = slices.Insert(out.Panels, at, p)
			repack = true
		case "set_variable":
			if op.Variable == nil {
				return panel.Dashboard{}, false, opError(i, "set_variable needs variable")
			}
			k := slices.IndexFunc(out.Variables, func(v panel.Variable) bool { return v.Name == op.Variable.Name })
			if k >= 0 {
				out.Variables[k] = *op.Variable
			} else {
				out.Variables = append(out.Variables, *op.Variable)
			}
		case "remove_variable":
			k := slices.IndexFunc(out.Variables, func(v panel.Variable) bool { return v.Name == op.Name })
			if k < 0 {
				return panel.Dashboard{}, false, opError(i, "no variable is named %q", op.Name)
			}
			out.Variables = slices.Delete(out.Variables, k, k+1)
		case "set_time":
			if op.Time == nil {
				return panel.Dashboard{}, false, opError(i, "set_time needs time")
			}
			out.Time = *op.Time
		case "rename":
			if op.Name == "" && op.Description == nil {
				return panel.Dashboard{}, false, opError(i, "rename needs name or description")
			}
			if op.Name != "" {
				out.Name = op.Name
			}
			if op.Description != nil {
				out.Description = *op.Description
			}
		default:
			return panel.Dashboard{}, false, opError(i, "unknown op %q; use add_panel, update_panel, remove_panel, move_panel, set_variable, remove_variable, set_time or rename", op.Op)
		}
	}
	if compact && !repack {
		panel.Normalize(&out)
		Compact(out.Panels)
	}
	return out, repack, nil
}

func clone(spec panel.Dashboard) (panel.Dashboard, error) {
	raw, err := json.Marshal(spec)
	if err != nil {
		return panel.Dashboard{}, err
	}
	var out panel.Dashboard
	err = json.Unmarshal(raw, &out)
	return out, err
}

// mergePanel replaces the fields named in set and decodes strictly, so a
// misspelled field is an error rather than a silent no-op.
func mergePanel(p panel.Panel, set map[string]any) (panel.Panel, error) {
	raw, err := json.Marshal(p)
	if err != nil {
		return panel.Panel{}, err
	}
	fields := map[string]any{}
	if err := json.Unmarshal(raw, &fields); err != nil {
		return panel.Panel{}, err
	}
	for key, value := range set {
		if value == nil {
			delete(fields, key)
		} else {
			fields[key] = value
		}
	}
	merged, err := json.Marshal(fields)
	if err != nil {
		return panel.Panel{}, err
	}
	decoder := json.NewDecoder(bytes.NewReader(merged))
	decoder.DisallowUnknownFields()
	var out panel.Panel
	if err := decoder.Decode(&out); err != nil {
		return panel.Panel{}, err
	}
	return out, nil
}

// panelFields is the exact set of Panel JSON field names; encoding/json would
// otherwise match set keys case-insensitively and silently ignore the edit.
var panelFields = func() map[string]bool {
	fields := map[string]bool{}
	t := reflect.TypeOf(panel.Panel{})
	for i := 0; i < t.NumField(); i++ {
		name, _, _ := strings.Cut(t.Field(i).Tag.Get("json"), ",")
		if name != "" && name != "-" {
			fields[name] = true
		}
	}
	return fields
}()
