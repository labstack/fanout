import type { TraceSpan } from "../../../../contracts";

/** Parse RFC3339 nanoseconds before converting to trace-relative milliseconds.
 * Subtracting the minimum as integers keeps epoch-sized doubles from rounding
 * away sub-microsecond differences between spans.
 */
export function spanStarts(spans: readonly TraceSpan[]): Map<TraceSpan, number> {
  const timestamps = spans.map(span => {
    const fraction = span.start.match(/\.(\d+)(?=Z|[+-]\d{2}:\d{2}$)/);
    const seconds = fraction ? span.start.replace(fraction[0], "") : span.start;
    const milliseconds = Date.parse(seconds);
    // A malformed start places that row at the trace start instead of throwing
    // and blanking the whole waterfall; it never shifts the valid spans.
    if (!Number.isFinite(milliseconds)) return undefined;
    return BigInt(milliseconds) * 1_000_000n + BigInt((fraction?.[1] ?? "").slice(0, 9).padEnd(9, "0"));
  });
  const valid = timestamps.filter((time): time is bigint => time !== undefined);
  const minimum = valid.reduce((min, time) => time < min ? time : min, valid[0] ?? 0n);
  return new Map(spans.map((span, i) => [span, Number((timestamps[i] ?? minimum) - minimum) / 1_000_000]));
}
