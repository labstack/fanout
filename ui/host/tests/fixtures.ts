import { expect } from "vitest";
import { panelFragment, type PanelFragment } from "../../panels/fragment";
import overview from "./go-fragments/overview.json";
import performance from "./go-fragments/performance.json";
import topology from "./go-fragments/topology.json";
import logs from "./go-fragments/logs.json";
import trace from "./go-fragments/trace.json";
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

// Generated from seeded tools/call responses by the Go contract test.
export const presets = ["overview", "performance", "topology", "logs", "trace"] as const;
export function presetFixture(preset: typeof presets[number]): PanelFragment {
  return panelFragment({ overview, performance, topology, logs, trace }[preset]);
}
export function assertPresetData(node: HTMLElement, preset: typeof presets[number]) {
  expect(node.textContent).not.toMatch(/Explain|fix it|Duplicate|Remove panel|Copy link/);
  if (preset === "performance") expect(node.textContent).toContain("/cart");
  else if (preset === "logs") expect(node.textContent).toContain("timeout");
  else if (preset === "trace") {
    expect(node.querySelector('[aria-label="Trace ID trace-1"]')).not.toBeNull();
    expect(node.querySelector('[role="img"][aria-label^="GET cart on checkout took"]')).not.toBeNull();
    expect(node.textContent).toContain("timeout");
  } else expect(node.textContent).toContain("checkout");
}
