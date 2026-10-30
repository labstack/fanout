import type { DashboardTime, Viz } from "../../../panels/types";
import { isInstant, ranges } from "../dashboards/search";

const vizzes: readonly Viz[] = ["stat", "gauge", "timeseries", "bar", "table", "heatmap", "histogram", "scatter", "state_timeline", "logs", "log_patterns", "traces", "service_map", "health"];

/** The FanQL envelope (proposal 0004, section 0.13) plus the page's own tab and SQL. */
export type ExploreSearch = { q: string; viz?: Viz; range: string; from?: string; to?: string; tab: "query" | "sql"; sql?: string };

export const defaultRange = "1h";

export function parseExploreSearch(raw: Record<string, unknown>): ExploreSearch {
  const out: ExploreSearch = { q: "", range: defaultRange, tab: "query" };
  // An envelope of another version is not ours to interpret.
  if (raw.v !== undefined && String(raw.v) !== "1") return out;
  if (typeof raw.q === "string") out.q = raw.q;
  if (typeof raw.viz === "string" && (vizzes as readonly string[]).includes(raw.viz)) out.viz = raw.viz as Viz;
  if (typeof raw.range === "string" && (ranges as readonly string[]).includes(raw.range)) out.range = raw.range;
  if (isInstant(raw.from) && isInstant(raw.to) && Date.parse(raw.from) < Date.parse(raw.to)) { out.from = raw.from; out.to = raw.to; }
  if (raw.tab === "sql") out.tab = "sql";
  if (typeof raw.sql === "string") out.sql = raw.sql;
  return out;
}

export function toExploreParams(search: ExploreSearch): Record<string, string> {
  const out: Record<string, string> = { v: "1" };
  if (search.q) out.q = search.q;
  if (search.viz) out.viz = search.viz;
  if (search.from && search.to) { out.from = search.from; out.to = search.to; }
  else if (search.range !== defaultRange) out.range = search.range;
  if (search.tab === "sql") out.tab = "sql";
  if (search.sql) out.sql = search.sql;
  return out;
}

export function exploreTime(search: ExploreSearch): DashboardTime {
  return search.from && search.to ? { from: search.from, to: search.to, refresh: "off" } : { range: search.range, refresh: "off" };
}
