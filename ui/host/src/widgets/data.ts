import { useQuery, useQueryClient, type UseQueryResult } from "@tanstack/react-query";
import { useEffect, useState } from "react";
import { getJSON } from "../api";
import type { Result } from "../../../contracts";

export type Filters = { window: string; namespace: string };
export type WidgetConfig = Record<string, unknown>;

export const dashboardWindows = [
  { value: "15m", label: "15 minutes" },
  { value: "1h", label: "1 hour" },
  { value: "6h", label: "6 hours" },
  { value: "24h", label: "24 hours" },
  { value: "168h", label: "7 days" },
  { value: "720h", label: "30 days" },
];

export function windowName(value: string): string {
  return dashboardWindows.find((item) => item.value === value)?.label ?? value;
}

export function configString(config: WidgetConfig | undefined, key: string): string {
  const value = config?.[key];
  return typeof value === "string" ? value : "";
}

/** Query string for the observability endpoints: the dashboard filters plus
 *  the listed widget config keys when they hold a non-empty string. */
export function widgetParams(filters: Filters, config?: WidgetConfig, keys: string[] = []): URLSearchParams {
  const params = new URLSearchParams({ window: filters.window, limit: "40" });
  if (filters.namespace) params.set("namespace", filters.namespace);
  for (const key of keys) {
    const value = configString(config, key);
    if (value) params.set(key, value);
  }
  return params;
}

export type ObservabilityKind = "overview" | "topology" | "performance" | "logs" | "trace";

export function observabilityKey(kind: ObservabilityKind, params: URLSearchParams) {
  return ["observability", kind, params.toString()] as const;
}

// Every widget refetches on the same cadence; a failed query retries sooner so
// a widget recovers without a manual refresh.
const refetchInterval = (query: { state: { status: string } }) => (query.state.status === "error" ? 15_000 : 30_000);

export function useObservability<T>(kind: ObservabilityKind, params: URLSearchParams, enabled = true): UseQueryResult<Result<T>> {
  return useQuery({
    queryKey: observabilityKey(kind, params),
    queryFn: () => getJSON<Result<T>>(`/api/observability/${kind}?${params}`),
    enabled,
    refetchInterval,
  });
}

function latestUpdate(client: ReturnType<typeof useQueryClient>): number | null {
  const newest = client.getQueryCache().findAll({ queryKey: ["observability"] }).reduce((max, query) => Math.max(max, query.state.dataUpdatedAt), 0);
  return newest || null;
}

/** Newest dataUpdatedAt across every observability query on the page. */
export function useLastUpdated(): number | null {
  const client = useQueryClient();
  const [updated, setUpdated] = useState<number | null>(() => latestUpdate(client));
  useEffect(() => client.getQueryCache().subscribe(() => setUpdated(latestUpdate(client))), [client]);
  return updated;
}
