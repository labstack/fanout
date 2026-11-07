import type { TraceSpan } from "../../../../contracts";

/** Parse RFC3339 nanoseconds before converting to trace-relative milliseconds.
 * Subtracting the minimum as integers keeps epoch-sized doubles from rounding
 * away sub-microsecond differences between spans.
 */
export function spanStarts(spans: readonly TraceSpan[]): Map<TraceSpan, number> {
  const timestamps = spans.map(span => {
    const fraction = span.start.match(/\.(\d+)(?=Z|[+-]\d{2}:\d{2}$)/);
    const seconds = fraction ? span.start.replace(fraction[0], "") : span.start;
    return BigInt(Date.parse(seconds)) * 1_000_000n + BigInt((fraction?.[1] ?? "").padEnd(9, "0"));
  });
  const minimum = timestamps.reduce((min, time) => time < min ? time : min, timestamps[0] ?? 0n);
  return new Map(spans.map((span, i) => [span, Number(timestamps[i] - minimum) / 1_000_000]));
}
