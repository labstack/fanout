import type { Panel, PanelResult, Selection } from "./types";

export type ChartEvent = { name?: string; seriesName?: string; value?: unknown; data?: unknown };

export function brushRange(from: number, to: number): { from: string; to: string } | undefined {
  if (!Number.isFinite(from) || !Number.isFinite(to) || from >= to || Math.abs(from) > 8640000000000000 || Math.abs(to) > 8640000000000000) return undefined;
  return { from: new Date(Math.floor(from)).toISOString(), to: new Date(Math.ceil(to)).toISOString() };
}

export function panelTimeLabel(panel: Panel): string | undefined {
  const parts = [panel.time?.range ? `Range ${panel.time.range}` : undefined, panel.time?.shift ? `Shifted ${panel.time.shift}` : undefined].filter(Boolean);
  return parts.length ? parts.join(" · ") : undefined;
}

export function pointSelection(panel: Panel, result: PanelResult, event: ChartEvent): Selection | undefined {
  if (event.seriesName?.endsWith(" · previous") || event.seriesName === "Other" || event.name === "Other") return undefined;
  const embedded = (event.data as { selection?: Selection } | undefined)?.selection;
  if (embedded) return embedded;
  const dimension = panel.query?.by?.[0] ?? result.frame?.columns.find(column => column.role === "dimension")?.name;
  const name = panel.viz === "timeseries" ? event.seriesName : event.name;
  const value = Array.isArray(event.value) ? event.value : [];
  const time = panel.viz === "timeseries" && typeof value[0] === "number" ? value[0] : undefined;
  return { time, dimensions: dimension && name ? { [dimension]: name } : {} };
}
