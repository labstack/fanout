import type { Result, TraceDetail } from "../../../contracts";
import type { ExemplarBody, ExemplarResponse } from "./api";
import type { DrillTarget } from "./drill-state";
export type DrillClient = {
  exemplars(body: ExemplarBody, signal?: AbortSignal): Promise<ExemplarResponse>;
  trace(target: DrillTarget, signal?: AbortSignal): Promise<Result<TraceDetail>>;
};
