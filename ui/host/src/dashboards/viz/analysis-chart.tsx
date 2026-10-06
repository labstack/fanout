import { useMemo } from "react";
import { analysisOption, analysisSummary } from "../../../../panels/analysis";
import { chartThemeFor } from "../../../../panels/compile";
import type { Panel, PanelResult, Selection } from "../../../../panels/types";
import { EChartCanvas } from "../echart-canvas";

export type AnalysisProps = {
  panel: Panel; title?: string; result: PanelResult; dark: boolean; height: number; group?: string;
  onSelect?: (value: string) => void; onPoint?: (selection: Selection) => void; onZoom?: (from: number, to: number) => void;
};

export function AnalysisChart({ panel, title, result, dark, height, group, onSelect, onPoint, onZoom }: AnalysisProps) {
  const option = useMemo(() => analysisOption(panel, result, chartThemeFor(dark)), [panel, result.frame, result.interval, result.better, dark]);
  const label = analysisSummary(title ? { ...panel, title } : panel, result);
  const time = panel.viz === "heatmap" || panel.viz === "state_timeline";
  return <EChartCanvas option={option} height={height} label={label} group={time ? group : undefined} onZoom={time ? onZoom : undefined} onClick={onPoint || onSelect ? event => {
    const selection = (event.data as { selection?: Selection } | undefined)?.selection;
    if (selection) {
      onPoint?.(selection);
      const value = Object.values(selection.dimensions)[0];
      if (value !== undefined) onSelect?.(value);
    }
  } : undefined} />;
}
