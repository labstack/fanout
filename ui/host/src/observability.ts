import { useQuery, type UseQueryResult } from "@tanstack/react-query";
import { getJSON } from "./api";
import type { Result } from "../../contracts";

export type Filters = { window: string; namespace: string };
/** Query string for the observability endpoints. */
export function observabilityParams(filters: Filters): URLSearchParams {
  const params = new URLSearchParams({ window: filters.window, limit: "40" });
  if (filters.namespace) params.set("namespace", filters.namespace);
  return params;
}

export type ObservabilityKind = "overview" | "topology" | "performance" | "logs" | "trace";

export function observabilityKey(kind: ObservabilityKind, params: URLSearchParams) {
  return ["observability", kind, params.toString()] as const;
}

// Every view refetches on the same cadence; a failed query retries sooner so
// a view recovers without a manual refresh.
const refetchInterval = (query: { state: { status: string } }) => (query.state.status === "error" ? 15_000 : 30_000);

/** How long an answer is treated as current.
 *
 *  Opening /dashboards renders the default dashboard and then navigates to its
 *  own URL, which unmounts the pane and mounts a new one. With nothing held
 *  fresh, that second mount asked the server for the overview, the service map
 *  and the performance summary all over again — a measured thirteen requests
 *  for a page that needs seven, and the same again on every focus of the tab.
 *  Half the poll interval keeps a remount free without letting a view show
 *  anything the poll would not have shown anyway. */
export const freshFor = 15_000;

export function useObservability<T>(kind: ObservabilityKind, params: URLSearchParams, enabled = true): UseQueryResult<Result<T>> {
  return useQuery({
    queryKey: observabilityKey(kind, params),
    queryFn: () => getJSON<Result<T>>(`/api/observability/${kind}?${params}`),
    enabled,
    refetchInterval,
    staleTime: freshFor,
  });
}

