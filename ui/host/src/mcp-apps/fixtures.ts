import type { PanelFragment } from "../../../panels/fragment";
import type { Result, TraceDetail } from "../../../contracts";

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
