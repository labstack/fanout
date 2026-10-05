import { useMemo } from "react";
import { barOption, chartThemeFor } from "../../../../panels/compile";
import type { Panel, PanelResult } from "../../../../panels/types";
import { EChartCanvas } from "../echart-canvas";

export function BarViz({ panel, title = panel.title, result, dark, height, onSelect }: { panel: Panel; title?: string; result: PanelResult; dark: boolean; height: number; onSelect?: (value: string) => void }) {
  const option = useMemo(() => barOption(panel, result.frame!, chartThemeFor(dark)), [panel, result.frame, dark]);
  return <EChartCanvas option={option} height={height} label={`${title}: bar chart`} onClick={onSelect ? (params) => params.name && onSelect(params.name) : undefined} />;
}
