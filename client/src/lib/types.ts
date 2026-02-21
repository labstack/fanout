// Chat event types (WebSocket protocol)
export interface ChatEvent {
  type: "token" | "tool_call" | "tool_result" | "error" | "done";
  content?: string;
  name?: string;
  input?: Record<string, unknown>;
  error?: string;
  id?: string;
  blocks?: Block[];
}

// Block spec
export interface Block {
  type: BlockType;
  data: unknown;
}

export type BlockType =
  | "text"
  | "metrics"
  | "table"
  | "timeseries"
  | "bar"
  | "heatmap"
  | "trace_waterfall"
  | "topology"
  | "flame_graph"
  | "sankey"
  | "dep_matrix"
  | "endpoints"
  | "correlation"
  | "tail";

// Data interfaces for each block type
export interface TextBlockData {
  content: string;
}

export interface MetricsBlockData {
  items: MetricItem[];
}

export interface MetricItem {
  label: string;
  value: number;
  unit: string;
  status: "ok" | "warning" | "danger";
}

export interface TableBlockData {
  columns: TableColumn[];
  rows: Record<string, unknown>[];
}

export interface TableColumn {
  key: string;
  label: string;
  align?: "left" | "right" | "center";
}

export interface TimeseriesBlockData {
  title: string;
  series: { label: string; color?: string; values: number[] }[];
  labels: string[];
  yLabel?: string;
}

export interface BarBlockData {
  title: string;
  bars: { label: string; value: number; color?: string }[];
  yLabel?: string;
  horizontal?: boolean;
}

export interface HeatmapBlockData {
  title: string;
  buckets: number[];
  times: string[];
  values: number[][];
}

export interface TraceWaterfallData {
  spans: TraceSpan[];
}

export interface TraceSpan {
  id: string;
  parent: string | null;
  service: string;
  operation: string;
  start: number;
  duration: number;
  status: string;
}

export interface TopologyData {
  nodes: TopologyNode[];
  edges: TopologyEdge[];
}

export interface TopologyNode {
  id: string;
  status: string;
  rpm: number;
  p95: number;
  errors: number;
}

export interface TopologyEdge {
  source: string;
  target: string;
  rpm: number;
  errorRate: number;
}

export interface FlameGraphData {
  frames: FlameFrame[];
}

export interface FlameFrame {
  name: string;
  depth: number;
  x: number;
  w: number;
  self: number;
  total: number;
  service: string;
}

export interface SankeyData {
  nodes: { id: string; label: string; rpm: number; status?: string }[];
  links: { source: string; target: string; value: number }[];
}

export interface DepMatrixData {
  services: string[];
  cells: { from: string; to: string; errorRate: number; rpm: number; p95: number }[];
}

export interface EndpointsData {
  endpoints: {
    method: string;
    path: string;
    rpm: number;
    p50: number;
    p95: number;
    p99: number;
    errorRate: number;
    status: string;
  }[];
}

export interface CorrelationData {
  times: string[];
  panels: {
    label: string;
    color: string;
    values: number[];
    baseline?: number;
    markers?: { t: string; label: string; severity: string }[];
  }[];
}

export interface TailData {
  entries: LogEntry[];
}

export interface LogEntry {
  time: number;
  severity: string;
  service: string;
  body: string;
  traceId?: string;
}

// Client message (sent to server)
export interface ClientMessage {
  type: "message" | "cancel" | "clear";
  content?: string;
  window?: number;
  namespace?: string;
}

// Bookmark
export interface Bookmark {
  id: string;
  question: string;
  answer_html: string;
  created_at: string;
}
