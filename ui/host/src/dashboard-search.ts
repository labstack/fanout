import { dashboardWindows } from "./widgets/data";

/** The part of a dashboard's view that belongs in the address bar, so a link
 *  reproduces what its sender was looking at rather than opening on whatever
 *  the recipient last chose. Anything unrecognised is dropped instead of
 *  rejected: a stale or hand-edited link should still open the dashboard. */
export type DashboardSearch = { window?: string; namespace?: string };

export function dashboardSearch(search: Record<string, unknown>): DashboardSearch {
  const next: DashboardSearch = {};
  if (typeof search.window === "string" && dashboardWindows.some((option) => option.value === search.window)) next.window = search.window;
  if (typeof search.namespace === "string" && search.namespace.trim()) next.namespace = search.namespace.trim();
  return next;
}
