import type { Frame, PanelResult, Status } from "./types";

export const healthGlyph: Record<string, string> = { healthy: "●", degraded: "■", unhealthy: "◆", unknown: "○" };
export type ServiceNode = { id: string; health: string; spans: number | null; p95_ms: number | null; error_rate: number | null; request_rate: number | null };
export type ServiceEdge = { id: string; caller: string; callee: string; edge_type: string; calls: number; average_ms: number | null; error_rate: number; request_rate: number | null; status: Status | null };
export type ServiceGraph = { nodes: ServiceNode[]; edges: ServiceEdge[] };
const order = (a: string, b: string) => a < b ? -1 : a > b ? 1 : 0;

/** Pure graph/metric model. The host owns Dagre and rendering; no layout packages here.
 * Frame error rates are percentages: observability's 1% degraded / 5% unhealthy. */
export function serviceMapModel(frame: Frame, window: Pick<PanelResult, "from_ms" | "to_ms"> = {}): ServiceGraph {
  const index = new Map(frame.columns.map((c, i) => [c.name, i]));
  const cell = (name: string, row: number) => frame.values[index.get(name) ?? -1]?.[row];
  const text = (name: string, row: number) => String(cell(name, row) ?? "");
  const num = (name: string, row: number) => { const n = cell(name, row); return typeof n === "number" && Number.isFinite(n) ? n : null; };
  const seconds = window.from_ms !== undefined && window.to_ms !== undefined && window.to_ms > window.from_ms ? (window.to_ms - window.from_ms) / 1000 : undefined;
  const rate = (count: number | null) => seconds && count !== null ? count / seconds : null;
  const nodes = new Map<string, ServiceNode>(); const edges: ServiceEdge[] = [];
  for (let r = 0; r < frame.rows; r++) {
    if (text("kind", r) === "node") {
      const id = text("service", r), spans = num("spans", r);
      nodes.set(id, { id, health: text("health", r) || "unknown", spans, p95_ms: num("p95_ms", r), error_rate: num("error_rate", r), request_rate: rate(spans) });
    } else if (text("kind", r) === "edge") {
      const caller = text("caller", r), callee = text("callee", r), edge_type = text("edge_type", r), calls = num("calls", r) ?? 0, error_rate = num("error_rate", r) ?? 0;
      edges.push({ id: JSON.stringify([caller, callee, edge_type]), caller, callee, edge_type, calls, average_ms: num("average_ms", r), error_rate, request_rate: rate(calls), status: error_rate >= 5 ? "bad" : error_rate >= 1 ? "warn" : null });
    }
  }
  for (const edge of edges) for (const id of [edge.caller, edge.callee]) if (!nodes.has(id)) nodes.set(id, { id, health: "unknown", spans: null, p95_ms: null, error_rate: null, request_rate: null });
  return { nodes: [...nodes.values()].sort((a, b) => order(a.id, b.id)), edges: edges.sort((a, b) => order(a.id, b.id)) };
}
