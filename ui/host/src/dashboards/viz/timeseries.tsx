import { useMemo } from "react";
import { chartThemeFor, timeseriesOption } from "../../../../panels/compile";
import type { Panel, PanelResult } from "../../../../panels/types";
import { EChartCanvas } from "../echart-canvas";

export function TimeseriesViz({ panel, title = panel.title, result, dark, height, group, onSelect }: { panel: Panel; title?: string; result: PanelResult; dark: boolean; height: number; group: string; onSelect?: (value: string) => void }) {
  const option = useMemo(() => timeseriesOption(panel, result, chartThemeFor(dark)), [panel, result.frame, result.previous, result.shift_ms, dark]);
  const select = onSelect && result.frame!.columns.some((column) => column.role === "dimension") ? (params: { seriesName?: string }) => { if (params.seriesName && !params.seriesName.endsWith(" · previous") && params.seriesName !== "Other") onSelect(params.seriesName); } : undefined;
  return <EChartCanvas option={option} height={height} label={`${title}: time series`} group={group} onClick={select} />;
}
