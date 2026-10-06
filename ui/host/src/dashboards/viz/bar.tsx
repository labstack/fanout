import { useMemo } from "react";
import { barOption, chartThemeFor } from "../../../../panels/compile";
import { pointSelection } from "../../../../panels/interaction";
import { EChartCanvas } from "../echart-canvas";
import type { AnalysisProps } from "./analysis-chart";

export function BarViz({ panel, title = panel.title, result, dark, height, onSelect, onPoint }: AnalysisProps) {
  const option = useMemo(() => barOption(panel, result.frame!, chartThemeFor(dark)), [panel, result.frame, dark]);
  return <EChartCanvas option={option} height={height} label={`${title}: bar chart`} onClick={onPoint || onSelect ? event => {
    const selection = pointSelection(panel, result, event);
    if (!selection) return;
    onPoint?.(selection);
    const first = Object.values(selection.dimensions)[0];
    if (first !== undefined) onSelect?.(first);
  } : undefined} />;
}
