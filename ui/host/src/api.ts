import { authorizedFetch } from "./auth";

export type Envelope<T> = { data: T };
export type DashboardSummary = { id: string; name: string; description: string; is_default: boolean; widget_count: number; updated_at: string };
export type DashboardLayoutRecord = { i: string; x: number; y: number; w: number; h: number; minW?: number; minH?: number };
export type DashboardWidgetRecord = { id: string; type: string; title: string; config?: Record<string, unknown>; enabled: boolean };
export type DashboardState = { layout: DashboardLayoutRecord[]; widgets: DashboardWidgetRecord[]; filters: { window: string; namespace: string } };
export type DashboardRecord = { id: string; name: string; description: string; is_default: boolean; state: DashboardState; updated_at: string };

export const dashboardsQueryKey = ["dashboards"] as const;

export async function getJSON<T>(url: string): Promise<T> {
  const response = await authorizedFetch(url);
  if (!response.ok) throw new Error(`Request failed (${response.status})`);
  return response.json() as Promise<T>;
}
