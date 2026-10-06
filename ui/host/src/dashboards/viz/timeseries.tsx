import { useMemo } from "react";
import { chartThemeFor, timeseriesOption } from "../../../../panels/compile";
import { pointSelection } from "../../../../panels/interaction";
import { withAnnotations } from "../../../../panels/annotations";
import { EChartCanvas } from "../echart-canvas";
import type { AnalysisProps } from "./analysis-chart";

export function TimeseriesViz({ panel, title = panel.title, result, dark, height, group, annotations, vars, onSelect, onPoint, onZoom }: AnalysisProps) {
  const option = useMemo(() => {
    const theme = chartThemeFor(dark);
    const compiled = timeseriesOption(panel, result, theme);
    return annotations ? withAnnotations(compiled, panel, result, annotations, vars ?? {}, theme) : compiled;
  }, [panel, result.frame, result.previous, result.shift_ms, result.from_ms, result.to_ms, result.annotation_scope, result.annotation_error, dark, annotations]);
  return <EChartCanvas option={option} height={height} label={`${title}: time series`} group={group} onZoom={onZoom} onClick={onPoint || onSelect ? event => {
    const selection = pointSelection(panel, result, event);
    if (!selection) return;
    onPoint?.(selection);
    const first = Object.values(selection.dimensions)[0];
    if (first !== undefined) onSelect?.(first);
  } : undefined} />;
}
