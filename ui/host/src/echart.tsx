import { AriaComponent, GridComponent, LegendComponent, TooltipComponent, VisualMapComponent } from "echarts/components";
import { init, use, type EChartsCoreOption } from "echarts/core";
import { SVGRenderer } from "echarts/renderers";
import { useEffect, useRef } from "react";

use([SVGRenderer, GridComponent, LegendComponent, TooltipComponent, VisualMapComponent, AriaComponent]);

export { use as useECharts };

/* Same component as ui/apps/src/echart.tsx. It is not shared through ui/
   because that directory has no node_modules to resolve echarts from. */
export function EChart({ option, height = 260, label, onClick, className }: { option: EChartsCoreOption; height?: number | string; label: string; onClick?: (params: unknown) => void; className?: string }) {
  const ref = useRef<HTMLDivElement>(null);
  useEffect(() => {
    if (!ref.current) return;
    const chart = init(ref.current, undefined, { renderer: "svg" });
    chart.setOption({ animationDuration: 280, aria: { enabled: true, decal: { show: true }, description: label }, ...option });
    if (onClick) chart.on("click", onClick);
    const observer = new ResizeObserver(() => chart.resize());
    observer.observe(ref.current);
    return () => { observer.disconnect(); chart.dispose(); };
  }, [label, onClick, option]);
  return <div ref={ref} className={className ? `echart ${className}` : "echart"} style={{ height, width: "100%", minWidth: 0 }} role="img" aria-label={label} />;
}
