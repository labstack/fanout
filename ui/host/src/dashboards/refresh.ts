import { queryAnnotations, queryPanels, type QueryBody } from "./api";
import type { AnnotationsResponse } from "../../../panels/annotations";
import type { PanelResult } from "../../../panels/types";

/** Panels a busy server turned away rerun this many at a time. */
const busyGroup = 2;

/** onResult sees each panel as it finishes; annotations follow the last one.
 *  `busy` panels, which the server last left unrun, go after the rest in small
 *  groups, one request at a time, so each group gets the server's whole wait
 *  instead of queueing behind the same batch again. A group turned away again
 *  ends the refresh; the panels after it keep their last result. */
export async function refreshDashboard(body: QueryBody, signal?: AbortSignal, onResult?: (result: PanelResult) => void, busy: readonly string[] = []) {
  const later = busy.filter(id => body.panels?.includes(id));
  const first = body.panels?.filter(id => !later.includes(id));
  let results = later.length && !first?.length ? [] : await queryPanels(later.length ? { ...body, panels: first } : body, signal, onResult);
  for (let i = 0; i < later.length; i += busyGroup) {
    const group = await queryPanels({ ...body, panels: later.slice(i, i + busyGroup) }, signal, onResult);
    results = [...results, ...group];
    if (group.some(r => r.reason === "busy")) break;
  }
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
