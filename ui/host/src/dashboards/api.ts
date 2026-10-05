import type { DashboardSpec, DashboardTime, Panel, PanelResult, Variable, VarValue } from "../../../panels/types";
import { authorizedFetch } from "../auth";

export type DashboardSummary = { id: string; name: string; description: string; is_default: boolean; version: number; panel_count: number; updated_at: string };
export type DashboardRecord = { id: string; name: string; description: string; is_default: boolean; version: number; spec: DashboardSpec; created_at: string; updated_at: string };
export type VersionInfo = { version: number; author_kind: "user" | "agent" | "system"; author_id?: string; message?: string; created_at: string };
export type Problem = { path: string; message: string; hint?: string };
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

export class ApiError extends Error {
  constructor(message: string, readonly status: number, readonly problems: Problem[] = []) {
    super(message);
  }
}

async function request<T>(url: string, init?: RequestInit & { json?: unknown }): Promise<T> {
  const { json, ...rest } = init ?? {};
  const headers = new Headers(rest.headers);
  let body = rest.body;
  if (json !== undefined) {
    headers.set("content-type", "application/json");
    body = JSON.stringify(json);
  }
  const response = await authorizedFetch(url, { ...rest, headers, body });
  if (response.status === 204) return undefined as T;
  const payload = await response.json().catch(() => ({})) as { message?: string; problems?: Problem[] };
  if (!response.ok) throw new ApiError(payload.message ?? `Request failed (${response.status})`, response.status, payload.problems ?? []);
  return payload as T;
}

const path = (id: string) => `/api/dashboards/${encodeURIComponent(id)}`;

export const listDashboards = () => request<{ dashboards: DashboardSummary[] }>("/api/dashboards").then((r) => r.dashboards);
export const getDashboard = (id: string) => request<DashboardRecord>(path(id));
export const createDashboard = (spec: DashboardSpec) => request<DashboardRecord>("/api/dashboards", { method: "POST", json: { spec } });
export const replaceDashboard = (id: string, spec: DashboardSpec, baseVersion: number, message?: string) =>
  request<DashboardRecord>(path(id), { method: "PUT", json: { spec, base_version: baseVersion, message } });
export const patchDashboard = (id: string, operations: Operation[], baseVersion: number, message?: string) =>
  request<DashboardRecord>(path(id), { method: "PATCH", json: { operations, base_version: baseVersion, message } });
export const deleteDashboard = (id: string) => request<void>(path(id), { method: "DELETE", headers: { "Fanout-Confirm-Delete": id } });
export const listVersions = (id: string) => request<{ versions: VersionInfo[] }>(`${path(id)}/versions`).then((r) => r.versions);
export const restoreVersion = (id: string, version: number) => request<DashboardRecord>(`${path(id)}/versions/${version}/restore`, { method: "POST" });
export const queryPanels = (body: QueryBody, signal?: AbortSignal) => request<{ results: PanelResult[] }>("/api/panels/query", { method: "POST", json: body, signal }).then((r) => r.results);
export const resolveVariables = (body: Omit<QueryBody, "panels" | "widths" | "compare">, signal?: AbortSignal) =>
  request<{ options: Record<string, { value: string; count?: number }[]> }>("/api/variables/resolve", { method: "POST", json: body, signal }).then((r) => r.options);
