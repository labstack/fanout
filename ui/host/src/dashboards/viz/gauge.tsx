import { useMemo } from "react";
import { chartThemeFor, gaugeOption, type ChartSize } from "../../../../panels/compile";
import { statValue } from "../../../../panels/frame";
import type { Panel, PanelResult } from "../../../../panels/types";
import { EChartCanvas } from "../echart-canvas";

export function GaugeViz({ panel, title = panel.title, result, dark, height }: { panel: Panel; title?: string; result: PanelResult; dark: boolean; height: number }) {
  const value = statValue(panel, result.frame!);
  const unit = result.frame!.columns.find((c) => c.role === "measure")?.unit ?? panel.unit;
  const better = panel.better ?? result.better;
  const optionForSize = useMemo(() => (size: ChartSize) => gaugeOption({ ...panel, better }, value, chartThemeFor(dark), unit, size), [panel, better, value, dark, unit]);
  const option = useMemo(() => optionForSize({ width: 270, height }), [optionForSize, height]);
  return <EChartCanvas option={option} optionForSize={optionForSize} height={height} label={`${title}: gauge`} />;
}
