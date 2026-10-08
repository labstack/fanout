import { AriaComponent, GridComponent, LegendComponent, TooltipComponent, VisualMapComponent } from "echarts/components";
import { init, use, type EChartsCoreOption, type EChartsType } from "echarts/core";
import { SVGRenderer } from "echarts/renderers";
import { useEffect, useRef } from "react";

use([SVGRenderer, GridComponent, LegendComponent, TooltipComponent, VisualMapComponent, AriaComponent]);

export { use as useECharts };

/* One chart instance lives for the component's lifetime. Applying options
   in place avoids rebuilding the SVG or restarting animation on re-render. */
export function EChart({ option, height = 260, label, onClick, className }: { option: EChartsCoreOption; height?: number | string; label: string; onClick?: (params: unknown) => void; className?: string }) {
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

  return <div ref={ref} className={className ? `echart ${className}` : "echart"} style={{ height, width: "100%", minWidth: 0 }} role="img" aria-label={label} />;
}
