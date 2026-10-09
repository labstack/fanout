import type { PanelResult } from "../../../panels/types";

/** Browser-only retry classification and original request panel IDs. */
export type PanelDisplayResult = PanelResult & { request_error?: readonly string[] };

/** The server decides which returned execution errors can be retried. */
export function classifyPanelResult(result: PanelResult, sent: readonly string[]): PanelDisplayResult {
  if (result.status === "error" && result.retryable) return {...result, request_error: sent};
  return result;
}
