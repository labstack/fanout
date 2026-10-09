import { useCallback, useMemo } from "react";
import { chartThemeFor, timeseriesOption, type ChartSize } from "../../../../panels/compile";
import { pointSelection } from "../../../../panels/interaction";
import { withAnnotations } from "../../../../panels/annotations";
import { EChartCanvas } from "../echart-canvas";
import type { AnalysisProps } from "./analysis-chart";

export function TimeseriesViz({ panel, title = panel.title, result, dark, height, group, annotations, vars, onSelect, onPoint, onZoom, onRangePending }: AnalysisProps) {
  const optionForSize = useMemo(() => (size: ChartSize) => {
    const theme = chartThemeFor(dark);
    const compiled = timeseriesOption(panel, result, theme, size);
    return annotations ? withAnnotations(compiled, panel, result, annotations, vars ?? {}, theme, size) : compiled;
  }, [panel, result.frame, result.previous, result.shift_ms, result.from_ms, result.to_ms, result.annotation_scope, result.annotation_error, dark, annotations]);
  const option = useMemo(() => optionForSize({ width: 500, height }), [optionForSize, height]);
  const canSelect = useCallback((event: Parameters<typeof pointSelection>[2]) => Boolean(pointSelection(panel, result, event)), [panel, result]);
  return <EChartCanvas keyboard={{canSelect, interval: result.interval, onRangePending, bounds: result.from_ms !== undefined && result.to_ms !== undefined ? {from: result.from_ms, to: result.to_ms} : undefined}} option={option} optionForSize={optionForSize} height={height} label={`${title}: time series`} group={group} onZoom={onZoom} onClick={onPoint || onSelect ? event => {
    const selection = pointSelection(panel, result, event);
    if (!selection) return;
    onPoint?.(selection);
    const first = Object.values(selection.dimensions)[0];
    if (first !== undefined) onSelect?.(first);
  } : undefined} />;
}
