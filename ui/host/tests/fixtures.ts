import { expect } from "vitest";
import type { Panel, Frame, Cell, Column } from "../../panels/types";
import type { PanelFragment } from "../../panels/fragment";
import type { Result, TraceDetail } from "../../contracts";

export const traceFixture: Result<TraceDetail> = {
  schema: "fanout.trace.v1", summary: "Trace",
  provenance: { query_id: "q", window: "1h", generated_at: "2026-10-08T00:00:00Z", complete: true, data_source: "spans" },
  data: { trace_id: "abc", duration_ms: 10, has_error: true, services: ["checkout"],
    spans: [{ span_id: "root", service: "checkout", operation: "cart", kind: "SPAN_KIND_SERVER", start: "2026-10-08T00:00:00Z", duration_ms: 10, status: "ERROR" }],
    logs: [{ time: "2026-10-08T00:00:00Z", severity: "ERROR", service: "checkout", body: "correlated failure" }], span_count: 3, service_count: 2, truncated: true },
};
export function fixture(viz: "health" | "service_map" | "logs" | "traces" | "log_patterns" = "logs"): PanelFragment {
  return { dashboard: { version: 1, name: "Answer", time: { range: "1h", refresh: "off" }, panels: [{ id: "p", title: "Checkout logs", viz, query: { from: "logs" } }] },
    results: [{ id: "p", status: "empty", diagnosis: "No logs for checkout", elapsed_ms: 1, from_ms: 0, to_ms: 3600000 }] };
}

// Task 1b preset panels, caps and captured windows from the frozen plan.
export const presets = ["overview", "performance", "topology", "logs", "trace"] as const;
export function presetFixture(preset: typeof presets[number]): PanelFragment {
  const from = "2026-10-08T00:00:00Z", to = "2026-10-08T01:00:00Z";
  const where = ["namespace = 'shop'", "service = 'checkout'"];
  const dashboard: PanelFragment["dashboard"] = { version: 1, name: "Telemetry", time: { from, to, refresh: "off" }, panels: [] };
  const add = (id: string, title: string, viz: Panel["viz"], from: "spans" | "logs", measures?: string[], unit?: Panel["unit"], by?: string[], bucket?: string) => {
    dashboard.panels.push({ id, title, viz, unit, query: { from, where: [...where], measures, by, bucket, limit: viz === "logs" ? 100 : viz === "traces" ? 50 : 400 },
      ...(from === "spans" && viz !== "health" && viz !== "service_map" ? { drill: "traces" as const } : {}) });
  };
  switch (preset) {
    case "overview": add("health", "System health", "health", "spans"); break;
    case "topology": add("services", "Service dependencies", "service_map", "spans"); break;
    case "performance":
      add("latency", "p95 latency", "timeseries", "spans", ["p95(duration_ms)"], "ms", undefined, "auto");
      add("errors", "Error rate", "timeseries", "spans", ["error_rate()"], "percent", undefined, "auto");
      add("requests", "Request rate", "timeseries", "spans", ["rate()"], "per_second", undefined, "auto");
      add("endpoints", "Slow endpoints", "table", "spans", ["p95(duration_ms)"], "ms", ["http_route"]);
      dashboard.panels.at(-1)!.query!.where!.push("http_route <> ''"); break;
    case "logs":
      where.push("severity = 'ERROR'");
      add("volume", "Log volume", "timeseries", "logs", ["count()"], "count", ["severity"], "auto");
      add("events", "Log events", "logs", "logs");
      dashboard.panels[0].query!.where!.push("contains(lower(body),lower('timeout'))");
      dashboard.panels[1].options = { highlight: "timeout" }; break;
    case "trace":
      add("traces", "Slow or erroring traces", "traces", "spans");
      dashboard.panels[0].query!.where!.push("trace_id = 'abc'"); break;
  }
  const frame = (columns: Frame["columns"], rows: Cell[][]): Frame => ({ columns, rows: rows.length, values: columns.map((_, index) => rows.map(row => row[index])) });
  const dimension = (name: string): Column => ({ name, type: "string", role: "dimension" });
  const measure = (name: string, unit?: string): Column => ({ name, type: "number", role: "measure", unit });
  const time: Column = { name: "time", type: "time", role: "time" };
  const results = dashboard.panels.map(panel => {
    let data: Frame;
    switch (panel.viz) {
      case "health":
        data = frame([dimension("service"), dimension("health"), measure("spans"), measure("p95_ms", "ms"), measure("error_rate", "percent")], [["checkout", "unhealthy", 120, 85, 6], ["inventory", "healthy", 200, 20, 0]]);
        data.health = { health: "unhealthy", counts: { healthy: 1, degraded: 0, unhealthy: 1 }, total_spans: 320, error_rate: 2.25, service_count: 2, error_trend: [1, 2, 2.25] }; break;
      case "service_map":
        data = frame([dimension("kind"), dimension("service"), dimension("health"), measure("spans"), measure("p95_ms"), measure("error_rate"), dimension("caller"), dimension("callee"), dimension("edge_type"), measure("calls"), measure("average_ms")], [
          ["node", "checkout", "unhealthy", 120, 85, 6, null, null, null, null, null],
          ["node", "inventory", "healthy", 200, 20, 0, null, null, null, null, null],
          ["edge", null, null, null, null, 6, "checkout", "inventory", "sync", 100, 12],
        ]); break;
      case "table": data = frame([dimension("http_route"), measure("p95(duration_ms)", "ms")], [["/checkout", 85], ["/cart", 30]]); break;
      case "logs": data = frame([time, dimension("severity"), dimension("service"), dimension("body"), dimension("trace_id"), dimension("namespace")], [[Date.parse(from), "ERROR", "checkout", "checkout timeout", "abc", "shop"]]); break;
      case "traces": data = frame([dimension("trace_id"), dimension("service"), dimension("operation"), measure("duration_ms", "ms"), dimension("status"), dimension("namespace")], [["abc", "checkout", "cart", 10, "STATUS_CODE_ERROR", "shop"]]); break;
      default:
        data = frame([time, ...(panel.id === "volume" ? [dimension("severity")] : []), measure(panel.query!.measures![0], panel.unit)], panel.id === "volume" ? [[Date.parse(from), "ERROR", 4], [Date.parse(from) + 60000, "ERROR", 8]] : [[Date.parse(from), 85], [Date.parse(from) + 60000, 90]]);
    }
    return { id: panel.id, status: "ok" as const, elapsed_ms: 1, from_ms: Date.parse(from), to_ms: Date.parse(to), interval: "1m", frame: data };
  });
  results.at(-1)!.frame.truncated = true;
  results.at(-1)!.frame.note = "Panel view payload limit: showing bounded rows";
  return { dashboard, results, ...(preset === "trace" ? { trace: traceFixture } : {}) };
}

export function assertPresetData(node: HTMLElement, preset: typeof presets[number]) {
  expect(node.textContent).toContain("Truncated: showing limited data");
  expect(node.textContent).toContain("Panel view payload limit: showing bounded rows");
  expect(node.textContent).not.toMatch(/Explain|fix it|Duplicate|Remove panel|Copy link/);
  if (preset === "performance") expect(node.textContent).toContain("/checkout");
  else if (preset === "logs") expect(node.textContent).toContain("checkout timeout");
  else if (preset === "trace") {
    expect(node.querySelector('[aria-label="Trace ID abc"]')).not.toBeNull();
    expect(node.querySelector('[role="img"][aria-label^="cart on checkout took"]')).not.toBeNull();
    expect(node.textContent).toContain("correlated failure");
  } else expect(node.textContent).toContain("checkout");
}
