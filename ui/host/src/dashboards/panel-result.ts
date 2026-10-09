import type { PanelResult } from "../../../panels/types";

/** Browser-only provenance for failures outside the panel-result response. */
export type PanelDisplayResult = PanelResult & { request_error?: readonly string[] };
