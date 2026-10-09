import type { PanelResult } from "../../../panels/types";

/** Browser-only retry classification and original request panel IDs. */
export type PanelDisplayResult = PanelResult & { request_error?: readonly string[] };

/** Returned validation/query errors need a spec fix; execution timeouts can be retried. */
export function classifyPanelResult(result: PanelResult, sent: readonly string[]): PanelDisplayResult {
  if (result.status === "error" && (
    result.error === "Not run: the dashboard ran out of time. Narrow the time range or split the dashboard." ||
    result.error === "The query took longer than 10 seconds. Narrow the time range or add filters."
  )) return {...result, request_error: sent};
  return result;
}
