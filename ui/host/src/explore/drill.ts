import { getTrace, queryExemplars } from "../dashboards/api";
import type { DrillClient } from "../dashboards/drill-client";
import { spanExtras } from "../dashboards/trace/span-extras";

/** The dashboard drill drawers, fed by a transient one-panel spec on Explore, Logs and Traces. */
export const exploreDrillClient: DrillClient = { exemplars: queryExemplars, trace: getTrace, span: spanExtras };
