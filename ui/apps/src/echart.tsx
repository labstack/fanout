import { GridComponent, LegendComponent, TooltipComponent, VisualMapComponent, AriaComponent } from "echarts/components";
import { init, use, type EChartsCoreOption } from "echarts/core";
import { SVGRenderer } from "echarts/renderers";
import { useEffect, useRef } from "react";

use([SVGRenderer, GridComponent, LegendComponent, TooltipComponent, VisualMapComponent, AriaComponent]);

export { use as useECharts };

export function EChart({ option, height = 260, label, onClick }: { option: EChartsCoreOption; height?: number; label: string; onClick?: (params: unknown) => void }) {
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
  return <div ref={ref} className="echart" style={{ height }} role="img" aria-label={label} />;
}
