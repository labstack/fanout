import { describe, expect, it } from "vitest";
import { contrastRatio } from "@labstack/ui";
import type { TraceSpan } from "../../../../contracts";
import { brand, surfaces } from "../../../../tokens";
import { criticalPathSegments } from "./critical-path";

const span = (id: string, start: number, end: number, parent?: string): TraceSpan => ({
  span_id: id, parent_span_id: parent, start: new Date(start).toISOString(), duration_ms: end - start,
  operation: id, service: "service", kind: "internal", status: "STATUS_CODE_OK",
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
      segment("a", 0, 30), segment("r", 30, 100),
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
  it("handles deep traces without recursion and partitions the root duration", () => {
    const spans = Array.from({ length: 20000 }, (_, i) => span(String(i), i, 40000 - i, i ? String(i - 1) : undefined));
    const result = path(spans);
    expect(result.reduce((sum, s) => sum + s.end - s.start, 0)).toBe(40000);
    expect(result.every((s, i) => s.end > s.start && (!i || result[i - 1].end === s.start))).toBe(true);
  });
});

it("keeps the critical path accent above 3:1 against waterfall surfaces in both schemes", () => {
  for (const scheme of ["light", "dark"] as const) {
    for (const surface of ["hover", "raised", "ground"] as const) {
      expect(contrastRatio(brand[scheme === "dark" ? 5 : 7], surfaces[scheme][surface])).toBeGreaterThanOrEqual(3);
    }
  }
});
