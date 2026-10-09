import { authorizedFetch } from "./auth";

export type Envelope<T> = { data: T };
export type { DashboardSummary } from "./dashboards/api";

export const threadHistoryQueryKey = ["agent-threads"] as const;

export async function getJSON<T>(url: string): Promise<T> {
  const response = await authorizedFetch(url);
  if (!response.ok) throw new Error(`Request failed (${response.status})`);
  return response.json() as Promise<T>;
}
