import type { DashboardTime, Viz } from "../../../panels/types";
import { parseDrill } from "../dashboards/drill-state";
import { isInstant, ranges } from "../dashboards/search";

const vizzes: readonly Viz[] = ["stat", "gauge", "timeseries", "bar", "table", "heatmap", "histogram", "scatter", "state_timeline", "logs", "log_patterns", "traces", "service_map", "health"];

/** The FanQL envelope (proposal 0004, section 0.13) plus the page's own tab and SQL. */
export type ExploreSearch = { q: string; viz?: Viz; range: string; from?: string; to?: string; tab: "query" | "builder" | "sql"; sql?: string; sort?: string; drill?: string };

export const defaultRange = "1h";

export function parseExploreSearch(raw: Record<string, unknown>): ExploreSearch {
  const out: ExploreSearch = { q: "", range: defaultRange, tab: "query" };
  // An envelope of another version is not ours to interpret.
  if (raw.v !== undefined && String(raw.v) !== "1") return out;
  if (typeof raw.q === "string") out.q = raw.q;
  if (typeof raw.viz === "string" && (vizzes as readonly string[]).includes(raw.viz)) out.viz = raw.viz as Viz;
  if (typeof raw.range === "string" && (ranges as readonly string[]).includes(raw.range)) out.range = raw.range;
  if (isInstant(raw.from) && isInstant(raw.to) && Date.parse(raw.from) < Date.parse(raw.to)) { out.from = raw.from; out.to = raw.to; }
  if (raw.tab === "sql" || raw.tab === "builder") out.tab = raw.tab;
  if (typeof raw.sql === "string") out.sql = raw.sql;
  const drill = parseDrill(raw.drill);
  if (drill) out.drill = JSON.stringify(drill);
  if (typeof raw.sort === "string" && /^\+?[a-z_]{1,40}$/.test(raw.sort)) out.sort = raw.sort;
  return out;
}

export function toExploreParams(search: ExploreSearch): Record<string, unknown> {
  const out: Record<string, unknown> = { v: "1" };
  if (search.q) out.q = search.q;
  if (search.viz) out.viz = search.viz;
  if (search.from && search.to) { out.from = search.from; out.to = search.to; }
  else if (search.range !== defaultRange) out.range = search.range;
  if (search.tab !== "query") out.tab = search.tab;
  if (search.sql) out.sql = search.sql;
  if (search.sort) out.sort = search.sort;
  if (search.drill) out.drill = JSON.parse(search.drill);
  return out;
}

export function exploreTime(search: ExploreSearch): DashboardTime {
  return search.from && search.to ? { from: search.from, to: search.to, refresh: "off" } : { range: search.range, refresh: "off" };
}
