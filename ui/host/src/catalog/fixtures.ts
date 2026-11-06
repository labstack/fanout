import type { Result, TraceDetail } from "../../../contracts";
import type { Message } from "@ag-ui/client";
import type { DashboardRecord, DashboardSummary } from "../dashboards/api";
import type { PanelFragment } from "../../../panels/fragment";
import type { Cell, Column, Frame, Panel, PanelResult } from "../../../panels/types";
import type { MCPAppContent } from "../mcp-app-content";
import { receiptForTurn } from "../dashboard-receipt";
import type { SaveReceipt } from "../dashboard-tool-result";

export const storyNow = Date.parse("2026-10-21T19:45:00Z");
export const services = ["checkout", "cart", "accounting", "payment"];
export const serviceSpans = [28420, 42160, 12680, 25640];
const serviceErrors = [1.24, 0.08, 0.04, 0.12];
const serviceP95 = [342, 78, 126, 184];
// Fictional OTLP IDs; even their displayed 16-character prefixes are distinct.
export const traceIDs = ["1111111111111111aaaaaaaaaaaaaaaa", "2222222222222222bbbbbbbbbbbbbbbb", "3333333333333333cccccccccccccccc"];
export const traceDetail: Result<TraceDetail> = {
  summary: "Checkout completed after a payment authorization retry.",
  provenance: { window: "1h", generated_at: new Date(storyNow).toISOString(), complete: true },
  data: {
    trace_id: traceIDs[0], duration_ms: 342, has_error: false, services,
    spans: [
      { span_id: "11111111aaaaaaaa", service: "checkout", operation: "POST /checkout", kind: "server", start: new Date(storyNow - 60342).toISOString(), duration_ms: 342, status: "STATUS_CODE_OK" },
      { span_id: "22222222bbbbbbbb", parent_span_id: "11111111aaaaaaaa", service: "cart", operation: "GET /cart", kind: "server", start: new Date(storyNow - 60330).toISOString(), duration_ms: 28, status: "STATUS_CODE_OK" },
      { span_id: "33333333cccccccc", parent_span_id: "11111111aaaaaaaa", service: "payment", operation: "POST /authorize", kind: "server", start: new Date(storyNow - 60300).toISOString(), duration_ms: 300, status: "STATUS_CODE_OK" },
      { span_id: "44444444dddddddd", parent_span_id: "33333333cccccccc", service: "accounting", operation: "POST /entries", kind: "server", start: new Date(storyNow - 60050).toISOString(), duration_ms: 42, status: "STATUS_CODE_OK" },
    ],
    logs: [{ time: new Date(storyNow - 60000).toISOString(), severity: "WARN", service: "checkout", body: "Payment authorization retried after 300 ms", trace_id: traceIDs[0], span_id: "11111111aaaaaaaa" }],
    span_count: 4, service_count: 4, truncated: false,
  },
};
const column = (name: string, type: Column["type"] = "number", role: Column["role"] = "measure", unit?: string): Column => ({ name, type, role, unit });
const dimension = (name: string) => column(name, "string", "dimension");
const time = column("time", "time", "time");
function frame(columns: Column[], rows: Cell[][]): Frame {
  return { columns, rows: rows.length, values: columns.map((_, i) => rows.map(row => row[i])) };
}
const times = Array.from({ length: 12 }, (_, i) => storyNow - (11 - i) * 300_000);
const healthFrame: Frame = {
  ...frame([dimension("service"), dimension("health"), column("spans", "number", "measure", "count"), column("error_rate", "number", "measure", "percent"), column("p50_ms", "number", "measure", "ms"), column("p95_ms", "number", "measure", "ms"), column("log_count"), column("metric_count")], [
    ["checkout", "degraded", serviceSpans[0], serviceErrors[0], 86, serviceP95[0], 3860, 720],
    ["cart", "healthy", serviceSpans[1], serviceErrors[1], 24, serviceP95[1], 2140, 720],
    ["accounting", "healthy", serviceSpans[2], serviceErrors[2], 42, serviceP95[2], 860, 720],
    ["payment", "healthy", serviceSpans[3], serviceErrors[3], 63, serviceP95[3], 1740, 720],
  ]),
  health: { health: "degraded", counts: { healthy: 3, degraded: 1, unhealthy: 0 }, service_count: 4, total_spans: 108900, error_rate: 0.39, error_trend: [0.12, 0.11, 0.14, 0.13, 0.18, 0.24, 0.32, 0.41, 0.48, 0.44, 0.40, 0.39] },
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

const latencyRows = times.flatMap((at, i) => services.map((service, j) => [at, service, j === 0 ? (at <= Date.parse("2026-10-21T19:20:00Z") ? 188 : 342) : [188, 60, 108, 166][j] + (i % 4) * 6]));
const buckets = [0, 32, 64, 128, 256, 512];
// Each five-minute slice contains 9,075 spans. The rollout shifts traffic
// into the slower buckets; summing the slices produces the hourly histogram.
const beforeRollout = [2400, 3100, 2300, 1100, 150, 25];
const afterRollout = [2200, 2850, 2150, 1300, 500, 75];
const densityRows = times.flatMap(at => buckets.map((b, j) => [at, b, j === 0 ? 32 : b * 2, (at <= Date.parse("2026-10-21T19:20:00Z") ? beforeRollout : afterRollout)[j]]));
const frames: Record<string, Frame> = {
  requests: frame([column("count", "number", "measure", "count")], [[108900]]),
  latency: frame([time, dimension("service"), column("p95", "number", "measure", "ms")], latencyRows),
  health: healthFrame,
  traffic: frame([dimension("service"), column("count", "number", "measure", "count")], services.map((service, i) => [service, serviceSpans[i]])),
  errors: frame([column("error_rate", "number", "measure", "percent")], [[0.39]]),
  services: frame([dimension("service"), column("requests", "number", "measure", "count"), column("p95", "number", "measure", "ms"), column("error_rate", "number", "measure", "percent")], services.map((service, i) => [service, serviceSpans[i], serviceP95[i], serviceErrors[i]])),
  density: frame([time, column("bucket_lower", "number", "dimension", "ms"), column("bucket_upper", "number", "dimension", "ms"), column("count", "number", "measure", "count")], densityRows),
  distribution: frame([column("bucket_lower", "number", "dimension", "ms"), column("bucket_upper", "number", "dimension", "ms"), column("count", "number", "measure", "count")], buckets.map((b, i) => [b, i === 0 ? 32 : b * 2, densityRows.filter(row => row[1] === b).reduce((sum, row) => sum + row[3], 0)])),
  correlation: frame([dimension("service"), column("requests", "number", "measure", "count"), column("p95", "number", "measure", "ms")], services.map((service, i) => [service, serviceSpans[i], serviceP95[i]])),
  timeline: frame([time, dimension("service"), column("p95", "number", "measure", "ms")], latencyRows),
  logs: frame([time, dimension("severity"), dimension("service"), dimension("body"), dimension("trace_id"), dimension("namespace")], [
    [storyNow - 60000, "WARN", "checkout", "Payment authorization retried after 300 ms", traceIDs[0], "shop"],
    [storyNow - 120000, "INFO", "checkout", "Order completed in 184 ms", traceIDs[1], "shop"],
    [storyNow - 180000, "ERROR", "checkout", "Inventory reservation timed out after 500 ms", traceIDs[2], "shop"],
  ]),
  patterns: frame([dimension("body_template"), column("count", "number", "measure", "count"), column("trend", "json", "dimension")], [["Payment authorization retried after <*> ms", 42, "[2,3,4,8,12,13]"], ["Order completed in <*> ms", 28420, "[4100,4600,4800,4720,5020,5180]"], ["Inventory reservation timed out after <*> ms", 64, "[1,2,3,14,20,24]"]]),
  traces: frame([dimension("trace_id"), dimension("namespace"), dimension("service"), dimension("operation"), column("duration_ms", "number", "measure", "ms"), dimension("status"), column("start", "time", "time")], [[traceIDs[0], "shop", "checkout", "POST /checkout", 342, "STATUS_CODE_OK", storyNow - 60342], [traceIDs[2], "shop", "checkout", "POST /checkout", 512, "STATUS_CODE_ERROR", storyNow - 180000]]),
  topology: frame([dimension("kind"), dimension("service"), dimension("caller"), dimension("callee"), dimension("edge_type"), column("calls"), column("average_ms", "number", "measure", "ms"), column("error_rate", "number", "measure", "percent"), dimension("health"), column("p95_ms", "number", "measure", "ms"), column("spans")], [
    ...services.map((service, i) => ["node", service, "", "", "", null, null, serviceErrors[i], i === 0 ? "degraded" : "healthy", serviceP95[i], serviceSpans[i]]),
    ...[["checkout", "cart", 28, 28420, 0.08], ["checkout", "payment", 86, 25640, 0.12], ["payment", "accounting", 42, 12680, 0.04]].map(([caller, callee, ms, calls, errorRate]) => ["edge", "", caller, callee, "call", calls, ms, errorRate, "", null, null]),
  ]),
};

export const results: PanelResult[] = panels.map(panel => ({ id: panel.id, status: "ok", frame: frames[panel.id], elapsed_ms: 24, interval: "5m", from_ms: storyNow - 3600000, to_ms: storyNow }));
export const health: PanelFragment = { view: { kind: "preset", key: "a".repeat(64) }, dashboard: { version: 1, name: "System health", time: { range: "1h", refresh: "off" }, panels: [panels[2]] }, results: [results[2]] };
export const healthContent: MCPAppContent = { resource_uri: "ui://fanout/panels.html", tool_name: "get_overview", tool_input: { window: "1h" }, tool_result: health, is_error: false };
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

// Per-panel validation leaves valid siblings executable (PR #425).
export const invalidResult: PanelResult = {
  id: "requests", status: "invalid", elapsed_ms: 0,
  error: "Unknown field: request_total",
  problems: [{ path: "panels[0].query.measures[0]", message: "Unknown field: request_total", hint: "Use count() to count spans." }],
};
export const mixedPreviewMessages: Message[] = [
  { ...userMessage, content: dashboardQuestion },
  call("mixed-preview", "preview_panels", { panels: dashboard.spec.panels }),
  toolResult("mixed-preview", { elapsed_ms: 142, panels: saveReceipt.save_check.panels.map(panel => panel.id === invalidResult.id ? { ...invalidResult, rows: 0 } : panel) }),
];
export const invalidReceipt = receiptForTurn(mixedPreviewMessages, userMessage.id)!;
export const correctedReceipt = receiptForTurn([...mixedPreviewMessages, ...savedMessages.slice(1)], userMessage.id)!;

export const exploreQuery = "spans | count() by service";
export const exploreInvalid = "spans | sum(request_total) by service";
export const exploreTypo = "spans | countt() by service";
// A visibly fictional token, never usable as an operator credential.
export const ingestToken = "foi_catalog_example_not_a_real_token";

export const savedInvalidReceipt = receiptForTurn([
  ...mixedPreviewMessages,
  call("save-invalid", "create_dashboard", { dashboard: dashboard.spec }),
  toolResult("save-invalid", { dashboard, receipt: { ...saveReceipt, save_check: { ...saveReceipt.save_check, panels: saveReceipt.save_check.panels.map(panel => panel.id === invalidResult.id ? { ...invalidResult, rows: 0 } : panel) } } }),
], userMessage.id)!;
