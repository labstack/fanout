export type Health = "healthy" | "degraded" | "unhealthy";

export interface Provenance {
  query_id: string;
  window: string;
  generated_at: string;
  complete: boolean;
  data_source: string;
}

export interface Result<T> {
  schema: string;
  summary: string;
  data: T;
  provenance: Provenance;
}

export interface ServiceHealth {
  service: string;
  health: Health;
  spans: number;
  error_rate: number;
  p50_ms: number;
  p95_ms: number;
  log_count: number;
  metric_count: number;
}

export interface Overview {
  health: Health;
  counts: { healthy: number; degraded: number; unhealthy: number };
  total_spans: number;
  error_rate: number;
  services: ServiceHealth[];
  service_count: number;
}

export interface Edge {
  caller: string;
  callee: string;
  type: string;
  calls: number;
  average_ms: number;
  error_rate: number;
}

export interface Topology {
  nodes: ServiceHealth[];
  edges: Edge[];
}

export interface PerformancePoint {
  time: string;
  spans: number;
  error_rate: number;
  p50_ms: number;
  p95_ms: number;
  log_count: number;
  metric_count: number;
}

export interface Endpoint {
  method: string;
  path: string;
  calls: number;
  p50_ms: number;
  p95_ms: number;
  p99_ms: number;
  error_rate: number;
  health: Health;
}

export interface HeatmapPoint {
  time: string;
  service: string;
  p95_ms: number;
}

export interface ComparisonMetric {
  label: string;
  unit: string;
  before: number;
  after: number;
  change_pct: number;
  direction: "improvement" | "regression" | "stable";
  significant: boolean;
}

export interface Performance {
  service?: string;
  points: PerformancePoint[];
  endpoints: Endpoint[];
  heatmap: HeatmapPoint[];
  comparison: ComparisonMetric[];
}

export interface TraceSpan {
  span_id: string;
  parent_span_id?: string;
  service: string;
  operation: string;
  kind: string;
  start: string;
  duration_ms: number;
  status: string;
  status_message?: string;
}

export interface LogEntry {
  time: string;
  severity: string;
  service: string;
  body: string;
  trace_id?: string;
  span_id?: string;
}

export interface TraceDetail {
  trace_id: string;
  duration_ms: number;
  has_error: boolean;
  services: string[];
  spans: TraceSpan[];
  logs: LogEntry[];
}

export interface LogBucket {
  time: string;
  severity: string;
  count: number;
}

export interface Logs {
  entries: LogEntry[];
  buckets: LogBucket[];
}
