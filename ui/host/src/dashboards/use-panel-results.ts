import { useQuery } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import type { DashboardSpec, DashboardTime, VarValue } from "../../../panels/types";
import { classifyPanelResult, type PanelDisplayResult } from "./panel-result";
import { ApiError } from "./api";
import { retryPanelQuery } from "./query-policy";
import { panelResultsKey } from "./query-keys";
import { refreshDashboard } from "./refresh";
import type { AnnotationsResponse } from "../../../panels/annotations";

const refreshMs: Record<string, number | false> = { off: false, "10s": 10_000, "30s": 30_000, "1m": 60_000, "5m": 300_000 };
// Keep completion IDs distinct across cached-query remounts and same-millisecond
// partial batches. Timestamps remain solely the age of the last successful frame.
let completedBatch = 0;

export function panelResultsForDashboard(query: { queryKey: readonly unknown[] }, id: string): boolean {
  const [kind, scope] = query.queryKey;
  if (kind !== "panels" || typeof scope !== "string") return false;
  try { return JSON.parse(scope)[0] === id; } catch { return false; }
}

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
  const queryKey = panelResultsKey(dashboardId, spec, time, vars, compare);
  const key = queryKey[1];
  const ids = visible.filter((id) => spec.panels.some((panel) => panel.id === id));
  const inFlight = useRef<string[]>([]);
  const failedBatch = useRef<{key: string; ids: string[]; error: string; retryable: boolean} | undefined>(undefined);
  const lazyBatch = useRef<{ key: string; ids: string[] } | null>(null);
  // A manual refresh pressed while a partial lazy batch is in flight runs once
  // that batch settles; during a full refresh it is already covered.
  const queuedRefresh = useRef(false);
  const queuedRetry = useRef<{key: string; ids: Set<string>} | null>(null);
  const requested = useRef<{ key: string; ids: Set<string> }>({ key, ids: new Set() });
  const [keptAnnotations, setKeptAnnotations] = useState<{key:string;annotations?:AnnotationsResponse;annotation_error?:string}>({key});
  const [kept, setKept] = useState<Snapshot>({ key, results: new Map(), updated: new Map(), stale: new Set(), receivedAt: 0 });
  const knownEmpty = ids.length === 0 && kept.key === key && kept.receivedAt > 0;
  const canQuery = enabled && spec.panels.some((p) => p.viz !== "text");
  const query = useQuery({
    queryKey,
    queryFn: ({ signal }): Promise<Omit<Awaited<ReturnType<typeof refreshDashboard>>, "results"> & {results: PanelDisplayResult[]; hasTimePanels: boolean; batch: number}> => {
      if (knownEmpty && lazyBatch.current?.key !== key) {
        inFlight.current = [];
        return Promise.resolve({ results: [], hasTimePanels: false, batch: ++completedBatch });
      }
      const batch = lazyBatch.current?.key === key ? lazyBatch.current.ids : ids;
      // An empty list means all panels to the server. Track the same batch.
      inFlight.current = (batch.length ? [...batch] : spec.panels.map((panel) => panel.id)).sort();
      if (requested.current.key !== key) requested.current = { key, ids: new Set() };
      for (const id of inFlight.current) requested.current.ids.add(id);
      const sent = [...inFlight.current];
      return refreshDashboard({ dashboard: spec, panels: sent, time, vars, widths: rounded, compare }, signal).then(data => {
        const returned = new Set(data.results.map(result => result.id));
        const missing: PanelDisplayResult[] = sent.filter(id => !returned.has(id) && spec.panels.find(p => p.id === id)?.viz !== "text")
          .map(id => ({id,status:"error",elapsed_ms:0,error:"No result was returned for this panel; refresh to retry.",request_error:sent}));
        return {...data,batch:++completedBatch,results:[...data.results.map(result => classifyPanelResult(result, sent)),...missing],hasTimePanels:spec.panels.some(p => sent.includes(p.id) && ["timeseries","heatmap","state_timeline"].includes(p.viz))};
      }).catch(error => {
        if (!signal.aborted) failedBatch.current = {key, ids: sent.filter(id => spec.panels.find(panel => panel.id === id)?.viz !== "text"), error: error instanceof Error ? error.message : String(error), retryable: !(error instanceof ApiError) || error.status === 408 || error.status === 429 || error.status >= 500};
        throw error;
      });
    },
    retry: retryPanelQuery,
    enabled: canQuery,
    refetchInterval: knownEmpty ? false : refreshMs[refresh] ?? 30_000,
    staleTime: 5_000,
    placeholderData: (previous) => previous,
  });
  const failure = query.error && failedBatch.current?.key === key ? failedBatch.current : undefined;
  const snapshot = useMemo(() => merge(kept, key, query.isPlaceholderData ? undefined : query.data?.results, query.dataUpdatedAt, query.data?.batch, failure),
    [kept, key, query.data, query.dataUpdatedAt, query.isPlaceholderData, query.error, query.errorUpdatedAt]);
  const results = snapshot.results;
  const annotationSnapshot = query.isPlaceholderData ? undefined : query.data?.hasTimePanels ? query.data : keptAnnotations.key === key ? keptAnnotations : undefined;
  useEffect(() => {
    if (!query.isPlaceholderData && query.data?.hasTimePanels) setKeptAnnotations({key,annotations:query.data.annotations,annotation_error:query.data.annotation_error});
  }, [key, query.data, query.isPlaceholderData]);
  useEffect(() => {
    if (query.isPlaceholderData) return;
    setKept((previous) => merge(previous, key, query.data?.results, query.dataUpdatedAt, query.data?.batch, failure));
  }, [key, query.data, query.dataUpdatedAt, query.isPlaceholderData, query.error, query.errorUpdatedAt]);
  const staleAt = new Map([...snapshot.stale].flatMap((id) => { const at = snapshot.updated.get(id); return at ? [[id, at] as const] : []; }));
  const panelError = (query.data?.results ?? []).find((r) => r.status === "error" && snapshot.stale.has(r.id));
  // A panel scrolled into view that has never loaded under this key is
  // fetched now, including when visibility changed during the previous batch.
  useEffect(() => {
    // Never repeat attempted panels, but an older failure cannot block new panels.
    if (!canQuery || query.isFetching || queuedRefresh.current || queuedRetry.current?.key === key || lazyBatch.current?.key === key) return;
    const attempted = requested.current.key === key ? requested.current.ids : new Set<string>();
    const missing = [...new Set(ids.filter((id) => !results.has(id) && !attempted.has(id) && spec.panels.find((p) => p.id === id)?.viz !== "text"))].sort();
    if (!missing.length) return;
    const batch = { key, ids: missing };
    lazyBatch.current = batch;
    void query.refetch({ cancelRefetch: false }).finally(() => {
      if (lazyBatch.current === batch) lazyBatch.current = null;
    });
  }, [visible, canQuery, key, query.isFetching, query.error, query.refetch, results, spec.panels]);
  useEffect(() => {
    if (query.isFetching || queuedRetry.current?.key === key || !queuedRefresh.current) return;
    queuedRefresh.current = false;
    lazyBatch.current = null;
    if (canQuery && !knownEmpty) void query.refetch({ cancelRefetch: false });
  }, [query.isFetching, key, canQuery, knownEmpty, query.refetch]);
  const sendRetry = (ids: string[]) => {
    const batch = {key, ids}; lazyBatch.current = batch;
    void query.refetch({cancelRefetch: false}).finally(() => { if (lazyBatch.current === batch) lazyBatch.current = null; });
  };
  useEffect(() => {
    if (query.isFetching || !queuedRetry.current) return;
    const queued = queuedRetry.current; queuedRetry.current = null;
    if (queued.key !== key || !canQuery) return;
    const ids = [...queued.ids].filter(id => spec.panels.some(panel => panel.id === id && panel.viz !== "text") && results.get(id)?.request_error).sort();
    if (ids.length) sendRetry(ids);
  }, [query.isFetching, key, canQuery, results, spec.panels]);
  const retry = (panelId?: string) => {
    if (!canQuery) return;
    const batchIds = panelId ? results.get(panelId)?.request_error ?? [] : [...results.values()].flatMap(result => result.request_error ?? []);
    const failed = [...new Set(batchIds)].filter(id => spec.panels.some(panel => panel.id === id && panel.viz !== "text") && results.get(id)?.request_error).sort();
    if (!failed.length) return;
    if (query.isFetching) {
      if (queuedRetry.current?.key !== key) queuedRetry.current = {key, ids: new Set()};
      for (const id of failed) queuedRetry.current.ids.add(id);
      return;
    }
    sendRetry(failed);
  };
  return { retry, results, staleAt, annotations: annotationSnapshot?.annotations, annotationError: annotationSnapshot?.annotation_error, fetchingIds: query.isFetching ? inFlight.current : [], fetching: query.isFetching, error: query.error ?? (panelError ? new Error(panelError.error ?? "Panel refresh failed") : null), updatedAt: query.isPlaceholderData ? null : query.dataUpdatedAt || null, refetch: () => { if (!canQuery || knownEmpty) return; if (query.isFetching) { if (lazyBatch.current?.key === key) queuedRefresh.current = true; return; } void query.refetch({ cancelRefetch: false }); } };
}

type Snapshot = { key: string; results: Map<string, PanelDisplayResult>; updated: Map<string, number>; stale: Set<string>; receivedAt: number; receivedBatch?: number };
function merge(previous: Snapshot, key: string, data: PanelDisplayResult[] | undefined, at: number, batch: number | undefined, failed?: {ids: string[]; error: string; retryable: boolean}): Snapshot {
  const next: Snapshot = previous.key === key
    ? { key, results: new Map(previous.results), updated: new Map(previous.updated), stale: new Set(previous.stale), receivedAt: previous.receivedAt, receivedBatch: previous.receivedBatch }
    : { key, results: new Map(), updated: new Map(), stale: new Set(), receivedAt: 0 };
  for (const result of data && batch !== next.receivedBatch ? data : []) {
    if (result.status === "error" && next.results.get(result.id)?.frame) {
      next.stale.add(result.id);
      next.results.set(result.id, {...next.results.get(result.id)!, error: result.error, request_error: result.request_error});
    }
    else { next.results.set(result.id, result); next.updated.set(result.id, at); next.stale.delete(result.id); }
  }
  if (data) { next.receivedAt = at; next.receivedBatch = batch; }
  for (const id of failed?.ids ?? []) {
    const previous = next.results.get(id);
    if (previous?.frame) {
      next.stale.add(id); next.results.set(id, {...previous, error: failed!.error, request_error: failed!.retryable ? failed!.ids : undefined});
    } else next.results.set(id, {id, status: "error", elapsed_ms: 0, error: failed!.error, request_error: failed!.retryable ? failed!.ids : undefined});
  }
  return next;
}
