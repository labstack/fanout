import { useMemo } from "react";
import { chartThemeFor, timeseriesOption } from "../../../../panels/compile";
import { pointSelection } from "../../../../panels/interaction";
import { EChartCanvas } from "../echart-canvas";
import type { AnalysisProps } from "./analysis-chart";

export function TimeseriesViz({ panel, title = panel.title, result, dark, height, group, onSelect, onPoint, onZoom }: AnalysisProps) {
  const option = useMemo(() => timeseriesOption(panel, result, chartThemeFor(dark)), [panel, result.frame, result.previous, result.shift_ms, dark]);
  return <EChartCanvas option={option} height={height} label={`${title}: time series`} group={group} onZoom={onZoom} onClick={onPoint || onSelect ? event => {
    const selection = pointSelection(panel, result, event);
    if (!selection) return;
    onPoint?.(selection);
    const first = Object.values(selection.dimensions)[0];
    if (first !== undefined) onSelect?.(first);
  } : undefined} />;
}
