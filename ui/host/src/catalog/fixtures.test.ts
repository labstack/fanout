import { expect, it } from "vitest";
import { results, services, serviceSpans, traceIDs, traceDetail } from "./fixtures";
import type { Cell } from "../../../panels/types";

function rows(id: string): Record<string, Cell>[] {
  const frame = results.find(result => result.id === id)!.frame!;
  return Array.from({ length: frame.rows }, (_, row) => Object.fromEntries(frame.columns.map((column, i) => [column.name, frame.values[i][row]])));
}

it("presents the same hourly service volumes and errors across health, tables and topology", () => {
  const health = rows("health"), performance = rows("services"), topology = rows("topology");
  services.forEach((service, i) => {
    const node = topology.find(row => row.kind === "node" && row.service === service)!;
    expect([health[i].spans, performance[i].requests, node.spans]).toEqual(Array(3).fill(serviceSpans[i]));
    expect([performance[i].error_rate, node.error_rate]).toEqual(Array(2).fill(health[i].error_rate));
  });
  expect(serviceSpans.reduce((sum, count) => sum + count, 0)).toBe(108900);
  expect(rows("requests")[0].count).toBe(108900);
});

it("accounts for every hourly span in the heatmap and its histogram", () => {
  const density = rows("density"), histogram = rows("distribution");
  expect(histogram.reduce((sum, row) => sum + Number(row.count), 0)).toBe(108900);
  for (const bucket of histogram) {
    expect(density.filter(row => row.bucket_lower === bucket.bucket_lower).reduce((sum, row) => sum + Number(row.count), 0)).toBe(bucket.count);
  }
});

it("keeps checkout latency flat until the rollout then steps to 342 ms", () => {
  const latency = rows("latency").filter(row => row.service === "checkout");
  const rollout = Date.parse("2026-10-21T19:20:00Z");
  expect(new Set(latency.filter(row => Number(row.time) <= rollout).map(row => row.p95))).toEqual(new Set([188]));
  expect(new Set(latency.filter(row => Number(row.time) > rollout).map(row => row.p95))).toEqual(new Set([342]));
});

it("keeps fictional OTLP trace IDs distinct when shortened and correlated with logs", () => {
  expect(traceIDs.every(id => /^[0-9a-f]{32}$/.test(id))).toBe(true);
  expect(new Set(traceIDs.map(id => id.slice(0, 16))).size).toBe(traceIDs.length);
  expect(rows("logs").map(row => row.trace_id)).toEqual(traceIDs);
  for (const trace of rows("traces")) expect(rows("logs").some(log => log.trace_id === trace.trace_id)).toBe(true);
  expect(traceDetail.data.trace_id).toBe(rows("traces")[0].trace_id);
  expect(traceDetail.data.spans.every(span => /^[0-9a-f]{16}$/.test(span.span_id))).toBe(true);
  expect(traceDetail.data.logs[0]).toMatchObject({ trace_id: traceIDs[0], body: rows("logs")[0].body });
  expect(rows("patterns").reduce((sum, row) => sum + Number(row.count), 0)).toBeLessThanOrEqual(Number(rows("health")[0].log_count));
  expect(rows("patterns").some(row => row.body_template === "Inventory reservation timed out after <*> ms")).toBe(true);
});
