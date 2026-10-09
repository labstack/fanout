import { parseDrill, type DrillTarget } from "./drill-state";
import { parseSearch as decodeSearch, stringifySearch } from "../search-encoding";
import type { DashboardSpec, DashboardTime, VarValue } from "../../../panels/types";

export const ranges = ["5m", "15m", "1h", "3h", "6h", "12h", "24h", "2d", "7d", "30d"] as const;
export const refreshes = ["off", "10s", "30s", "1m", "5m"] as const;

/** Everything about a dashboard view that belongs in the address bar, so a
 *  link reproduces what its sender saw (#232 item 12). Unrecognised values
 *  are dropped rather than rejected: a stale link still opens the dashboard. */
export type DashboardSearch = { drill?: string; range?: string; from?: string; to?: string; compare?: "0" | "1"; view?: string; edit?: "1"; vars?: Record<string, VarValue> };

const isInstant = (value: unknown): value is string => {
  if (typeof value !== "string" || !/^\d{4}-\d{2}-\d{2}[Tt](?:[01]\d|2[0-3]):[0-5]\d:[0-5]\d(?:\.\d+)?(?:[Zz]|[+-](?:[01]\d|2[0-3]):[0-5]\d)$/.test(value) || Number.isNaN(Date.parse(value))) return false;
  // Date.parse normalizes impossible dates such as February 30.
  const [year, month, day] = value.slice(0, 10).split("-").map(Number);
  const date = new Date(0); date.setUTCFullYear(year, month - 1, day); date.setUTCHours(0, 0, 0, 0);
  return date.getUTCFullYear() === year && date.getUTCMonth() === month - 1 && date.getUTCDate() === day;
};
const primitiveString = (value: unknown): string | undefined =>
  typeof value === "string" || typeof value === "number" || typeof value === "boolean" ? String(value) : undefined;

export function parseSearch(raw: Record<string, unknown>): DashboardSearch {
  const out: DashboardSearch = {};
  if (typeof raw.range === "string" && (ranges as readonly string[]).includes(raw.range)) out.range = raw.range;
  else if (isInstant(raw.from) && isInstant(raw.to) && Date.parse(raw.from) < Date.parse(raw.to)) { out.from = raw.from; out.to = raw.to; }
  const compare = primitiveString(raw.compare);
  const view = primitiveString(raw.view);
  if (compare === "1" || compare === "0") out.compare = compare;
  if (view !== undefined && /^[a-z][a-z0-9_]{0,39}$/.test(view)) out.view = view;
  if (primitiveString(raw.edit) === "1") out.edit = "1";
  const vars: Record<string, VarValue> = {};
  for (const [key, value] of Object.entries(raw)) {
    if (!key.startsWith("var-")) continue;
    const name = key.slice(4);
    if (!/^[a-z][a-z0-9_]{0,39}$/.test(name)) continue;
    if (value !== null && typeof value === "object") {
      // Query variables resolve at most 500 options (custom lists at most 200).
      if (!Array.isArray(value) && Object.keys(value).length === 1 && Object.hasOwn(value, "values")) {
        const items = (value as { values: unknown }).values;
        if (Array.isArray(items) && items.length <= 500 && items.every((item): item is string => typeof item === "string")) vars[name] = items;
      }
    } else {
      const scalar = primitiveString(value);
      if (scalar !== undefined) vars[name] = scalar;
    }
  }
  if (Object.keys(vars).length > 0) out.vars = vars;
  const drill = parseDrill(raw.drill);
  if (drill) out.drill = JSON.stringify(drill);
  return out;
}

export function toSearchParams(search: DashboardSearch): Record<string, unknown> {
  const out: Record<string, unknown> = {};
  if (search.drill) out.drill = JSON.parse(search.drill);
  if (search.range) out.range = search.range;
  if (search.from && search.to) { out.from = search.from; out.to = search.to; }
  if (search.compare) out.compare = search.compare;
  if (search.view) out.view = search.view;
  if (search.edit) out.edit = search.edit;
  for (const [name, value] of Object.entries(search.vars ?? {})) out[`var-${name}`] = Array.isArray(value) ? { values: value } : value;
  return out;
}

/** Build trace links with the same canonical search state and codec as the router. */
export function drillHref(href: string, target: DrillTarget): string {
  const url = new URL(href);
  const search = {...parseSearch(decodeSearch(url.search)), drill: JSON.stringify(target)};
  url.search = stringifySearch(toSearchParams(search));
  return url.href;
}

export function effectiveTime(spec: DashboardSpec, search: DashboardSearch): DashboardTime {
  const refresh = spec.time.refresh ?? "30s";
  if (search.from && search.to) return { from: search.from, to: search.to, refresh };
  if (search.range) return { range: search.range, refresh };
  return { ...spec.time, refresh };
}
