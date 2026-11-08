import { expect, it } from "vitest";
import type { TraceSpan } from "../../../../contracts";
import { spanStarts } from "./span-timing";

it("preserves nanosecond differences across second boundaries and RFC3339 offsets", () => {
  const spans = ["2026-11-07T00:00:01.000000100Z", "2026-11-06T16:00:00.999999900-08:00", "2026-11-07T00:00:01Z"].map(start => ({ start }) as TraceSpan);
  expect([...spanStarts(spans).values()]).toEqual([0.0002, 0, 0.0001]);
  expect([...spanStarts([]).values()]).toEqual([]);
});

it("places a malformed start at the trace start without shifting valid spans, and truncates extra fraction digits", () => {
  const spans = ["2026-11-07T00:00:00.0000000019Z", "not a time", "2026-11-07T00:00:00.5Z"].map(start => ({ start }) as TraceSpan);
  expect([...spanStarts(spans).values()]).toEqual([0, 0, 499.999999]);
});
