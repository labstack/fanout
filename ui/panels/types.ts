/* The v1 dashboard spec and panel results, mirroring internal/panel. Pure
 * types: this directory has no node_modules and imports only siblings. */
export const ALL = "$__all";

export type Unit = "ms" | "s" | "ns" | "percent" | "ratio" | "count" | "per_second" | "per_minute" | "bytes" | "none";
export type Status = "ok" | "warn" | "bad";
export const visualizations = ["stat", "gauge", "timeseries", "bar", "table", "text", "heatmap", "histogram", "scatter", "state_timeline", "logs", "log_patterns", "traces", "service_map", "health"] as const;
export type Viz = typeof visualizations[number];
export type Selection = { from?: string; to?: string; time?: number; dimensions: Record<string, string>; trace_id?: string; namespace?: string; bucket?: { lower: number; upper?: number } };
export type ColumnFormat = { field: string; format: "unit"|"bar"|"status"|"sparkline"|"trace_link"|"service_link"|"log_template"; unit?: Unit; variable?: string };

export type Threshold = { value: number; status: Status; label?: string };

export type Query = { from: "spans" | "logs" | "metrics"; where?: string[]; measures?: string[]; by?: string[]; bucket?: string; histogram?: { field: string; buckets: "log2" | "explicit"; temporality?: "cumulative" | "delta" }; sort?: string; limit?: number };

export type Panel = {
  id: string;
  title: string;
  description?: string;
  viz: Viz;
  width?: number;
  height?: "s" | "m" | "l";
  query?: Query;
  sql?: string;
  unit?: Unit;
  x_unit?: Unit;
  reduce?: "window" | "last" | "mean" | "min" | "max" | "sum";
  thresholds?: Threshold[];
  better?: "lower" | "higher";
  min?: number;
  max?: number;
  options?: { style?: "line" | "area" | "bars" | "stacked"; scale?: "linear" | "log"; top?: number; legend?: "auto" | "hidden"; x_scale?: "linear" | "log"; y_scale?: "linear" | "log"; highlight?: string; columns?: ColumnFormat[]; split?: "deploy" };
  click?: { set_variable: string };
  drill?: "traces" | "logs";
  time?: { range?: string; shift?: string };
  content?: string;
  grid?: { x: number; y: number; w: number; h: number };
};

export type Variable = {
  name: string;
  kind: "query" | "custom" | "constant" | "text";
  from?: string;
  field?: string;
  where?: string[];
  options?: string[];
  value?: string;
  default?: string;
  multi?: boolean;
  include_all?: boolean;
};

export type DashboardTime = { range?: string; from?: string; to?: string; refresh?: string; compare?: "previous_period" };

export type DashboardSpec = {
  version: 1;
  name: string;
  description?: string;
  time: DashboardTime;
  variables?: Variable[];
  annotations?: { deploys?: boolean; anomalies?: boolean };
  panels: Panel[];
};

export type Cell = string | number | null;

export type Column = { name: string; type: "time" | "number" | "string" | "json"; role: "time" | "dimension" | "measure"; unit?: string };

export type HealthFrame = { health: string; counts: { healthy: number; degraded: number; unhealthy: number }; total_spans: number; error_rate: number; service_count: number; error_trend: number[] };
export type AnnotationService = { namespace: string; service: string };
export type AnnotationMatch = { services: AnnotationService[]; namespace_scoped?: boolean; limited?: boolean };
export type Frame = { periods?: Record<string, { from: string; to: string }>; trends?: Record<string, (number | null)[][]>; note?: string; columns: Column[]; values: Cell[][]; rows: number; totals?: Cell[]; truncated?: boolean; health?: HealthFrame; trend?: { start_ms: number; step_ms: number } };

export type PanelResult = {
  annotation_scope?: AnnotationMatch;
  annotation_error?: string;
  id: string;
  status: "ok" | "empty" | "error";
  frame?: Frame;
  previous?: Frame;
  error?: string;
  diagnosis?: string;
  sql?: string;
  interval?: string;
  elapsed_ms: number;
  from_ms?: number;
  to_ms?: number;
  better?: "lower" | "higher";
  shift_ms?: number;
};

export type VarValue = string | string[];
