import type { EChartsCoreOption, EChartsType } from "echarts/core";
import type { ChartSize } from "../../../panels/compile";
import { nativeAudit } from "./native-audit";

type Snapshot = ReturnType<typeof chartAuditSnapshot>;
const snapshots = new Map<string, () => Snapshot | undefined>();
declare global { interface Window { __fanoutAudit?: (id: string) => Snapshot | undefined } }
let nextAuditID = 0;
export function registerAudit(element: HTMLElement, snapshot: () => Snapshot | undefined) {
  const id = `fanout-audit-${++nextAuditID}`;
  element.id = id;
  snapshots.set(id, snapshot);
  window.__fanoutAudit = id => snapshots.get(id)?.();
  return () => { element.removeAttribute("id"); snapshots.delete(id); if (!snapshots.size) delete window.__fanoutAudit; };
}
/** Explicit, on-demand development inspection; never runs on an ECharts frame. */
export function chartAuditSnapshot(instance: EChartsType, compiled: EChartsCoreOption, size: ChartSize) {
  const legend = compiled.legend as {type?:string; show?:boolean; data?:string[]; formatter?:(name:string)=>string} | undefined;
  // Instrument the pinned native renderer for collector evidence. Its grid
  // rect includes axis-label containment, which option margins cannot measure.
  type NativeGrid = { coordinateSystem?: { getRect(): { x: number; y: number; width: number; height: number }; getAxis(dim: string): { scale: { getExtent(): number[] } } } };
  const native = instance as unknown as { getModel?(): { getComponent(name: string): NativeGrid | undefined } };
  const grid = native.getModel?.().getComponent("grid");
  const rect = grid?.coordinateSystem?.getRect();
  const audit = nativeAudit(instance,compiled,size,rect);
  let plot;
  if (rect) {
    const extent = grid?.coordinateSystem?.getAxis("x")?.scale.getExtent();
    plot = ({ left: rect.x, top: rect.y, right: rect.x + rect.width, bottom: rect.y + rect.height, from: extent?.[0], to: extent?.[1] });
  }
  const lines = (compiled.series ?? []) as { name?: string; endLabel?: { show?: boolean } }[];
  const endNames = lines.filter(s => s.endLabel?.show).map(s => s.name!);
  const bandNames = (compiled.graphic as {annotation?:boolean;style?:{text?:string}}[] ?? []).filter(g=>g.annotation).map(g=>g.style?.text);
  const display = instance.getZr?.().storage.getDisplayList(false) ?? [];
  const bounds = display.flatMap(el => {
    if (!("style" in el) || typeof (el.style as { text?: unknown }).text !== "string") return [];
    const style = el.style as { text: string; stroke?: string; lineWidth?: number };
    const parent = el.parent as unknown as { style?: { text?: string }; __hostTarget?: { type?: string } } | undefined;
    const rect = el.getBoundingRect().clone(), transform = el.getComputedTransform();
    if (transform) rect.applyTransform(transform);
    return [{ name: parent?.style?.text ?? style.text, end: parent?.__hostTarget?.type === "ec-polyline", left: rect.x, top: rect.y, right: rect.x + rect.width, bottom: rect.y + rect.height, rotation: transform ? Math.atan2(transform[1], transform[0]) : 0, halo: Boolean(style.stroke && style.lineWidth) }];
  });
  const labels = ({ width: size.width, height: size.height, grid_top:rect?.y, end_names: endNames,
    ends: bounds.filter(b => b.end && endNames.includes(b.name)),
    deploys: bounds.filter(b => bandNames.includes(b.name) && b.name !== "anomaly"), anomalies: bounds.filter(b => bandNames.includes(b.name) && b.name === "anomaly") });
  const names = legend?.show ? legend.data ?? [] : [];

  const entries = names.flatMap(name => {
    const text = legend?.formatter?.(name) ?? name;
    return display.filter(el => "style" in el && (el.style as { text?: string }).text === text).map(el => {
      const rect = el.getBoundingRect().clone(), transform = el.getComputedTransform();
      if (transform) rect.applyTransform(transform);
      return { name, text, left: rect.x, top: rect.y, right: rect.x + rect.width, bottom: rect.y + rect.height };
    });
  });

  return {seriesCount: Array.isArray(compiled.series) ? compiled.series.length : compiled.series ? 1 : 0, audit, plot, labels, legend: {type: legend?.type, names, entries}, legendBottom: (compiled.grid as {top?:number}|undefined)?.top ?? 0};
}
