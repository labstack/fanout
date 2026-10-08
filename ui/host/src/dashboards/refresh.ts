import { queryAnnotations, queryPanels, type QueryBody } from "./api";
import type { AnnotationsResponse } from "../../../panels/annotations";

export async function refreshDashboard(body: QueryBody, signal?: AbortSignal) {
  const results = await queryPanels(body, signal);
  const timePanels = new Set(body.dashboard.panels.filter(p => ["timeseries", "heatmap", "state_timeline"].includes(p.viz)).map(p => p.id));
  const windows = results.filter(r => timePanels.has(r.id) && r.from_ms !== undefined && r.to_ms !== undefined && r.from_ms < r.to_ms);
  if (windows.length === 0 || body.dashboard.annotations?.deploys === false && body.dashboard.annotations?.anomalies === false) return { results };
  const from = Math.min(...windows.map(r => r.from_ms!)), to = Math.max(...windows.map(r => r.to_ms!));
  try {
    const annotations: AnnotationsResponse = await queryAnnotations({ from: new Date(from).toISOString(), to: new Date(to).toISOString() }, signal);
    return { results, annotations };
  } catch (error) {
    if (signal?.aborted) throw error;
    return { results, annotation_error: error instanceof Error ? error.message : "Annotations unavailable" };
  }
}
