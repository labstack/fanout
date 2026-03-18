package ai

import (
	"encoding/json"
	"fmt"
	"log/slog"
	"math"
)

// isFinite returns true if f is neither NaN nor +/-Inf.
func isFinite(f float64) bool {
	return !math.IsNaN(f) && !math.IsInf(f, 0)
}

// validateBlocks checks semantic invariants on blocks and returns only valid ones.
// Invalid blocks are dropped individually with a warning log.
func validateBlocks(blocks []Block) []Block {
	valid := make([]Block, 0, len(blocks))
	for _, b := range blocks {
		if err := validateBlock(b); err != nil {
			slog.Warn("dropping invalid block", "type", b.Type, "err", err)
			continue
		}
		valid = append(valid, b)
	}
	return valid
}

// validateBlock dispatches to per-type validators.
func validateBlock(b Block) error {
	switch b.Type {
	case BlockText:
		var d TextBlockData
		return unmarshalOnly(b.Data, &d)
	case BlockMetrics:
		return validateMetrics(b.Data)
	case BlockTable:
		return validateTable(b.Data)
	case BlockTimeseries:
		return validateTimeseries(b.Data)
	case BlockBar:
		return validateBar(b.Data)
	case BlockHeatmap:
		return validateHeatmap(b.Data)
	case BlockTopology:
		return validateTopology(b.Data)
	case BlockSankey:
		return validateSankey(b.Data)
	case BlockTraceWaterfall:
		return validateTraceWaterfall(b.Data)
	case BlockCorrelation:
		return validateCorrelation(b.Data)
	case BlockFlameGraph:
		var d FlameGraphData
		return unmarshalOnly(b.Data, &d)
	case BlockDepMatrix:
		var d DepMatrixData
		return unmarshalOnly(b.Data, &d)
	case BlockEndpoints:
		var d EndpointsData
		return unmarshalOnly(b.Data, &d)
	case BlockLogs:
		var d LogsBlockData
		return unmarshalOnly(b.Data, &d)
	case BlockComparison:
		var d ComparisonData
		return unmarshalOnly(b.Data, &d)
	default:
		return fmt.Errorf("unknown block type %q", b.Type)
	}
}

// unmarshalOnly verifies that the JSON can be unmarshaled into dst.
func unmarshalOnly(data json.RawMessage, dst any) error {
	return json.Unmarshal(data, dst)
}

// validateMetrics checks that items are non-empty and all values are finite.
func validateMetrics(data json.RawMessage) error {
	var d MetricsBlockData
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	if len(d.Items) == 0 {
		return fmt.Errorf("metrics block has no items")
	}
	for i, item := range d.Items {
		if !isFinite(item.Value) {
			return fmt.Errorf("metrics item %d (%s): non-finite value %v", i, item.Label, item.Value)
		}
	}
	return nil
}

// validateTable checks that columns and rows are non-empty.
func validateTable(data json.RawMessage) error {
	var d TableBlockData
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	if len(d.Columns) == 0 {
		return fmt.Errorf("table block has no columns")
	}
	if len(d.Rows) == 0 {
		return fmt.Errorf("table block has no rows")
	}
	return nil
}

// validateTimeseries checks labels and series lengths match and all values are finite.
func validateTimeseries(data json.RawMessage) error {
	var d TimeseriesBlockData
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	if len(d.Labels) == 0 {
		return fmt.Errorf("timeseries block has no labels")
	}
	if len(d.Series) == 0 {
		return fmt.Errorf("timeseries block has no series")
	}
	for i, s := range d.Series {
		if len(s.Values) != len(d.Labels) {
			return fmt.Errorf("timeseries series %d (%s): values length %d != labels length %d",
				i, s.Label, len(s.Values), len(d.Labels))
		}
		for j, v := range s.Values {
			if !isFinite(v) {
				return fmt.Errorf("timeseries series %d value %d: non-finite %v", i, j, v)
			}
		}
	}
	return nil
}

// validateBar checks that bars are non-empty and all values are finite.
func validateBar(data json.RawMessage) error {
	var d BarBlockData
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	if len(d.Bars) == 0 {
		return fmt.Errorf("bar block has no bars")
	}
	for i, bar := range d.Bars {
		if !isFinite(bar.Value) {
			return fmt.Errorf("bar item %d (%s): non-finite value %v", i, bar.Label, bar.Value)
		}
	}
	return nil
}

// validateHeatmap checks dimension alignment and finite values.
func validateHeatmap(data json.RawMessage) error {
	var d HeatmapBlockData
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	if len(d.Values) != len(d.Times) {
		return fmt.Errorf("heatmap: values rows %d != times length %d", len(d.Values), len(d.Times))
	}
	for i, row := range d.Values {
		if len(row) != len(d.Buckets) {
			return fmt.Errorf("heatmap: values[%d] length %d != buckets length %d", i, len(row), len(d.Buckets))
		}
		for j, v := range row {
			if !isFinite(v) {
				return fmt.Errorf("heatmap: values[%d][%d] non-finite %v", i, j, v)
			}
		}
	}
	return nil
}

// validateTopology checks that nodes are non-empty and all edges reference existing node IDs.
func validateTopology(data json.RawMessage) error {
	var d TopologyData
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	if len(d.Nodes) == 0 {
		return fmt.Errorf("topology block has no nodes")
	}
	ids := make(map[string]struct{}, len(d.Nodes))
	for _, n := range d.Nodes {
		ids[n.ID] = struct{}{}
	}
	for i, e := range d.Edges {
		if _, ok := ids[e.Source]; !ok {
			return fmt.Errorf("topology edge %d: source %q not in nodes", i, e.Source)
		}
		if _, ok := ids[e.Target]; !ok {
			return fmt.Errorf("topology edge %d: target %q not in nodes", i, e.Target)
		}
	}
	return nil
}

// validateSankey checks that nodes are non-empty and all links reference existing node IDs.
func validateSankey(data json.RawMessage) error {
	var d SankeyData
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	if len(d.Nodes) == 0 {
		return fmt.Errorf("sankey block has no nodes")
	}
	ids := make(map[string]struct{}, len(d.Nodes))
	for _, n := range d.Nodes {
		ids[n.ID] = struct{}{}
	}
	for i, l := range d.Links {
		if _, ok := ids[l.Source]; !ok {
			return fmt.Errorf("sankey link %d: source %q not in nodes", i, l.Source)
		}
		if _, ok := ids[l.Target]; !ok {
			return fmt.Errorf("sankey link %d: target %q not in nodes", i, l.Target)
		}
	}
	return nil
}

// validateTraceWaterfall checks that spans are non-empty and all non-nil parent refs exist.
func validateTraceWaterfall(data json.RawMessage) error {
	var d TraceWaterfallData
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	if len(d.Spans) == 0 {
		return fmt.Errorf("trace_waterfall block has no spans")
	}
	ids := make(map[string]struct{}, len(d.Spans))
	for _, s := range d.Spans {
		ids[s.ID] = struct{}{}
	}
	for i, s := range d.Spans {
		if s.Parent != nil {
			if _, ok := ids[*s.Parent]; !ok {
				return fmt.Errorf("trace_waterfall span %d (%s): parent %q not found", i, s.ID, *s.Parent)
			}
		}
	}
	return nil
}

// validateCorrelation checks times/panels alignment.
func validateCorrelation(data json.RawMessage) error {
	var d CorrelationData
	if err := json.Unmarshal(data, &d); err != nil {
		return err
	}
	if len(d.Times) == 0 {
		return fmt.Errorf("correlation block has no times")
	}
	if len(d.Panels) == 0 {
		return fmt.Errorf("correlation block has no panels")
	}
	for i, p := range d.Panels {
		if len(p.Values) != len(d.Times) {
			return fmt.Errorf("correlation panel %d (%s): values length %d != times length %d",
				i, p.Label, len(p.Values), len(d.Times))
		}
	}
	return nil
}
