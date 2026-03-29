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
