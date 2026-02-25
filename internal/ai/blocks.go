package ai

//go:generate sh -c "go run ../../cmd/genblocks > ../../client/src/lib/types.ts"

import "encoding/json"

// BlockType identifies the kind of block for the client to render.
type BlockType string

const (
	BlockText           BlockType = "text"
	BlockMetrics        BlockType = "metrics"
	BlockTable          BlockType = "table"
	BlockTimeseries     BlockType = "timeseries"
	BlockBar            BlockType = "bar"
	BlockHeatmap        BlockType = "heatmap"
	BlockTraceWaterfall BlockType = "trace_waterfall"
	BlockTopology       BlockType = "topology"
	BlockFlameGraph     BlockType = "flame_graph"
	BlockSankey         BlockType = "sankey"
	BlockDepMatrix      BlockType = "dep_matrix"
	BlockEndpoints      BlockType = "endpoints"
	BlockCorrelation    BlockType = "correlation"
	BlockTail           BlockType = "tail"
)

// Block is a typed data unit sent to the client for rendering.
type Block struct {
	Type BlockType       `json:"type"`
	Data json.RawMessage `json:"data"`
}

// NewBlock creates a Block by marshaling the data to JSON.
func NewBlock(t BlockType, data any) Block {
	b, _ := json.Marshal(data)
	return Block{Type: t, Data: b}
}

// ---------------------------------------------------------------------------
// Data structs for each block type. JSON tags match the TypeScript interfaces
// in client/src/lib/types.ts exactly.
// ---------------------------------------------------------------------------

// TextBlockData is markdown content.
type TextBlockData struct {
	Content string `json:"content"`
}

// MetricItem is a single metric value with label, unit and status indicator.
type MetricItem struct {
	Label  string  `json:"label"`
	Value  float64 `json:"value"`
	Unit   string  `json:"unit"`
	Status string  `json:"status"` // "ok", "warning", "danger"
}

// MetricsBlockData holds a grid of metric items.
type MetricsBlockData struct {
	Items []MetricItem `json:"items"`
}

// TableColumn defines a column in a table block.
type TableColumn struct {
	Key   string `json:"key"`
	Label string `json:"label"`
	Align string `json:"align,omitempty"` // "left", "right", "center"
}

// TableBlockData holds columnar data rendered as a table.
type TableBlockData struct {
	Columns []TableColumn    `json:"columns"`
	Rows    []map[string]any `json:"rows"`
}

// TimeseriesSeries is a single data series within a timeseries chart.
type TimeseriesSeries struct {
	Label  string    `json:"label"`
	Color  string    `json:"color,omitempty"`
	Values []float64 `json:"values"`
}

// TimeseriesBlockData holds multi-series time-aligned data.
type TimeseriesBlockData struct {
	Title  string             `json:"title"`
	Series []TimeseriesSeries `json:"series"`
	Labels []string           `json:"labels"`
	YLabel string             `json:"yLabel,omitempty"`
}

// BarItem is a single bar in a bar chart.
type BarItem struct {
	Label string  `json:"label"`
	Value float64 `json:"value"`
	Color string  `json:"color,omitempty"`
}

// BarBlockData holds a bar chart with optional horizontal layout.
type BarBlockData struct {
	Title      string    `json:"title"`
	Bars       []BarItem `json:"bars"`
	YLabel     string    `json:"yLabel,omitempty"`
	Horizontal bool      `json:"horizontal,omitempty"`
}

// HeatmapBlockData holds a latency heatmap with time x bucket dimensions.
type HeatmapBlockData struct {
	Title   string      `json:"title"`
	Buckets []float64   `json:"buckets"`
	Times   []string    `json:"times"`
	Values  [][]float64 `json:"values"`
}

// TraceSpan is a single span in a trace waterfall.
type TraceSpan struct {
	ID        string  `json:"id"`
	Parent    *string `json:"parent"` // null for root
	Service   string  `json:"service"`
	Operation string  `json:"operation"`
	Start     float64 `json:"start"`
	Duration  float64 `json:"duration"`
	Status    string  `json:"status"`
}

// TraceWaterfallData holds spans for a distributed trace waterfall view.
type TraceWaterfallData struct {
	Spans []TraceSpan `json:"spans"`
}

// TopologyNode is a service node in the dependency graph.
type TopologyNode struct {
	ID     string  `json:"id"`
	Status string  `json:"status"`
	RPM    float64 `json:"rpm"`
	P95    float64 `json:"p95"`
	Errors float64 `json:"errors"`
}

// TopologyEdge is a dependency between two services.
type TopologyEdge struct {
	Source    string  `json:"source"`
	Target    string  `json:"target"`
	RPM       float64 `json:"rpm"`
	ErrorRate float64 `json:"errorRate"`
}

// TopologyData holds the service dependency graph.
type TopologyData struct {
	Nodes []TopologyNode `json:"nodes"`
	Edges []TopologyEdge `json:"edges"`
}

// FlameFrame is a single frame in a flame graph.
type FlameFrame struct {
	Name    string  `json:"name"`
	Depth   int     `json:"depth"`
	X       float64 `json:"x"`
	W       float64 `json:"w"`
	Self    float64 `json:"self"`
	Total   float64 `json:"total"`
	Service string  `json:"service"`
}

// FlameGraphData holds frames for a flame graph visualization.
type FlameGraphData struct {
	Frames []FlameFrame `json:"frames"`
}

// SankeyNode is a node in a Sankey flow diagram.
type SankeyNode struct {
	ID     string  `json:"id"`
	Label  string  `json:"label"`
	RPM    float64 `json:"rpm"`
	Status string  `json:"status,omitempty"`
}

// SankeyLink is a flow between two Sankey nodes.
type SankeyLink struct {
	Source string  `json:"source"`
	Target string  `json:"target"`
	Value  float64 `json:"value"`
}

// SankeyData holds nodes and links for a Sankey flow diagram.
type SankeyData struct {
	Nodes []SankeyNode `json:"nodes"`
	Links []SankeyLink `json:"links"`
}

// DepMatrixCell holds the relationship metrics between two services.
type DepMatrixCell struct {
	From      string  `json:"from"`
	To        string  `json:"to"`
	ErrorRate float64 `json:"errorRate"`
	RPM       float64 `json:"rpm"`
	P95       float64 `json:"p95"`
}

// DepMatrixData holds a dependency matrix of inter-service relationships.
type DepMatrixData struct {
	Services []string        `json:"services"`
	Cells    []DepMatrixCell `json:"cells"`
}

// EndpointInfo holds per-endpoint performance data.
type EndpointInfo struct {
	Method    string  `json:"method"`
	Path      string  `json:"path"`
	RPM       float64 `json:"rpm"`
	P50       float64 `json:"p50"`
	P95       float64 `json:"p95"`
	P99       float64 `json:"p99"`
	ErrorRate float64 `json:"errorRate"`
	Status    string  `json:"status"`
}

// EndpointsData holds a list of endpoint breakdowns.
type EndpointsData struct {
	Endpoints []EndpointInfo `json:"endpoints"`
}

// CorrelationMarker highlights a point of interest on a correlation panel.
type CorrelationMarker struct {
	T        string `json:"t"`
	Label    string `json:"label"`
	Severity string `json:"severity"`
}

// CorrelationPanel is a single metric panel in a correlation view.
type CorrelationPanel struct {
	Label    string              `json:"label"`
	Color    string              `json:"color"`
	Values   []float64           `json:"values"`
	Baseline *float64            `json:"baseline,omitempty"`
	Markers  []CorrelationMarker `json:"markers,omitempty"`
}

// CorrelationData holds time-aligned panels for cross-signal correlation.
type CorrelationData struct {
	Times  []string           `json:"times"`
	Panels []CorrelationPanel `json:"panels"`
}

// LogEntry is a single log line in a tail block.
type LogEntry struct {
	Time     int64  `json:"time"`
	Severity string `json:"severity"`
	Service  string `json:"service"`
	Body     string `json:"body"`
	TraceID  string `json:"traceId,omitempty"`
}

// TailData holds live log entries.
type TailData struct {
	Entries []LogEntry `json:"entries"`
}

// ---------------------------------------------------------------------------
// Convenience constructors for commonly used block types.
// ---------------------------------------------------------------------------

// MakeTextBlock creates a text/markdown block.
func MakeTextBlock(content string) Block {
	return NewBlock(BlockText, TextBlockData{Content: content})
}

// MakeMetricsBlock creates a metrics grid block.
func MakeMetricsBlock(items []MetricItem) Block {
	return NewBlock(BlockMetrics, MetricsBlockData{Items: items})
}

// MakeTableBlock creates a table block from columns and rows.
func MakeTableBlock(columns []TableColumn, rows []map[string]any) Block {
	return NewBlock(BlockTable, TableBlockData{Columns: columns, Rows: rows})
}
