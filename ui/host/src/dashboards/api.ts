import type { Result, TraceDetail } from "../../../contracts";
import type { DrillTarget } from "./drill-state";
import type { AnnotationBody, AnnotationsResponse } from "../../../panels/annotations";
import type { Frame, Selection, DashboardSpec, DashboardTime, Panel, PanelResult, Variable, VarValue } from "../../../panels/types";
import { authorizedFetch } from "../auth";
import type { Change } from "../dashboard-receipt";
import { ApiError, type Problem } from "../api-error";
export { ApiError, type Problem } from "../api-error";

export type BuildOrigin = { thread_id: string; message_id: string; request_excerpt: string };
export type DashboardSummary = { origin?: BuildOrigin; id: string; name: string; description: string; is_default: boolean; version: number; panel_count: number; updated_at: string };
export type DashboardRecord = { id: string; name: string; description: string; is_default: boolean; version: number; spec: DashboardSpec; created_at: string; updated_at: string };
export type VersionInfo = { version: number; author_kind: "user" | "agent" | "system"; author_id?: string; message?: string; created_at: string };
export type VersionRecord = Omit<VersionInfo, "version"> & { dashboard: DashboardRecord; changes: Change[]; layout_changed: boolean; dashboard_fields: string[]; changes_available: boolean };
export type Operation =
  | { op: "add_panel"; panel: Panel; after?: string }
  | { op: "update_panel"; id: string; set: Record<string, unknown> }
  | { op: "remove_panel"; id: string }
  | { op: "move_panel"; id: string; after?: string }
  | { op: "set_variable"; variable: Variable }
  | { op: "remove_variable"; name: string }
  | { op: "set_time"; time: DashboardTime }
  | { op: "rename"; name?: string; description?: string };
export type QueryBody = { dashboard: DashboardSpec; panels?: string[]; time?: DashboardTime; vars?: Record<string, VarValue>; widths?: Record<string, number>; compare?: boolean };

export const dashboardsKey = ["dashboards"] as const;
export const dashboardsStaleTime = 30_000;

async function request<T>(url: string, init?: RequestInit & { json?: unknown }): Promise<T> {
  const { json, ...rest } = init ?? {};
  const headers = new Headers(rest.headers);
  let body = rest.body;
  if (json !== undefined) {
    headers.set("content-type", "application/json");
    body = JSON.stringify(json);
  }
  const response = await authorizedFetch(url, { ...rest, headers, body });
  const payload = await response.json().catch(() => ({})) as { code?: string; message?: string; problems?: Problem[] };
  if (!response.ok) throw new ApiError(payload.message ?? `Request failed (${response.status})`, response.status, payload.problems ?? [], payload.code ?? "");
  return payload as T;
}

const path = (id: string) => `/api/dashboards/${encodeURIComponent(id)}`;

export const listDashboards = () => request<{ dashboards: DashboardSummary[] }>("/api/dashboards").then((r) => r.dashboards);
export const getDashboard = (id: string, signal?: AbortSignal) => request<DashboardRecord>(path(id), { signal });
export const listVersions = (id: string, signal?: AbortSignal) => request<{ versions: VersionInfo[] }>(`${path(id)}/versions`, { signal }).then(r => r.versions);
export const getVersion = (id: string, version: number, signal?: AbortSignal) => request<VersionRecord>(`${path(id)}/versions/${version}`, { signal });
export const restoreVersion = (id: string, version: number) => request<DashboardRecord>(`${path(id)}/versions/${version}/restore`, { method: "POST" });
export const replaceDashboard = (id: string, spec: DashboardSpec, baseVersion: number, message?: string) =>
  request<DashboardRecord>(path(id), { method: "PUT", json: { spec, base_version: baseVersion, message } });
export const patchDashboard = (id: string, operations: Operation[], baseVersion: number, message?: string) =>
  request<DashboardRecord>(path(id), { method: "PATCH", json: { operations, base_version: baseVersion, message } });
export const queryPanels = (body: QueryBody, signal?: AbortSignal) => request<{ results: PanelResult[] }>("/api/panels/query", { method: "POST", json: body, signal }).then((r) => r.results);
export const queryAnnotations = (body: AnnotationBody, signal?: AbortSignal) => request<AnnotationsResponse>("/api/annotations", { method: "POST", json: body, signal });
export const resolveVariables = (body: Omit<QueryBody, "panels" | "widths" | "compare">, signal?: AbortSignal) =>
  request<{ options: Record<string, { value: string; count?: number }[]> }>("/api/panels/variables/resolve", { method: "POST", json: body, signal }).then((r) => r.options);

export type ExemplarBody = { dashboard: DashboardSpec; panel_id: string; kind?: "traces" | "logs"; time?: DashboardTime; from: string; to: string; dimensions?: Record<string, string>; bucket?: Selection["bucket"]; vars?: Record<string, VarValue> };
export type Exemplar = { trace_id: string; namespace: string; service: string; operation: string; duration_ms: number; status: string; start: string };
export type ExemplarResponse = { traces: Exemplar[]; logs?: Frame; truncated?: boolean };
export const queryExemplars = (body: ExemplarBody, signal?: AbortSignal) => request<ExemplarResponse>("/api/panels/exemplars", { method: "POST", json: body, signal });
export function getTrace(target: DrillTarget, signal?: AbortSignal): Promise<Result<TraceDetail>> {
  const params = new URLSearchParams({ namespace: target.namespace ?? "", from: target.window_from, to: target.window_to, limit: "200" });
  return request<Result<TraceDetail>>(`/api/traces/${encodeURIComponent(target.trace_id ?? "")}?${params}`, { signal });
}
