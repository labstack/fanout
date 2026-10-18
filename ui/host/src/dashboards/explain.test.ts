import { expect, it } from "vitest";
import { explainPrompt, fixPrompt, panelContext } from "./explain";

it("uses a short product request and carries complete diagnostics only in context", () => {
  const c = { dashboard_id: "board", version: 7, panel_id: "latency", title: "Checkout latency", from_ms: 0, to_ms: 3600000, vars: { service: ["checkout"] }, error: "Binder Error: TIMESTAMPTZ_NS", diagnosis: "No rows", status: "error", stale_since: 42, truncated: true, note: "Partial", spec: { sql: "SELECT duration_ms FROM telemetry.spans" } };
  expect(explainPrompt(c)).toBe("Explain the panel “Checkout latency”");
  expect(fixPrompt(c)).toBe("Fix the panel “Checkout latency”");
  const context = panelContext(c);
  expect(context).toHaveLength(1);
  expect(JSON.parse(context[0].value)).toEqual(c);
  expect(explainPrompt(c) + fixPrompt(c)).not.toMatch(/Binder|TIMESTAMPTZ_NS|telemetry|board|SELECT/);
});

it("preserves exact authored bounds and an absent execution window in context", () => {
  const c = { panel_id: "p", title: 'Latency "checkout"\nnext line', vars: {}, spec: { time: { from: "2026-10-08T00:00:00.123456789Z", to: "2026-10-08T01:00:00.987654321Z" } } };
  expect(JSON.parse(panelContext(c)[0].value)).toEqual(c);
  expect(JSON.parse(panelContext(c)[0].value).from_ms).toBeUndefined();
});
