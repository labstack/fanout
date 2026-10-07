import type { Panel, PanelResult, Selection } from "../../../panels/types";
export type DrillTarget = {
  panel_id: string;
  kind: "traces" | "logs";
  from: string;
  to: string;
  window_from: string;
  window_to: string;
  dimensions: Record<string, string>;
  trace_id?: string;
  namespace?: string;
  bucket?: { lower: number; upper?: number };
};
export function parseDrill(raw: unknown): DrillTarget | undefined {
  try {
    const encoded = typeof raw === "string" ? raw : JSON.stringify(raw);
    if (!encoded || encoded.length > 4096) return undefined;
    const v: unknown = typeof raw === "string" ? JSON.parse(raw) : raw;
    if (!v || typeof v !== "object") return undefined;
    const x = v as Partial<DrillTarget>;
    if (
      typeof x.panel_id !== "string" ||
      !/^([a-z][a-z0-9_]{0,39})$/.test(x.panel_id) ||
      (x.kind !== "traces" && x.kind !== "logs") ||
      typeof x.from !== "string" ||
      typeof x.to !== "string" ||
      !Number.isFinite(Date.parse(x.from)) ||
      !Number.isFinite(Date.parse(x.to)) ||
      Date.parse(x.from) >= Date.parse(x.to) ||
      Date.parse(x.to) - Date.parse(x.from) > 30 * 86400000
    )
      return undefined;
    if (
      typeof x.window_from !== "string" ||
      typeof x.window_to !== "string" ||
      !Number.isFinite(Date.parse(x.window_from)) ||
      !Number.isFinite(Date.parse(x.window_to)) ||
      Date.parse(x.window_from) >= Date.parse(x.window_to) ||
      Date.parse(x.window_to) - Date.parse(x.window_from) > 30 * 86400000 ||
      Date.parse(x.from) < Date.parse(x.window_from) ||
      Date.parse(x.to) > Date.parse(x.window_to)
    )
      return undefined;
    if (
      !x.dimensions ||
      Array.isArray(x.dimensions) ||
      typeof x.dimensions !== "object" ||
      Object.keys(x.dimensions).length > 3 ||
      Object.values(x.dimensions).some(
        (v) => typeof v !== "string" || v.length > 500,
      )
    )
      return undefined;
    if (
      x.trace_id !== undefined &&
      (typeof x.trace_id !== "string" || x.trace_id.length > 128)
    )
      return undefined;
    if (
      x.namespace !== undefined &&
      (typeof x.namespace !== "string" || x.namespace.length > 200)
    )
      return undefined;
    if (
      x.bucket &&
      (!Number.isFinite(x.bucket.lower) ||
        x.bucket.lower < 0 ||
        (x.bucket.upper !== undefined &&
          (!Number.isFinite(x.bucket.upper) ||
            x.bucket.upper <= x.bucket.lower)))
    )
      return undefined;
    return {
      panel_id: x.panel_id,
      kind: x.kind,
      from: x.from,
      to: x.to,
      window_from: x.window_from,
      window_to: x.window_to,
      dimensions: x.dimensions,
      trace_id: x.trace_id,
      namespace: x.namespace,
      bucket: x.bucket,
    };
  } catch {
    return undefined;
  }
}
export function makeDrill(
  panel: Panel,
  result: PanelResult,
  selection: Selection,
): DrillTarget | undefined {
  if (!panel.drill && panel.viz !== "service_map" && !selection.trace_id) return undefined;
  if (result.from_ms === undefined || result.to_ms === undefined)
    return undefined;
  let from = result.from_ms,
    to = result.to_ms;
  if (selection.from !== undefined && selection.to !== undefined) {
    from = Math.max(from, Date.parse(selection.from));
    to = Math.min(to, Date.parse(selection.to));
  }
  if (selection.time !== undefined && !selection.trace_id) {
    const m = /^(\d+)(s|m|h|d)$/.exec(result.interval ?? "");
    const width = m
      ? Number(m[1]) *
        { s: 1000, m: 60000, h: 3600000, d: 86400000 }[
          m[2] as "s" | "m" | "h" | "d"
        ]
      : 60000;
    from = Math.max(from, selection.time);
    to = Math.min(to, selection.time + width);
  }
  return parseDrill(
    JSON.stringify({
      panel_id: panel.id,
      kind: selection.trace_id || panel.viz === "service_map" ? "traces" : panel.drill,
      from: selection.from && Date.parse(selection.from) === from ? selection.from : new Date(from).toISOString(),
      to: selection.to && Date.parse(selection.to) === to ? selection.to : new Date(to).toISOString(),
      window_from: new Date(result.from_ms).toISOString(),
      window_to: new Date(result.to_ms).toISOString(),
      dimensions: selection.dimensions,
      trace_id: selection.trace_id,
      namespace: selection.namespace,
      bucket: selection.bucket,
    }),
  );
}
