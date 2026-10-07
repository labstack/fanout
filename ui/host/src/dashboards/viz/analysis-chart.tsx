import { useMemo } from "react";
import { analysisOption, analysisSummary } from "../../../../panels/analysis";
import { chartThemeFor, type ChartSize } from "../../../../panels/compile";
import type { Panel, PanelResult, Selection, VarValue } from "../../../../panels/types";
import { withAnnotations, type AnnotationsResponse } from "../../../../panels/annotations";
import { EChartCanvas } from "../echart-canvas";

export type AnalysisProps = {
  panel: Panel; title?: string; result: PanelResult; dark: boolean; height: number; group?: string;
  annotations?: AnnotationsResponse; vars?: Record<string, VarValue>;
  onSelect?: (value: string) => void; onPoint?: (selection: Selection) => void; onZoom?: (from: number, to: number) => void;
};

export function AnalysisChart({ panel, title, result, dark, height, group, annotations, vars, onSelect, onPoint, onZoom }: AnalysisProps) {
  const optionForSize = useMemo(() => (size: ChartSize) => {
    const theme = chartThemeFor(dark);
    const compiled = analysisOption(panel, result, theme, size);
    return annotations ? withAnnotations(compiled, panel, result, annotations, vars ?? {}, theme, size) : compiled;
  }, [panel, result.frame, result.interval, result.better, result.from_ms, result.to_ms, result.annotation_scope, result.annotation_error, dark, annotations]);
  const label = analysisSummary(title ? { ...panel, title } : panel, result);
  const option = useMemo(() => optionForSize({ width: 500, height }), [optionForSize, height]);
  const time = panel.viz === "heatmap" || panel.viz === "state_timeline";
  return <EChartCanvas option={option} optionForSize={optionForSize} height={height} label={label} group={time ? group : undefined} onZoom={time ? onZoom : undefined} onClick={onPoint || onSelect ? event => {
    const selection = (event.data as { selection?: Selection } | undefined)?.selection;
    if (selection) {
      onPoint?.(selection);
      const value = Object.values(selection.dimensions)[0];
      if (value !== undefined) onSelect?.(value);
    }
  } : undefined} />;
}
