import type { Message } from "@ag-ui/client";
import type { DashboardRecord, DashboardSummary } from "../dashboards/api";
import type { PanelFragment } from "../../../panels/fragment";
import type { Cell, Column, Frame, Panel, PanelResult } from "../../../panels/types";
import type { MCPAppContent } from "../mcp-app-content";
import { receiptForTurn } from "../dashboard-receipt";
import type { SaveReceipt } from "../dashboard-tool-result";

export const storyNow = Date.parse("2026-10-21T19:45:00Z");
export const services = ["checkout", "cart", "accounting", "payment"];
const column = (name: string, type: Column["type"] = "number", role: Column["role"] = "measure", unit?: string): Column => ({ name, type, role, unit });
const dimension = (name: string) => column(name, "string", "dimension");
const time = column("time", "time", "time");
function frame(columns: Column[], rows: Cell[][]): Frame {
  return { columns, rows: rows.length, values: columns.map((_, i) => rows.map(row => row[i])) };
}
const times = Array.from({ length: 12 }, (_, i) => storyNow - (11 - i) * 300_000);
const healthFrame: Frame = {
  ...frame([dimension("service"), dimension("health"), column("spans", "number", "measure", "count"), column("error_rate", "number", "measure", "percent"), column("p50_ms", "number", "measure", "ms"), column("p95_ms", "number", "measure", "ms"), column("log_count"), column("metric_count")], [
    ["checkout", "degraded", 28420, 1.24, 86, 342, 3860, 720],
    ["cart", "healthy", 42160, 0.08, 24, 78, 2140, 720],
    ["accounting", "healthy", 12680, 0.04, 42, 126, 860, 720],
    ["payment", "healthy", 25640, 0.12, 63, 184, 1740, 720],
  ]),
  health: { health: "degraded", counts: { healthy: 3, degraded: 1, unhealthy: 0 }, service_count: 4, total_spans: 108900, error_rate: 0.38, error_trend: [0.12, 0.11, 0.14, 0.13, 0.18, 0.24, 0.32, 0.41, 0.48, 0.44, 0.40, 0.38] },
};

export const panels: Panel[] = [
  { id: "requests", title: "Requests", viz: "stat", unit: "count", query: { from: "spans", measures: ["count()"] }, width: 4, height: "s" },
  { id: "latency", title: "p95 latency", viz: "timeseries", unit: "ms", query: { from: "spans", measures: ["p95(duration_ms)"], by: ["service"], bucket: "5m" }, width: 8, height: "m" },
  { id: "health", title: "System health", viz: "health", query: { from: "spans", limit: 400 }, width: 6, height: "m" },
  { id: "traffic", title: "Requests by service", viz: "bar", unit: "count", query: { from: "spans", measures: ["count()"], by: ["service"] }, width: 6, height: "m" },
  { id: "errors", title: "Error rate", viz: "gauge", unit: "percent", min: 0, max: 5, thresholds: [{ value: 1, status: "warn" }, { value: 2, status: "bad" }] },
  { id: "services", title: "Service performance", viz: "table" },
  { id: "notes", title: "Operations notes", viz: "text", content: "### Checkout latency\nCheckout p95 rose to **342 ms** after the 19:20 rollout. Cart, accounting, and payment are within their latency budgets." },
  { id: "density", title: "Latency density", viz: "heatmap", unit: "count", x_unit: "ms" },
  { id: "distribution", title: "Latency distribution", viz: "histogram", unit: "count", x_unit: "ms" },
  { id: "correlation", title: "Traffic and latency", viz: "scatter", unit: "ms", x_unit: "count" },
  { id: "timeline", title: "Service latency over time", viz: "state_timeline", unit: "ms", thresholds: [{ value: 200, status: "warn" }, { value: 500, status: "bad" }] },
  { id: "logs", title: "Recent checkout logs", viz: "logs" },
  { id: "patterns", title: "Log patterns", viz: "log_patterns" },
  { id: "traces", title: "Slow checkout traces", viz: "traces" },
  { id: "topology", title: "Service dependencies", viz: "service_map" },
];

const latencyRows = times.flatMap((at, i) => services.map((service, j) => [at, service, [188, 60, 108, 166][j] + (j === 0 ? i * 14 : (i % 4) * 6)]));
const buckets = [16, 32, 64, 128, 256, 512];
const frames: Record<string, Frame> = {
  requests: frame([column("count", "number", "measure", "count")], [[108900]]),
  latency: frame([time, dimension("service"), column("p95", "number", "measure", "ms")], latencyRows),
  health: healthFrame,
  traffic: frame([dimension("service"), column("count", "number", "measure", "count")], services.map((service, i) => [service, [28420, 42160, 12680, 25640][i]])),
  errors: frame([column("error_rate", "number", "measure", "percent")], [[0.38]]),
  services: frame([dimension("service"), column("requests", "number", "measure", "count"), column("p95", "number", "measure", "ms"), column("error_rate", "number", "measure", "percent")], services.map((service, i) => [service, [28420, 42160, 12680, 25640][i], [342, 78, 126, 184][i], [1.24, 0.08, 0.04, 0.12][i]])),
  density: frame([time, column("bucket_lower", "number", "dimension", "ms"), column("bucket_upper", "number", "dimension", "ms"), column("count", "number", "measure", "count")], times.flatMap((at, i) => buckets.map((b, j) => [at, b, b * 2, Math.round(500 / (j + 1) + i * 4)]))),
  distribution: frame([column("bucket_lower", "number", "dimension", "ms"), column("bucket_upper", "number", "dimension", "ms"), column("count", "number", "measure", "count")], buckets.map((b, i) => [b, b * 2, [420, 1480, 2860, 1860, 640, 180][i]])),
  correlation: frame([dimension("service"), column("requests", "number", "measure", "count"), column("p95", "number", "measure", "ms")], services.map((service, i) => [service, [28420, 42160, 12680, 25640][i], [342, 78, 126, 184][i]])),
  timeline: frame([time, dimension("service"), column("p95", "number", "measure", "ms")], latencyRows),
  logs: frame([time, dimension("severity"), dimension("service"), dimension("body"), dimension("trace_id"), dimension("namespace")], [
    [storyNow - 60000, "WARN", "checkout", "Payment authorization retried after 300 ms", "trace-checkout-001", "shop"],
    [storyNow - 120000, "INFO", "checkout", "Order completed in 184 ms", "trace-checkout-002", "shop"],
    [storyNow - 180000, "ERROR", "checkout", "Inventory reservation timed out after 500 ms", "trace-checkout-003", "shop"],
  ]),
  patterns: frame([dimension("body_template"), column("count", "number", "measure", "count"), column("trend", "json", "dimension")], [["Payment authorization retried after <*> ms", 42, "[2,3,4,8,12,13]"], ["Order completed in <*> ms", 28420, "[4100,4600,4800,4720,5020,5180]"]]),
  traces: frame([dimension("trace_id"), dimension("namespace"), dimension("service"), dimension("operation"), column("duration_ms", "number", "measure", "ms"), dimension("status"), column("start", "time", "time")], [["trace-checkout-001", "shop", "checkout", "POST /checkout", 342, "STATUS_CODE_OK", storyNow - 60000], ["trace-checkout-003", "shop", "checkout", "POST /checkout", 512, "STATUS_CODE_ERROR", storyNow - 180000]]),
  topology: frame([dimension("kind"), dimension("service"), dimension("caller"), dimension("callee"), dimension("edge_type"), column("calls"), column("average_ms", "number", "measure", "ms"), column("error_rate", "number", "measure", "percent"), dimension("health"), column("p95_ms", "number", "measure", "ms"), column("spans")], [
    ...services.map((service, i) => ["node", service, "", "", "", null, null, [1.24, 0.08, 0.04, 0.12][i], i === 0 ? "degraded" : "healthy", [342, 78, 126, 184][i], 12000]),
    ...[["checkout", "cart", 28], ["checkout", "payment", 86], ["payment", "accounting", 42]].map(([caller, callee, ms]) => ["edge", "", caller, callee, "call", 12400, ms, 0.12, "", null, null]),
  ]),
};

export const results: PanelResult[] = panels.map(panel => ({ id: panel.id, status: "ok", frame: frames[panel.id], elapsed_ms: 24, interval: "5m", from_ms: storyNow - 3600000, to_ms: storyNow }));
export const health: PanelFragment = { view: { kind: "preset", key: "a".repeat(64) }, dashboard: { version: 1, name: "System health", time: { range: "1h", refresh: "off" }, panels: [panels[2]] }, results: [results[2]] };
export const healthContent: MCPAppContent = { resource_uri: "ui://fanout/panels.html", tool_name: "get_observability_overview", tool_input: { window: "1h" }, tool_result: health, is_error: false };
// Saved responses carry the server's packed coordinates, not only draft widths.
const dashboardGrids = [
  { x: 0, y: 0, w: 4, h: 6 }, { x: 4, y: 0, w: 8, h: 6 },
  { x: 0, y: 6, w: 6, h: 6 }, { x: 6, y: 6, w: 6, h: 6 },
];
export const dashboard: DashboardRecord = {
  id: "commerce-health", name: "Commerce health", description: "Request volume, latency, and errors across the commerce fleet.", is_default: true, version: 1,
  created_at: new Date(storyNow).toISOString(), updated_at: new Date(storyNow).toISOString(),
  spec: { version: 1, name: "Commerce health", description: "Checkout, cart, accounting, and payment · last hour", time: { range: "1h", refresh: "off" }, panels: panels.slice(0, 4).map((panel, index) => ({ ...panel, grid: dashboardGrids[index] })) },
};
export const dashboardSummary: DashboardSummary = { id: dashboard.id, name: dashboard.name, description: dashboard.description, is_default: true, version: 1, panel_count: dashboard.spec.panels.length, updated_at: dashboard.updated_at };
export const question = "How are checkout, cart, accounting, and payment doing over the last hour?";
export const dashboardQuestion = "Build a dashboard for checkout, cart, accounting, and payment with request volume, p95 latency, and errors.";
export const answer = "Checkout needs attention: p95 latency is **342 ms** and errors are **1.24%**. Cart (78 ms), accounting (126 ms), and payment (184 ms) are healthy. The fleet handled **108,900 requests** in the last hour.";
export const userMessage: Message = { id: "question-001", role: "user", content: question };
export const answeredMessages: Message[] = [userMessage, { id: "health-view-001", role: "activity", activityType: "mcp-app", content: healthContent }, { id: "answer-001", role: "assistant", content: answer }];
export const call = (id: string, name: string, input: unknown = {}): Message => ({ id: `assistant-${id}`, role: "assistant", content: "", toolCalls: [{ id, type: "function", function: { name, arguments: JSON.stringify(input) } }] });
export const toolResult = (id: string, payload: unknown): Message => ({ id: `result-${id}`, role: "tool", toolCallId: id, content: JSON.stringify(payload) });
export const saveReceipt: SaveReceipt = { base_version: 0, version: 1, changes: dashboard.spec.panels.map(panel => ({ panel_id: panel.id, title: panel.title, kind: "added" })), layout_changed: false, save_check: { checked: true, elapsed_ms: 186, panels: dashboard.spec.panels.map(panel => ({ id: panel.id, status: "ok", rows: results.find(r => r.id === panel.id)?.frame?.rows ?? 0, elapsed_ms: 24 })) } };
export const savedMessages: Message[] = [
  { ...userMessage, content: dashboardQuestion },
  call("schema", "get_telemetry_schema"), toolResult("schema", { services }),
  call("preview", "preview_panels", { panels: dashboard.spec.panels }), toolResult("preview", { elapsed_ms: 142, panels: saveReceipt.save_check.panels }),
  call("save", "create_dashboard", { dashboard: dashboard.spec }), toolResult("save", { dashboard, receipt: saveReceipt }),
  { id: "saved-answer", role: "assistant", content: "Your **Commerce health** dashboard is saved. It tracks requests, p95 latency, and service health for checkout, cart, accounting, and payment." },
];
export const savedReceipt = receiptForTurn(savedMessages, userMessage.id)!;
