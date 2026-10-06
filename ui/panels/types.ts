/* The v1 dashboard spec and panel results, mirroring internal/panel. Pure
 * types: this directory has no node_modules and imports only siblings. */
export const ALL = "$__all";

export type Unit = "ms" | "s" | "ns" | "percent" | "ratio" | "count" | "per_second" | "per_minute" | "bytes" | "none";
export type Status = "ok" | "warn" | "bad";
export type Viz = "stat" | "gauge" | "timeseries" | "bar" | "table" | "text" | "service_map" | "health";

export type Threshold = { value: number; status: Status; label?: string };

export type Query = { from: "spans" | "logs" | "metrics"; where?: string[]; measures?: string[]; by?: string[]; bucket?: string; sort?: string; limit?: number };

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
  reduce?: "window" | "last" | "mean" | "min" | "max" | "sum";
  thresholds?: Threshold[];
  better?: "lower" | "higher";
  min?: number;
  max?: number;
  options?: { style?: "line" | "area" | "bars" | "stacked"; scale?: "linear" | "log"; top?: number; legend?: "auto" | "hidden" };
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
export type Frame = { columns: Column[]; values: Cell[][]; rows: number; totals?: Cell[]; truncated?: boolean; health?: HealthFrame };

export type PanelResult = {
  id: string;
  status: "ok" | "empty" | "error";
  frame?: Frame;
  previous?: Frame;
  error?: string;
  diagnosis?: string;
  sql?: string;
  interval?: string;
  elapsed_ms: number;
  better?: "lower" | "higher";
  shift_ms?: number;
};

export type VarValue = string | string[];
