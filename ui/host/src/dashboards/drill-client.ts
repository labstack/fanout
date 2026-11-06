import type { Result, TraceDetail } from "../../../contracts";
import type { ExemplarBody, ExemplarResponse } from "./api";
import type { DrillTarget } from "./drill-state";
import type { SpanLoader } from "./trace/span-details";
export type DrillClient = {
  exemplars(body: ExemplarBody, signal?: AbortSignal): Promise<ExemplarResponse>;
  trace(target: DrillTarget, signal?: AbortSignal): Promise<Result<TraceDetail>>;
  /** Reads a selected span's attributes and events; without it span details show only what the trace carries. */
  span?: SpanLoader;
};
