import { describe, expect, it } from "vitest";
import type { TraceSpan } from "../../../../contracts";
import { criticalPathSegments } from "./critical-path";

const span = (id: string, start: number, end: number, parent?: string): TraceSpan => ({
  span_id: id, parent_span_id: parent, start: new Date(start).toISOString().replace(/\.\d+Z$/, `.${String(Math.round(((start % 1000 + 1000) % 1000) * 1_000_000)).padStart(9, "0")}Z`), duration_ms: end - start,
  operation: id, service: "service", kind: "SPAN_KIND_INTERNAL", status: "STATUS_CODE_OK",
});
const segment = (span_id: string, start: number, end: number) => ({ span_id, start, end });
const path = (spans: TraceSpan[]) => criticalPathSegments(spans).sort((a, b) => a.start - b.start);

describe("criticalPathSegments", () => {
  it("returns no segments for an empty trace", () => expect(path([])).toEqual([]));
  it("attributes a single span's duration to itself", () => expect(path([span("r", 0, 100)])).toEqual([segment("r", 0, 100)]));
  it("resumes the parent around sequential children regardless of input order", () => {
    expect(path([span("b", 50, 80, "r"), span("r", 0, 100), span("a", 10, 30, "r")])).toEqual([
      segment("r", 0, 10), segment("a", 10, 30), segment("r", 30, 50), segment("b", 50, 80), segment("r", 80, 100),
    ]);
  });
  it("keeps sub-millisecond starts and fractional durations on the same clock", () => {
    const spans = [span("r", 0, 5), span("a", 1, 3.5, "r"), span("b", 3.6, 4.9, "r")];
    // Use a modern epoch, where absolute millisecond doubles also lose precision.
    for (const item of spans) item.start = item.start.replace("1970-01-01", "2026-11-07");
    const result = path(spans);
    const expected = [segment("r", 0, 1), segment("a", 1, 3.5), segment("r", 3.5, 3.6), segment("b", 3.6, 4.9), segment("r", 4.9, 5)];
    expect(result.map(item => item.span_id)).toEqual(expected.map(item => item.span_id));
    result.forEach((item, i) => {
      expect(item.start).toBeCloseTo(expected[i].start, 9);
      expect(item.end).toBeCloseTo(expected[i].end, 9);
    });
  });
  it("includes back-to-back blocking siblings", () => {
    expect(path([span("r", 0, 100), span("a", 10, 50, "r"), span("b", 50, 80, "r")])).toEqual([
      segment("r", 0, 10), segment("a", 10, 50), segment("b", 50, 80), segment("r", 80, 100),
    ]);
  });
  it("excludes a consumer subtree from its producer's blocking children", () => {
    const producer = { ...span("p", 10, 90, "r"), kind: "SPAN_KIND_PRODUCER" };
    const consumer = { ...span("c", 20, 80, "p"), kind: "SPAN_KIND_CONSUMER" };
    expect(path([span("r", 0, 100), producer, consumer, span("work", 30, 70, "c")])).toEqual([
      segment("r", 0, 10), segment("p", 10, 90), segment("r", 90, 100),
    ]);
    // Consumers remain blocking children of other span kinds.
    expect(path([span("p", 0, 100), consumer])).toEqual([
      segment("p", 0, 20), segment("c", 20, 80), segment("p", 80, 100),
    ]);
  });
  it("selects the latest ending parallel child and skips overlapping siblings", () => {
    expect(path([span("r", 0, 100), span("a", 10, 70, "r"), span("b", 30, 90, "r")])).toEqual([
      segment("r", 0, 30), segment("b", 30, 90), segment("r", 90, 100),
    ]);
  });
  it("clamps async children and their descendants at the root end", () => {
    expect(path([span("r", 0, 100), span("a", 20, 150, "r"), span("b", 50, 200, "a")])).toEqual([
      segment("r", 0, 20), segment("a", 20, 50), segment("b", 50, 100),
    ]);
  });
  it("descends into nested children then resumes earlier siblings", () => {
    expect(path([span("r", 0, 100), span("a", 10, 20, "r"), span("b", 30, 90, "r"), span("c", 40, 70, "b")])).toEqual([
      segment("r", 0, 10), segment("a", 10, 20), segment("r", 20, 30), segment("b", 30, 40), segment("c", 40, 70), segment("b", 70, 90), segment("r", 90, 100),
    ]);
  });
  it("clamps clock skew and ignores negative, zero and disjoint durations", () => {
    expect(path([span("r", 0, 100), span("a", -20, 30, "r"), span("b", 110, 150, "r"), span("z", 50, 50, "r"), span("n", 60, 40, "r")])).toEqual([
      segment("a", 20, 50), segment("r", 50, 120),
    ]);
    expect(path([span("r", 0, 0)])).toEqual([]);
    expect(path([span("r", 20, 0)])).toEqual([]);
  });
  it("treats missing parents as roots and picks the longest root duration", () => {
    expect(path([span("small", 0, 20), span("orphan", 50, 150, "missing"), span("child", 60, 140, "orphan")])).toEqual([
      segment("orphan", 50, 60), segment("child", 60, 140), segment("orphan", 140, 150),
    ]);
  });
  it("handles equal-end async children deterministically", () => {
    expect(path([span("r", 0, 100), span("a", 10, 150, "r"), span("b", 30, 120, "r")])).toEqual([
      segment("r", 0, 30), segment("b", 30, 100),
    ]);
  });
  it("ignores rootless cycles and treats self parents as roots", () => {
    expect(path([span("a", 0, 100, "b"), span("b", 0, 100, "a")])).toEqual([]);
    expect(path([span("a", 0, 100, "a")])).toEqual([segment("a", 0, 100)]);
  });
  it("handles ten thousand siblings", () => {
    const children = Array.from({ length: 10000 }, (_, i) => span(String(i), i + 1, i + 2, "r"));
    const result = path([span("r", 0, 10002), ...children.reverse()]);
    expect(result).toHaveLength(10002);
    expect(result[0]).toEqual(segment("r", 0, 1));
    expect(result.at(-1)).toEqual(segment("r", 10001, 10002));
    expect(result.every((item, i) => !i || result[i - 1].end === item.start)).toBe(true);
  });
  it("partitions the root contiguously for seeded random trees", () => {
    let seed = 0x51a7cafe;
    const random = () => { seed = (Math.imul(seed, 1664525) + 1013904223) >>> 0; return seed / 2 ** 32; };
    for (let trial = 0; trial < 200; trial++) {
      const spans = [span("r", 0, 1000)];
      for (let i = 0; i < 100; i++) {
        const start = Math.floor(random() * 1100);
        const parent = spans[Math.floor(random() * spans.length)];
        spans.push(span(String(i), start, start + Math.floor(random() * 500), parent.span_id));
      }
      const result = path(spans);
      expect(result[0].start).toBe(0);
      expect(result.at(-1)!.end).toBe(1000);
      expect(result.every((item, i) => item.end > item.start && (!i || result[i - 1].end === item.start))).toBe(true);
      expect(result.reduce((sum, item) => sum + item.end - item.start, 0)).toBe(1000);
    }
  });
  it("handles deep traces without recursion and partitions the root duration", () => {
    const spans = Array.from({ length: 20000 }, (_, i) => span(String(i), i, 40000 - i, i ? String(i - 1) : undefined));
    const result = path(spans);
    expect(result.reduce((sum, s) => sum + s.end - s.start, 0)).toBe(40000);
    expect(result.every((s, i) => s.end > s.start && (!i || result[i - 1].end === s.start))).toBe(true);
  });
});
