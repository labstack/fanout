package ai

import (
	"encoding/json"
	"math"
	"testing"
)

func TestValidateBlocks_DropsEmpty(t *testing.T) {
	blocks := []Block{
		NewBlock(BlockMetrics, MetricsBlockData{Items: nil}),
		NewBlock(BlockMetrics, MetricsBlockData{Items: []MetricItem{{Label: "ok", Value: 1, Unit: "ms", Status: "ok"}}}),
	}
	valid := validateBlocks(blocks)
	if len(valid) != 1 {
		t.Errorf("got %d blocks, want 1 (empty dropped)", len(valid))
	}
}

func TestValidateBlocks_DropsNaN(t *testing.T) {
	blocks := []Block{
		NewBlock(BlockMetrics, MetricsBlockData{Items: []MetricItem{
			{Label: "bad", Value: math.NaN(), Unit: "ms", Status: "ok"},
		}}),
	}
	valid := validateBlocks(blocks)
	// NewBlock returns a text fallback when marshal fails (NaN can't be marshaled)
	if len(valid) != 1 || valid[0].Type != BlockText {
		t.Errorf("got %d blocks (type=%v), want 1 text fallback block", len(valid), func() BlockType {
			if len(valid) > 0 {
				return valid[0].Type
			}
			return ""
		}())
	}
}

func TestValidateBlocks_DropsInfinity(t *testing.T) {
	blocks := []Block{
		NewBlock(BlockMetrics, MetricsBlockData{Items: []MetricItem{
			{Label: "bad", Value: math.Inf(1), Unit: "ms", Status: "ok"},
		}}),
	}
	valid := validateBlocks(blocks)
	// NewBlock returns a text fallback when marshal fails (Inf can't be marshaled)
	if len(valid) != 1 || valid[0].Type != BlockText {
		t.Errorf("got %d blocks (type=%v), want 1 text fallback block", len(valid), func() BlockType {
			if len(valid) > 0 {
				return valid[0].Type
			}
			return ""
		}())
	}
}

func TestValidateBlocks_DropsTimeseriesMismatch(t *testing.T) {
	blocks := []Block{
		NewBlock(BlockTimeseries, TimeseriesBlockData{
			Title:  "test",
			Labels: []string{"a", "b"},
			Series: []TimeseriesSeries{
				{Label: "s1", Values: []float64{1}}, // length mismatch
			},
		}),
	}
	valid := validateBlocks(blocks)
	if len(valid) != 0 {
		t.Errorf("got %d blocks, want 0 (length mismatch dropped)", len(valid))
	}
}

func TestValidateBlocks_KeepsValidTimeseries(t *testing.T) {
	blocks := []Block{
		NewBlock(BlockTimeseries, TimeseriesBlockData{
			Title:  "test",
			Labels: []string{"a", "b"},
			Series: []TimeseriesSeries{
				{Label: "s1", Values: []float64{1, 2}},
			},
		}),
	}
	valid := validateBlocks(blocks)
	if len(valid) != 1 {
		t.Errorf("got %d blocks, want 1", len(valid))
	}
}

func TestValidateBlocks_DropsTopologyBadEdge(t *testing.T) {
	blocks := []Block{
		NewBlock(BlockTopology, TopologyData{
			Nodes: []TopologyNode{{ID: "a", Status: "ok"}},
			Edges: []TopologyEdge{{Source: "a", Target: "nonexistent"}},
		}),
	}
	valid := validateBlocks(blocks)
	if len(valid) != 0 {
		t.Errorf("got %d blocks, want 0 (bad edge dropped)", len(valid))
	}
}

func TestValidateBlocks_KeepsValidTopology(t *testing.T) {
	blocks := []Block{
		NewBlock(BlockTopology, TopologyData{
			Nodes: []TopologyNode{{ID: "a", Status: "ok"}, {ID: "b", Status: "ok"}},
			Edges: []TopologyEdge{{Source: "a", Target: "b"}},
		}),
	}
	valid := validateBlocks(blocks)
	if len(valid) != 1 {
		t.Errorf("got %d blocks, want 1", len(valid))
	}
}

func TestValidateBlocks_TextAlwaysValid(t *testing.T) {
	blocks := []Block{MakeTextBlock("hello")}
	valid := validateBlocks(blocks)
	if len(valid) != 1 {
		t.Errorf("got %d blocks, want 1", len(valid))
	}
}

func TestValidateBlocks_UnmarshalFailure(t *testing.T) {
	blocks := []Block{{Type: BlockMetrics, Data: json.RawMessage(`invalid`)}}
	valid := validateBlocks(blocks)
	if len(valid) != 0 {
		t.Errorf("got %d blocks, want 0 (unmarshal fail)", len(valid))
	}
}

func TestValidateBlocks_DropsSankeyBadLink(t *testing.T) {
	blocks := []Block{
		NewBlock(BlockSankey, SankeyData{
			Nodes: []SankeyNode{{ID: "a", Label: "A", RPM: 100}},
			Links: []SankeyLink{{Source: "a", Target: "missing", Value: 10}},
		}),
	}
	valid := validateBlocks(blocks)
	if len(valid) != 0 {
		t.Errorf("got %d blocks, want 0 (bad link dropped)", len(valid))
	}
}

func TestValidateBlocks_DropsCorrelationMismatch(t *testing.T) {
	blocks := []Block{
		NewBlock(BlockCorrelation, CorrelationData{
			Times:  []string{"a", "b", "c"},
			Panels: []CorrelationPanel{{Label: "p1", Color: "#f00", Values: []float64{1, 2}}}, // 2 != 3
		}),
	}
	valid := validateBlocks(blocks)
	if len(valid) != 0 {
		t.Errorf("got %d blocks, want 0 (correlation mismatch dropped)", len(valid))
	}
}

func TestValidateBlocks_DropsHeatmapMismatch(t *testing.T) {
	blocks := []Block{
		NewBlock(BlockHeatmap, HeatmapBlockData{
			Title:   "test",
			Buckets: []float64{10, 50, 100},
			Times:   []string{"t1", "t2"},
			Values:  [][]float64{{1, 2}}, // only 1 row, but 2 times expected
		}),
	}
	valid := validateBlocks(blocks)
	if len(valid) != 0 {
		t.Errorf("got %d blocks, want 0 (heatmap mismatch dropped)", len(valid))
	}
}

func TestValidateBlocks_DropsTraceWaterfallBadParent(t *testing.T) {
	parent := "missing"
	blocks := []Block{
		NewBlock(BlockTraceWaterfall, TraceWaterfallData{
			Spans: []TraceSpan{
				{ID: "a", Parent: nil, Service: "svc", Operation: "op", Start: 0, Duration: 10, Status: "ok"},
				{ID: "b", Parent: &parent, Service: "svc", Operation: "op", Start: 1, Duration: 5, Status: "ok"},
			},
		}),
	}
	valid := validateBlocks(blocks)
	if len(valid) != 0 {
		t.Errorf("got %d blocks, want 0 (bad parent ref dropped)", len(valid))
	}
}
