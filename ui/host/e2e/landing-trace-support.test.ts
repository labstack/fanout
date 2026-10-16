import { expect, it } from "vitest";
import type { TraceDetail, TraceSpan } from "../../contracts";
import { pageItems, TRACE_PAGE_SIZE } from "../src/dashboards/trace/pagination";
import { compareTracePages, scoreTracePage } from "./landing-trace-support";

function span(id: string, start: number, duration: number, service = id): TraceSpan {
  return {
    span_id: id, service, operation: id, kind: "SERVER", status: "OK",
    start: new Date(Date.UTC(2026, 9, 1) + start).toISOString(), duration_ms: duration,
  };
}

function trace(spans: TraceSpan[], overrides: Partial<TraceDetail> = {}): TraceDetail {
  const services = [...new Set(spans.map(span => span.service))];
  return {
    trace_id: "trace", duration_ms: 100, has_error: false, services, spans, logs: [],
    span_count: spans.length, service_count: services.length, truncated: false, ...overrides,
  };
}

it("scores the union of overlapping intervals without counting gaps or nesting twice", () => {
  const data = trace([span("a", 10, 30), span("b", 0, 20), span("c", 70, 10), span("d", 12, 3)]);
  const original = [...data.spans];
  expect(scoreTracePage(data)).toEqual({
    traceID: "trace", coverage: 0.5, startSpread: 0.7, serviceCount: 4, fitsOnePage: true,
  });
  expect(data.spans).toEqual(original);
});

it("uses the UI's first page in server order, even when later spans start earlier", () => {
  const spans = Array.from({ length: TRACE_PAGE_SIZE }, (_, i) => span(`first-${i}`, 40 + i, 10, `service-${i % 3}`));
  spans.push(span("later", 0, 100));
  expect(pageItems(spans)).toEqual(spans.slice(0, TRACE_PAGE_SIZE));
  expect(pageItems(spans, 2)).toEqual([spans.at(-1)]);
  expect(scoreTracePage(trace(spans))).toEqual({
    traceID: "trace", coverage: 0.17, startSpread: 0.07, serviceCount: 3, fitsOnePage: false,
  });
  const tooFewServices = spans.map((item, i) => i < TRACE_PAGE_SIZE ? { ...item, service: "only" } : item);
  expect(scoreTracePage(trace(tooFewServices))).toBeNull();
});

it("normalizes coverage and start spread by trace duration", () => {
  const data = trace([span("a", 0, 20), span("b", 40, 10), span("c", 90, 10)]);
  const scaled = trace([span("a", 0, 200), span("b", 400, 100), span("c", 900, 100)], { duration_ms: 1000 });
  expect(scoreTracePage(data)).toEqual(scoreTracePage(scaled));
});

it("prefers a distributed single-page trace over a long root and seven early slivers", () => {
  const clustered = [span("root", 0, 834), ...Array.from({ length: 7 }, (_, i) => span(`early-${i}`, i + 1, 1))];
  clustered.push(...Array.from({ length: 186 }, (_, i) => span(`later-${i}`, 100 + i, 1)));
  const busy = scoreTracePage(trace(clustered, { trace_id: "large", duration_ms: 834 }))!;
  const distributed = scoreTracePage(trace([span("a", 0, 100), span("b", 30, 30), span("c", 70, 30)]))!;
  expect(busy.coverage).toBe(1);
  expect(distributed.coverage).toBe(1);
  expect(compareTracePages(distributed, busy)).toBeLessThan(0);
});

it("rejects truncated, incomplete and erroneous traces, including errors beyond page one", () => {
  const spans = [span("a", 0, 100), span("b", 30, 30), span("c", 70, 30)];
  for (const overrides of [{ truncated: true }, { span_count: 4 }, { has_error: true }, { duration_ms: 0 }]) {
    expect(scoreTracePage(trace(spans, overrides))).toBeNull();
  }
  const withError = [...spans, ...Array.from({ length: TRACE_PAGE_SIZE }, (_, i) => span(`extra-${i}`, 1, 1))];
  withError[withError.length - 1].status = "STATUS_CODE_ERROR";
  expect(scoreTracePage(trace(withError))).toBeNull();
});

it("prefers one page on equal visual scores, then page services and trace ID", () => {
  const spans = [span("a", 0, 100), span("b", 30, 30), span("c", 70, 30)];
  const single = scoreTracePage(trace(spans))!;
  const multiple = scoreTracePage(trace([...spans, ...Array.from({ length: TRACE_PAGE_SIZE }, (_, i) => span(`extra-${i}`, 30, 1))]))!;
  expect(single.coverage + single.startSpread).toBe(multiple.coverage + multiple.startSpread);
  expect(compareTracePages(single, multiple)).toBeLessThan(0);
  const moreServices = scoreTracePage(trace([...spans, span("d", 40, 1)]))!;
  expect(compareTracePages(moreServices, single)).toBeLessThan(0);
  const earlierID = scoreTracePage(trace(spans, { trace_id: "aaa" }))!;
  const laterID = scoreTracePage(trace(spans, { trace_id: "zzz" }))!;
  expect([laterID, earlierID].sort(compareTracePages)).toEqual([earlierID, laterID]);
  expect(compareTracePages(single, single)).toBe(0);
});
