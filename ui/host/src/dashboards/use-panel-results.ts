import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import type { DashboardSpec, DashboardTime, PanelResult, VarValue } from "../../../panels/types";
import { panelContent, retryPanelQuery } from "./query-policy";
import { refreshDashboard } from "./refresh";

const refreshMs: Record<string, number | false> = { off: false, "10s": 10_000, "30s": 30_000, "1m": 60_000, "5m": 300_000 };

/** One panel batch plus one annotation request per refresh for every visible panel. Results
 *  of panels scrolled out of view are kept from their last fetch. Widths are
 *  rounded for the next refresh. Layout and width changes do not refetch. */
export function usePanelResults({ dashboardId, spec, time, vars, compare, widths, visible, refresh, enabled = true }: {
  dashboardId: string; version: number; spec: DashboardSpec; time: DashboardTime; vars: Record<string, VarValue>;
  compare: boolean; widths: Record<string, number>; visible: string[]; refresh: string; enabled?: boolean;
}) {
  const rounded = useMemo(() => Object.fromEntries(Object.entries(widths).map(([id, w]) => [id, Math.max(100, Math.round(w / 100) * 100)])), [widths]);
  // Visibility decides which panels a refresh asks for, but is not part of
  // the key: scrolling must not refetch, and the first load asks for all.
  const key = JSON.stringify([dashboardId, panelContent(spec), time, vars, compare]);
  const ids = visible.filter((id) => spec.panels.some((panel) => panel.id === id));
  const inFlight = useRef<string[]>([]);
  const [kept, setKept] = useState<Snapshot>({ key, results: new Map(), updated: new Map(), stale: new Set(), receivedAt: 0 });
  const canQuery = enabled && spec.panels.some((p) => p.viz !== "text");
  const query = useQuery({
    queryKey: ["panels", key],
    queryFn: ({ signal }) => {
      // An empty list means all panels to the server. Track the same batch.
      inFlight.current = (ids.length ? [...ids] : spec.panels.map((panel) => panel.id)).sort();
      return refreshDashboard({ dashboard: spec, panels: inFlight.current, time, vars, widths: rounded, compare }, signal);
    },
    retry: retryPanelQuery,
    enabled: canQuery,
    refetchInterval: refreshMs[refresh] ?? 30_000,
    staleTime: 5_000,
    placeholderData: (previous) => previous,
  });
  const snapshot = useMemo(() => merge(kept, key, query.isPlaceholderData ? undefined : query.data?.results, query.dataUpdatedAt, query.error ? inFlight.current : []),
    [kept, key, query.data, query.dataUpdatedAt, query.isPlaceholderData, query.error, query.errorUpdatedAt]);
  const results = snapshot.results;
  useEffect(() => {
    if (query.isPlaceholderData) return;
    setKept((previous) => merge(previous, key, query.data?.results, query.dataUpdatedAt, query.error ? inFlight.current : []));
  }, [key, query.data, query.dataUpdatedAt, query.isPlaceholderData, query.error, query.errorUpdatedAt]);
  const staleAt = new Map([...snapshot.stale].flatMap((id) => { const at = snapshot.updated.get(id); return at ? [[id, at] as const] : []; }));
  const panelError = (query.data?.results ?? []).find((r) => r.status === "error" && snapshot.stale.has(r.id));
  // A panel scrolled into view that has never loaded under this key is
  // fetched now rather than at the next refresh.
  useEffect(() => {
    if (canQuery && !query.isFetching && ids.some((id) => !results.has(id) && spec.panels.find((p) => p.id === id)?.viz !== "text")) void query.refetch();
  }, [visible]);
  return { results, staleAt, annotations: query.isPlaceholderData ? undefined : query.data?.annotations, annotationError: query.isPlaceholderData ? undefined : query.data?.annotation_error, fetchingIds: query.isFetching ? inFlight.current : [], fetching: query.isFetching, error: query.error ?? (panelError ? new Error(panelError.error ?? "Panel refresh failed") : null), updatedAt: query.isPlaceholderData ? null : query.dataUpdatedAt || null, refetch: () => { if (canQuery) void query.refetch(); } };
}

type Snapshot = { key: string; results: Map<string, PanelResult>; updated: Map<string, number>; stale: Set<string>; receivedAt: number };
function merge(previous: Snapshot, key: string, data: PanelResult[] | undefined, at: number, failed: string[]): Snapshot {
  const next: Snapshot = previous.key === key
    ? { key, results: new Map(previous.results), updated: new Map(previous.updated), stale: new Set(previous.stale), receivedAt: previous.receivedAt }
    : { key, results: new Map(), updated: new Map(), stale: new Set(), receivedAt: 0 };
  for (const result of data && at !== next.receivedAt ? data : []) {
    if (result.status === "error" && next.results.get(result.id)?.frame) next.stale.add(result.id);
    else { next.results.set(result.id, result); next.updated.set(result.id, at); next.stale.delete(result.id); }
  }
  if (data) next.receivedAt = at;
  for (const id of failed) if (next.results.get(id)?.frame) next.stale.add(id);
  return next;
}
