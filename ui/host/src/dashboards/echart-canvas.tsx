import { BarChart, CustomChart, GaugeChart, GraphChart, HeatmapChart, LineChart, ScatterChart } from "echarts/charts";
import { AriaComponent, BrushComponent, DataZoomComponent, GridComponent, LegendComponent, MarkAreaComponent, MarkLineComponent, ToolboxComponent, TooltipComponent, VisualMapComponent } from "echarts/components";
import { connect, disconnect, init, use, type EChartsCoreOption, type EChartsType } from "echarts/core";
import { CanvasRenderer } from "echarts/renderers";
import { useEffect, useRef } from "react";

use([CanvasRenderer, LineChart, BarChart, GaugeChart, GraphChart, CustomChart, HeatmapChart, ScatterChart, GridComponent, LegendComponent, TooltipComponent, MarkAreaComponent, MarkLineComponent, VisualMapComponent, AriaComponent, BrushComponent, ToolboxComponent, DataZoomComponent]);

/* Dashboard panels draw on canvas: SVG stays smooth only to a few thousand
   points, and a dashboard of a dozen time series passes that. One instance
   lives for the component's lifetime; options are replaced in place. */
/* Charts per connected group, so the last one out disconnects it. */
const groups = new Map<string, number>();

export function EChartCanvas({ option, height, label, onClick, onZoom, group }: { option: EChartsCoreOption; height: number | string; label: string; onClick?: (params: { name?: string; seriesName?: string; value?: unknown; data?: unknown; dataType?: string }) => void; onZoom?: (from: number, to: number) => void; group?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  const chart = useRef<EChartsType | null>(null);
  const selected = useRef<Record<string, boolean>>({});
  const click = useRef(onClick);
  click.current = onClick;
  const zoom = useRef(onZoom);
  zoom.current = onZoom;
  const zoomEnabled = Boolean(onZoom);
  const description = zoomEnabled ? `${label} Brush across the chart to zoom to that range.` : label;

  useEffect(() => {
    if (!ref.current) return;
    const instance = init(ref.current, undefined, { renderer: "canvas" });
    chart.current = instance;
    instance.on("click", (params) => click.current?.(params as { name?: string; seriesName?: string; value?: unknown; data?: unknown; dataType?: string }));
    instance.on("brushEnd", (payload) => {
      const range = (payload as { areas?: { coordRange?: number[] }[] }).areas?.[0]?.coordRange;
      if (range?.length === 2 && Number.isFinite(range[0]) && Number.isFinite(range[1]) && range[0] < range[1]) {
        zoom.current?.(range[0], range[1]);
        instance.dispatchAction({ type: "brush", areas: [] }, { silent: true });
        if (zoom.current) instance.dispatchAction({ type: "takeGlobalCursor", key: "brush", brushOption: { brushType: "lineX", brushMode: "single" } });
      }
    });
    instance.on("legendselectchanged", (params) => {
      selected.current = { ...(params as { selected: Record<string, boolean> }).selected };
    });
    const observer = new ResizeObserver(() => instance.resize());
    observer.observe(ref.current);
    return () => { observer.disconnect(); instance.dispose(); chart.current = null; };
  }, []);

  useEffect(() => {
    // notMerge replaces the whole option: callers must memoize `option`.
    const series = (option.series ?? []) as { name?: string }[];
    const names = new Set(series.map((item) => item.name));
    const retained = Object.fromEntries(Object.entries(selected.current).filter(([name]) => names.has(name)));
    selected.current = retained;
    const legend = option.legend as { selected?: Record<string, boolean> } | undefined;
    chart.current?.setOption({ ...option, ...(legend ? { legend: { ...legend, selected: { ...legend.selected, ...retained } } } : {}), ...(zoom.current ? { brush: { toolbox: ["lineX", "clear"], xAxisIndex: 0, brushMode: "single", removeOnClick: true } } : {}), aria: { ...(option as { aria?: object }).aria, enabled: true, description } }, { notMerge: true });
    if (zoom.current) chart.current?.dispatchAction({ type: "takeGlobalCursor", key: "brush", brushOption: { brushType: "lineX", brushMode: "single" } });
  }, [option, description, zoomEnabled]);

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

  return <div ref={ref} role="img" aria-label={description} style={{ height, flex: "1 1 auto", minHeight: 0, width: "100%", minWidth: 0, cursor: onClick ? "pointer" : undefined }} />;
}
