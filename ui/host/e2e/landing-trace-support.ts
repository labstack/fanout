import type { TraceDetail } from "../../contracts";
import { pageItems } from "../src/dashboards/trace/pagination";

interface TracePageScore {
  traceID: string;
  coverage: number;
  startSpread: number;
  serviceCount: number;
  fitsOnePage: boolean;
}

export function scoreTracePage(trace: TraceDetail): TracePageScore | null {
  if (trace.truncated || trace.spans.length !== trace.span_count || trace.has_error
    || trace.spans.some(span => span.status.toUpperCase().includes("ERROR"))
    || !Number.isFinite(trace.duration_ms) || trace.duration_ms <= 0) return null;

  const firstPage = pageItems(trace.spans);
  const serviceCount = new Set(firstPage.map(span => span.service)).size;
  if (serviceCount < 3) return null;

  const traceStart = Math.min(...trace.spans.map(span => new Date(span.start).valueOf()));
  const intervals = firstPage.map(span => {
    const start = new Date(span.start).valueOf() - traceStart;
    return { start, end: start + span.duration_ms };
  }).sort((a, b) => a.start - b.start);
  let covered = 0, coveredUntil = 0;
  for (const { start, end } of intervals) {
    covered += Math.max(0, end - Math.max(start, coveredUntil));
    coveredUntil = Math.max(coveredUntil, end);
  }
  return {
    traceID: trace.trace_id,
    coverage: covered / trace.duration_ms,
    startSpread: (intervals.at(-1)!.start - intervals[0].start) / trace.duration_ms,
    serviceCount,
    fitsOnePage: firstPage.length === trace.spans.length,
  };
}

export function compareTracePages(a: TracePageScore, b: TracePageScore): number {
  // Equal weight for occupied time and distributed starts: a long root alone
  // can fill the timeline while the other seven bars cluster at its left edge.
  return (b.coverage + b.startSpread) - (a.coverage + a.startSpread)
    || Number(b.fitsOnePage) - Number(a.fitsOnePage)
    || b.serviceCount - a.serviceCount
    || a.traceID.localeCompare(b.traceID);
}
