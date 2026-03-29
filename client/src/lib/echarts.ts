import * as echarts from "echarts/core";
import {
  LineChart,
  BarChart,
  HeatmapChart,
  SankeyChart,
  GraphChart,
} from "echarts/charts";
import {
  GridComponent,
  TooltipComponent,
  LegendComponent,
  VisualMapComponent,
  MarkLineComponent,
  MarkPointComponent,
} from "echarts/components";
import { SVGRenderer } from "echarts/renderers";
import ReactEChartsCore from "echarts-for-react/lib/core";

echarts.use([
  LineChart,
  BarChart,
  HeatmapChart,
  SankeyChart,
  GraphChart,
  GridComponent,
  TooltipComponent,
  LegendComponent,
  VisualMapComponent,
  MarkLineComponent,
  MarkPointComponent,
  SVGRenderer,
]);

export default echarts;
export const ReactECharts =
  (ReactEChartsCore as any).default || ReactEChartsCore;

export function cssVar(name: string): string {
  return getComputedStyle(document.documentElement)
    .getPropertyValue(name)
    .trim();
}

/** Shared tooltip style matching the app's popover theme. */
export function tooltipStyle(): Record<string, unknown> {
  return {
    backgroundColor: cssVar("--popover"),
    borderColor: cssVar("--border"),
    textStyle: { color: cssVar("--popover-foreground"), fontSize: 12 },
  };
}

/** Standard axis line style (border color). */
export function axisLine(): { lineStyle: { color: string } } {
  return { lineStyle: { color: cssVar("--border") } };
}

/** Standard axis label style. */
export function axisLabel(fontSize = 12): { color: string; fontSize: number } {
  return { color: cssVar("--foreground"), fontSize };
}

/** Standard dashed split line for value axes. */
export function splitLine(): { lineStyle: { color: string; type: "dashed" } } {
  return { lineStyle: { color: cssVar("--border"), type: "dashed" } };
}

/** Escape HTML entities for safe interpolation in ECharts tooltip formatters. */
export function esc(s: unknown): string {
  return String(s).replace(/&/g, "&amp;").replace(/</g, "&lt;").replace(/>/g, "&gt;").replace(/"/g, "&quot;");
}

/** Status color used by topology and sankey blocks. */
export function statusColor(status?: string): string {
  switch (status?.toLowerCase()) {
    case "healthy": return "#22c55e";
    case "degraded": return "#f59e0b";
    case "unhealthy": return "#ef4444";
    default: return "#6b7280";
  }
}
