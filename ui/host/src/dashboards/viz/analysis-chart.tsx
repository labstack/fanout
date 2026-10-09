import { useCallback, useMemo } from "react";
import { analysisOption, analysisSummary } from "../../../../panels/analysis";
import { chartThemeFor, type ChartSize } from "../../../../panels/compile";
import type { Panel, PanelResult, Selection, VarValue } from "../../../../panels/types";
import { withAnnotations, type AnnotationsResponse } from "../../../../panels/annotations";
import { pointSelection } from "../../../../panels/interaction";
import { EChartCanvas } from "../echart-canvas";

export type AnalysisProps = {
  panel: Panel; title?: string; result: PanelResult; dark: boolean; height: number; group?: string;
  foldConstants?: boolean; traceLinks?: "button"; annotations?: AnnotationsResponse; vars?: Record<string, VarValue>;
  onVariable?: (name: string, value: string) => void; onSelect?: (value: string) => void; onPoint?: (selection: Selection) => void; onZoom?: (from: number, to: number) => void; onRangePending?(pending: boolean): void;
};

export function AnalysisChart({ panel, title, result, dark, height, group, annotations, vars, onSelect, onPoint, onZoom, onRangePending }: AnalysisProps) {
  const optionForSize = useMemo(() => (size: ChartSize) => {
    const theme = chartThemeFor(dark);
    const compiled = analysisOption(panel, result, theme, size);
    return annotations ? withAnnotations(compiled, panel, result, annotations, vars ?? {}, theme, size) : compiled;
  }, [panel, result.frame, result.interval, result.better, result.from_ms, result.to_ms, result.annotation_scope, result.annotation_error, dark, annotations]);
  const label = analysisSummary(title ? { ...panel, title } : panel, result);
  const option = useMemo(() => optionForSize({ width: 500, height }), [optionForSize, height]);
  const canSelect = useCallback((event: Parameters<typeof pointSelection>[2]) => Boolean((event.data as {selection?: Selection} | undefined)?.selection && pointSelection(panel, result, event)), [panel, result]);
  const time = panel.viz === "heatmap" || panel.viz === "state_timeline";
  return <EChartCanvas keyboard={{canSelect, onRangePending, bounds: time && result.from_ms !== undefined && result.to_ms !== undefined ? {from: result.from_ms, to: result.to_ms} : undefined}} option={option} optionForSize={optionForSize} height={height} label={label} group={time ? group : undefined} onZoom={time ? onZoom : undefined} onClick={onPoint || onSelect ? event => {
    const selection = canSelect(event) ? pointSelection(panel, result, event) : undefined;
    if (selection) {
      onPoint?.(selection);
      const value = Object.values(selection.dimensions)[0];
      if (value !== undefined) onSelect?.(value);
    }
  } : undefined} />;
}
