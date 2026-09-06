import { GridComponent, LegendComponent, TooltipComponent, VisualMapComponent, AriaComponent } from "echarts/components";
import { init, use, type EChartsCoreOption, type EChartsType } from "echarts/core";
import { SVGRenderer } from "echarts/renderers";
import { useEffect, useRef } from "react";

use([SVGRenderer, GridComponent, LegendComponent, TooltipComponent, VisualMapComponent, AriaComponent]);

export { use as useECharts };

/* Mirrors ui/host/src/echart.tsx. One chart instance lives for the component's
   lifetime and option changes are applied in place, so a parent re-render never
   rebuilds the SVG or restarts its animation. The click handler goes through a
   ref registered once, because a caller that passes an inline function would
   otherwise tear the chart down on every render. */
export function EChart({ option, height = 260, label, onClick }: { option: EChartsCoreOption; height?: number; label: string; onClick?: (params: unknown) => void }) {
  const ref = useRef<HTMLDivElement>(null);
  const chartRef = useRef<EChartsType | null>(null);
  const onClickRef = useRef(onClick);
  onClickRef.current = onClick;

  useEffect(() => {
    if (!ref.current) return;
    const chart = init(ref.current, undefined, { renderer: "svg" });
    chartRef.current = chart;
    chart.on("click", (params) => onClickRef.current?.(params));
    const observer = new ResizeObserver(() => chart.resize());
    observer.observe(ref.current);
    return () => { observer.disconnect(); chart.dispose(); chartRef.current = null; };
  }, []);

  useEffect(() => {
    // notMerge replaces the whole option, so a series that disappears from
    // the data disappears from the chart instead of lingering from a merge.
    chartRef.current?.setOption({ animationDuration: 280, aria: { enabled: true, decal: { show: true }, description: label }, ...option }, { notMerge: true });
  }, [label, option]);

  return <div ref={ref} className="echart" style={{ height }} role="img" aria-label={label} />;
}
