import { useCallback, useMemo } from "react";
import { barOption, chartThemeFor, type ChartSize } from "../../../../panels/compile";
import { pointSelection } from "../../../../panels/interaction";
import { EChartCanvas } from "../echart-canvas";
import type { AnalysisProps } from "./analysis-chart";

export function BarViz({ panel, title = panel.title, result, dark, height, onSelect, onPoint }: AnalysisProps) {
  const optionForSize = useMemo(() => (size: ChartSize) => barOption(panel, result.frame!, chartThemeFor(dark), size), [panel, result.frame, dark]);
  const option = useMemo(() => optionForSize({ width: 500, height }), [optionForSize, height]);
  const canSelect = useCallback((event: Parameters<typeof pointSelection>[2]) => Boolean(pointSelection(panel, result, event)), [panel, result]);
  return <EChartCanvas keyboard={{canSelect}} option={option} optionForSize={optionForSize} height={height} label={`${title}: bar chart`} onClick={onPoint || onSelect ? event => {
    const selection = pointSelection(panel, result, event);
    if (!selection) return;
    onPoint?.(selection);
    const first = Object.values(selection.dimensions)[0];
    if (first !== undefined) onSelect?.(first);
  } : undefined} />;
}
