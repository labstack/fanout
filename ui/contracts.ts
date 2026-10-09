/** "unknown" is an empty window — no telemetry rather than good news. Mirrors
 *  the Health constants in internal/observability/contracts.go; keep in sync. */
export type Health = "healthy" | "degraded" | "unhealthy" | "unknown";

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
  // span_count and service_count describe the trace; spans and services
  // describe the page the limit admitted, which may be narrower.
  span_count: number;
  service_count: number;
  truncated: boolean;
}
