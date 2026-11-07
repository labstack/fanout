import { Group, Stack } from "@mantine/core";
import { StatusBadge } from "@labstack/ui";
import { formatValue } from "../../../../panels/units";
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
  return <Stack gap={4} style={{minHeight:0, flex:1, overflow:"auto"}}>
    <EChartCanvas option={option} optionForSize={optionForSize} height={Math.max(56, height - (panel.thresholds?.length ? 28 : 0))} label={`${title}: gauge`} />
    {!!panel.thresholds?.length && <Group gap="xs" aria-label="Gauge thresholds">{panel.thresholds.map((t,i) => <StatusBadge key={i} tone={t.status === "bad" ? "danger" : t.status === "warn" ? "warning" : "success"}>{t.label ?? (t.status === "bad" ? "Unhealthy" : t.status === "warn" ? "Degraded" : "Healthy")} {better === "higher" ? "≤" : "≥"} {formatValue(unit,t.value)}</StatusBadge>)}</Group>}
  </Stack>;
}
