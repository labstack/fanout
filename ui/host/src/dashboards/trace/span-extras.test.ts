import { beforeEach, expect, it, vi } from "vitest";
import type { TraceSpan } from "../../../../contracts";
import { spanExtras } from "./span-extras";

const panels = vi.hoisted(() => vi.fn());
vi.mock("../api", () => ({ queryPanels: panels }));

const span: TraceSpan = { span_id: "s'1", service: "checkout", operation: "GET /cart", kind: "SPAN_KIND_SERVER", start: "2026-10-01T12:00:00.000Z", duration_ms: 5, status: "STATUS_CODE_ERROR" };
const frame = (row: unknown[]) => ({ columns: [], values: row.map(value => [value]), rows: 1 });

beforeEach(() => panels.mockReset());

it("reads events from the parallel event lists", async () => {
  panels.mockResolvedValue([{ id: "span", status: "ok", elapsed_ms: 1, frame: frame([
    `{"http.route":"/cart"}`, `{"service.name":"checkout"}`,
    "[1500000,7]", `["exception","retry"]`, `[{"exception.type":"Error","exception.message":"boom","attempt":2},null]`, "otelhttp",
  ]) }]);
  const extras = await spanExtras("t'1", span);
  const { sql } = panels.mock.calls[0][0].dashboard.panels[0];
  expect(sql).toContain("event_times");
  expect(sql).toContain("event_names, event_attributes");
  expect(sql).not.toContain("events_json");
  expect(sql).toContain("trace_id = 't''1' AND span_id = 's''1'");
  expect(extras).toEqual({
    attributes: { "http.route": "/cart" }, resource: { "service.name": "checkout" }, scope: "otelhttp",
    events: [
      { offset_ns: 1500000, name: "exception", attributes: { "exception.type": "Error", "exception.message": "boom", attempt: 2 } },
      { offset_ns: 7, name: "retry", attributes: {} },
    ],
  });
});

it("returns no events for a span without them", async () => {
  panels.mockResolvedValue([{ id: "span", status: "ok", elapsed_ms: 1, frame: frame(["{}", "{}", "[]", "[]", "[]", null]) }]);
  await expect(spanExtras("t", span)).resolves.toEqual({ attributes: {}, resource: {}, events: [], scope: "" });
});

it("reports a span that is no longer stored", async () => {
  panels.mockResolvedValue([{ id: "span", status: "ok", elapsed_ms: 1, frame: { columns: [], values: [[], [], [], [], [], []], rows: 0 } }]);
  await expect(spanExtras("t", span)).rejects.toThrow("This span is no longer stored.");
});

it("reports invalid span detail queries as errors", async () => {
  panels.mockResolvedValue([{ id: "span", status: "invalid", elapsed_ms: 0, error: "unknown field", problems: [{ path: "panels[0].sql", message: "unknown field" }] }]);
  await expect(spanExtras("t", span)).rejects.toThrow("unknown field");
});
