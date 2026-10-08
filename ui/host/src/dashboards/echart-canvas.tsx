import { LabelLayout } from "echarts/features";
import { adaptValueAxis } from "../../../panels/units";
import { BarChart, CustomChart, HeatmapChart, LineChart, ScatterChart } from "echarts/charts";
import { AriaComponent, BrushComponent, DataZoomComponent, GraphicComponent, GridComponent, LegendComponent, MarkAreaComponent, MarkLineComponent, ToolboxComponent, TooltipComponent, VisualMapComponent } from "echarts/components";
import { connect, disconnect, init, use, type EChartsCoreOption, type EChartsType } from "echarts/core";
import { CanvasRenderer } from "echarts/renderers";
import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { layoutEndLabels, type ChartSize } from "../../../panels/compile";
import { keyboardPoints, type KeyboardPoint, type PointEvent } from "../../../panels/keyboard";
import { formatValue } from "../../../panels/units";
import { ChartKeyboard } from "./chart-keyboard";
import { registerAudit, chartAuditSnapshot } from "./chart-measurements-dev";

use([LabelLayout, CanvasRenderer, LineChart, BarChart, CustomChart, HeatmapChart, ScatterChart, GridComponent, GraphicComponent, LegendComponent, TooltipComponent, MarkAreaComponent, MarkLineComponent, VisualMapComponent, AriaComponent, BrushComponent, DataZoomComponent, ToolboxComponent]);

/* Dashboard panels draw on canvas: SVG stays smooth only to a few thousand
   points, and a dashboard of a dozen time series passes that. One instance
   lives for the component's lifetime; options are replaced in place. */
/* Charts per connected group, so the last one out disconnects it. */
const groups = new Map<string, number>();

type DisplaySeries = {name?: string; data?: readonly unknown[]; interactive?: boolean; type?: string; keyboard_unit?: string; keyboard_x_unit?: string; tooltip?: {valueFormatter?(value: number): string}};
type DisplayAxis = {type?: string; data?: string[]};
export function EChartCanvas({ option, optionForSize, height, label, onClick, onZoom, group, keyboard }: { option: EChartsCoreOption; optionForSize?: (size: ChartSize) => EChartsCoreOption; height: number | string; label: string; onClick?: (params: PointEvent) => void; onZoom?: (from: number, to: number) => void; group?: string;
  keyboard?: {canSelect?(event: PointEvent): boolean; bounds?: {from: number; to: number}; interval?: string; onRangePending?(pending: boolean): void} }) {
  const ref = useRef<HTMLDivElement>(null);
  const chart = useRef<EChartsType | null>(null);
  const [displayed, setDisplayed] = useState(option);
  const [active, setActive] = useState(false);
  const activeRef = useRef(false);
  const displayedRef = useRef(option);
  const keyboardRef = useRef(keyboard); keyboardRef.current = keyboard;
  const highlighted = useRef<KeyboardPoint | undefined>(undefined);
  const highlight = useCallback((point?: KeyboardPoint) => {
    if (highlighted.current) chart.current?.dispatchAction({type: "downplay", seriesIndex: highlighted.current.series_index, dataIndex: highlighted.current.data_index});
    chart.current?.dispatchAction({type: "hideTip"});
    highlighted.current = point;
    if (point) {
      chart.current?.dispatchAction({type: "highlight", seriesIndex: point.series_index, dataIndex: point.data_index});
      chart.current?.dispatchAction({type: "showTip", seriesIndex: point.series_index, dataIndex: point.data_index});
    }
  }, []);
  const displaySeries = (Array.isArray(displayed.series) ? displayed.series : displayed.series ? [displayed.series] : []) as DisplaySeries[];
  const x = (Array.isArray(displayed.xAxis) ? displayed.xAxis[0] : displayed.xAxis) as DisplayAxis | undefined;
  const y = (Array.isArray(displayed.yAxis) ? displayed.yAxis[0] : displayed.yAxis) as DisplayAxis | undefined;
  const points = useMemo(() => {
    if (!keyboard || !active) return [];
    const series = (Array.isArray(displayed.series) ? displayed.series : displayed.series ? [displayed.series] : []) as DisplaySeries[];
    const category = y?.type === "category" && series[0]?.type !== "custom" ? y : x?.type === "category" ? x : undefined;
    return keyboardPoints(series.map(s => ({...s, interactive: undefined}))).map(point => {
      const value = Array.isArray(point.event.value) ? point.event.value : [];
      return {...point, event: {...point.event, interactive: series[point.series_index]?.interactive, name: point.event.name ?? (series[point.series_index]?.type === "custom" ? y?.data?.[Number(value[1])] : category?.data?.[point.data_index])}};
    }).filter(point => point.event.value != null && (!Array.isArray(point.event.value) || point.event.value.at(-1) != null));
  }, [displayed, active]);
  const pointWindow = (point: KeyboardPoint) => {
    const selection = (point.event.data as {selection?: {time?: number; from?: string; to?: string}} | undefined)?.selection;
    const value = Array.isArray(point.event.value) ? point.event.value : [];
    const from = selection?.from ? Date.parse(selection.from) : selection?.time ?? (x?.type === "time" ? Number(value[0]) : NaN);
    const interval = /^(\d+)(ms|s|m|h|d)$/.exec(keyboard?.interval ?? "");
    const duration = interval ? Number(interval[1]) * ({ms:1,s:1000,m:60000,h:3600000,d:86400000}[interval[2] as "ms"|"s"|"m"|"h"|"d"]) : undefined;
    const next = displaySeries[point.series_index]?.data?.[point.data_index + 1];
    const nextValue = Array.isArray(next) ? next : (next as {value?: unknown[]} | undefined)?.value;
    const nextTime = typeof nextValue?.[0] === "number" && nextValue[0] > from ? nextValue[0] : undefined;
    const end = duration ? from + duration : nextTime ?? keyboard?.bounds?.to ?? from;
    const to = selection?.to ? Date.parse(selection.to) : displaySeries[point.series_index]?.type === "custom" ? Number(value[3]) : Math.min(end, keyboard?.bounds?.to ?? end);
    return Number.isFinite(from) && Number.isFinite(to) ? {from, to} : undefined;
  };
  const summary = (point: KeyboardPoint) => {
    const series = displaySeries[point.series_index];
    const value = Array.isArray(point.event.value) ? point.event.value : [point.event.value];
    const custom = series?.type === "custom";
    const at = pointWindow(point);
    const category = custom ? y?.data?.[Number(value[1])] : point.event.name;
    const numeric = custom ? value[2] : value.at(-1);
    const formatted = typeof numeric === "number" ? series?.tooltip?.valueFormatter?.(numeric) ?? formatValue(series?.keyboard_unit, numeric) : String(numeric ?? "No value");
    const scatter = series?.type === "scatter" ? `x: ${formatValue(series.keyboard_x_unit, Number(value[0]))} · y: ` : "";
    return [point.event.seriesName ?? "Series", category, at ? new Date(at.from).toISOString() + (at.to !== at.from ? ` – ${new Date(at.to).toISOString()}` : "") : undefined, scatter + formatted].filter(Boolean).join(" · ");
  };
  const selected = useRef<Record<string, boolean>>({});
  const click = useRef(onClick);
  click.current = onClick;
  const zoom = useRef(onZoom);
  zoom.current = onZoom;
  const zoomEnabled = Boolean(onZoom);
  const brushRange = useRef<{from: number; to: number} | undefined>(undefined);
  const rangeBarHeight = useRef(0);
  const [rangeReset, setRangeReset] = useState(0);
  const paintRange = useCallback(() => {
    const range = brushRange.current;
    // Use the same native translucent covers as a mouse lineX brush. Silent
    // preview updates never emit brushEnd or invoke the shared zoom callback.
    chart.current?.dispatchAction({type: "brush", areas: range ? [{brushType: "lineX", xAxisIndex: 0, coordRange: [range.from, range.to]}] : []}, {silent: true});
  }, []);
  const previewRange = useCallback((range?: {from: number; to: number}) => {
    if (brushRange.current?.from === range?.from && brushRange.current?.to === range?.to) return;
    brushRange.current = range; paintRange();
  }, [paintRange]);
  const description = zoomEnabled ? `${label} Brush across the chart to zoom to that range.` : label;
  const apply = useRef<() => void>(() => undefined);
  const reserveRangeBar = useCallback((height: number) => {
    if (height === rangeBarHeight.current) return;
    rangeBarHeight.current = height; apply.current();
  }, []);
  const auditInput = useRef<{ compiled: EChartsCoreOption; size: ChartSize } | null>(null);
  const context = useRef<CanvasRenderingContext2D | null | undefined>(undefined);
  const responsive = useRef(false);
  responsive.current = Boolean(optionForSize);
  apply.current = () => {
    if (context.current === undefined) context.current = document.createElement("canvas").getContext("2d");
    const ctx = context.current;
    const measureText = ctx ? (text: string, font: string) => { ctx.font = font; return ctx.measureText(text).width; } : undefined;
    const size = { measureText, width: ref.current?.clientWidth ?? 0, height: ref.current?.clientHeight ?? 0 };
    let compiled = optionForSize && size.width > 0 && size.height > 0 ? optionForSize({...size, height: Math.max(1, size.height - rangeBarHeight.current)}) : option;
    if (rangeBarHeight.current && compiled.grid && !Array.isArray(compiled.grid)) {
      const grid = compiled.grid as {bottom?: number};
      compiled = {...compiled, grid: {...grid, bottom: (typeof grid.bottom === "number" ? grid.bottom : 8) + rangeBarHeight.current}};
    }
    const series = (compiled.series ?? []) as { name?: string }[];
    const names = new Set(series.map(item => item.name));
    const retained = Object.fromEntries(Object.entries(selected.current).filter(([name]) => names.has(name)));
    selected.current = retained;
    const legend = compiled.legend as { type?: string; show?: boolean; data?: string[]; formatter?: (name: string) => string; selected?: Record<string, boolean> } | undefined;
    // ECharts 6 brush preprocessing always creates a toolbox, even without
    // buttons. Register it and hide it; activation uses takeGlobalCursor.
    chart.current?.setOption({ ...compiled, ...(legend ? { legend: { ...legend, selected: { ...legend.selected, ...retained } } } : {}), ...(zoom.current ? { toolbox: { show: false }, brush: { xAxisIndex: 0, brushMode: "single", removeOnClick: true } } : {}), aria: { ...(compiled as { aria?: object }).aria, enabled: true, description } }, { notMerge: true });
    // ECharts containment can shrink the nominal plot. Recompute numeric tick
    // density once from the native rect, including any annotation/legend band.
    const native = chart.current as unknown as {getModel?():{getComponent(name:string):{coordinateSystem?:{getRect():{y:number;height:number}}}}};
    const rect=native?.getModel?.().getComponent("grid")?.coordinateSystem?.getRect();
    const plotHeight=rect?.height;
    if(plotHeight && compiled.yAxis) {
      const axes=(Array.isArray(compiled.yAxis)?compiled.yAxis:[compiled.yAxis]) as Record<string,unknown>[];
      const updated=axes.map(axis=>adaptValueAxis(axis,plotHeight));
      if(updated.some((axis,i)=>axis.splitNumber!==axes[i].splitNumber || axis.interval!==axes[i].interval)) {
        compiled={...compiled,yAxis:Array.isArray(compiled.yAxis)?updated:updated[0]};
        chart.current?.setOption({yAxis:compiled.yAxis});
      }
    }
    if (rect && (compiled.series as {endLabel?: {show?: boolean}}[] | undefined)?.some(line => line.endLabel?.show)) {
      compiled = layoutEndLabels(compiled, rect, (seriesIndex, value) => {
        const pixel = chart.current?.convertToPixel({seriesIndex}, value);
        return Array.isArray(pixel) ? pixel[1] : NaN;
      });
      chart.current?.setOption({series: compiled.series});
    }
    if (highlighted.current) highlight(highlighted.current);
    if (zoom.current) chart.current?.dispatchAction({ type: "takeGlobalCursor", key: "brush", brushOption: { brushType: "lineX", brushMode: "single" } });
    if (zoom.current && brushRange.current) paintRange();
    displayedRef.current = compiled;
    if (keyboardRef.current && activeRef.current) setDisplayed(compiled);
    if (import.meta.env.DEV) auditInput.current = { compiled, size };
  };

  useEffect(() => {
    if (!ref.current) return;
    const instance = init(ref.current, undefined, { renderer: "canvas" });
    chart.current = instance;
    const removeAudit = import.meta.env.DEV && new URLSearchParams(location.search).get("__fanout_audit") === "1"
      ? registerAudit(ref.current, () => auditInput.current ? chartAuditSnapshot(instance, auditInput.current.compiled, auditInput.current.size) : undefined)
      : undefined;
    instance.on("click", (params) => {
      const event = params as PointEvent & {seriesIndex?: number; componentType?: string};
      const series = (displayedRef.current.series ?? []) as DisplaySeries[];
      const interactive = event.seriesIndex === undefined ? event.interactive : event.componentType && event.componentType !== "series" ? false : series[event.seriesIndex]?.interactive;
      click.current?.({...event, ...(interactive === undefined ? {} : {interactive})});
    });
    instance.on("brushEnd", (payload) => {
      const range = (payload as { areas?: { coordRange?: number[] }[] }).areas?.[0]?.coordRange;
      if (range?.length === 2 && Number.isFinite(range[0]) && Number.isFinite(range[1]) && range[0] < range[1]) {
        zoom.current?.(range[0], range[1]);
        if (brushRange.current) { brushRange.current = undefined; setRangeReset(previous => previous + 1); }
        instance.dispatchAction({ type: "brush", areas: [] }, { silent: true });
        if (zoom.current) instance.dispatchAction({ type: "takeGlobalCursor", key: "brush", brushOption: { brushType: "lineX", brushMode: "single" } });
      }
    });
    instance.on("legendselectchanged", (params) => {
      selected.current = { ...(params as { selected: Record<string, boolean> }).selected };
    });
    const observer = new ResizeObserver(() => { instance.resize(); if (responsive.current) apply.current(); });
    observer.observe(ref.current);
    return () => { observer.disconnect(); removeAudit?.(); if (highlighted.current) highlight(undefined); instance.dispose(); chart.current = null; };
  }, []);

  useEffect(() => {
    // notMerge replaces the whole option: callers must memoize `option`.
    apply.current();
  }, [option, optionForSize, description, zoomEnabled]);

  useEffect(() => {
    const instance = chart.current;
    if (!instance || !group) return;
    instance.group = group;
    groups.set(group, (groups.get(group) ?? 0) + 1);
    connect(group);
    return () => {
      instance.group = "";
      const left = (groups.get(group) ?? 1) - 1;
      if (left > 0) {
        groups.set(group, left);
        return;
      }
      groups.delete(group);
      disconnect(group);
    };
  }, [group]);

  const canvas = <div ref={ref} role="img" aria-label={description} aria-hidden={keyboard ? true : undefined} style={{ height: keyboard ? "100%" : height, position: keyboard ? "absolute" : undefined, inset: keyboard ? 0 : undefined, zIndex: keyboard ? 0 : undefined, flex: "1 1 auto", minHeight: 0, width: "100%", minWidth: 0, cursor: onClick ? "pointer" : undefined }} />;
  const series = (Array.isArray(option.series) ? option.series : option.series ? [option.series] : []) as DisplaySeries[];
  const overview = label + ": " + series.length + " series" + (keyboard?.bounds ? "; " + new Date(keyboard.bounds.from).toISOString() + " – " + new Date(keyboard.bounds.to).toISOString() : "");
  return keyboard ? <ChartKeyboard height={height} rangeReset={rangeReset} onRangeChange={previewRange} onRangeBarHeight={reserveRangeBar} points={points} label={overview} summary={summary} onClick={onClick} canSelect={keyboard.canSelect} onHighlight={highlight} onZoom={onZoom} bounds={keyboard.bounds} pointWindow={pointWindow}
    onRangePending={keyboard.onRangePending} onActiveChange={next => {
      activeRef.current = next; setActive(next); if (next) setDisplayed(displayedRef.current);
    }}>{canvas}</ChartKeyboard> : canvas;
}
