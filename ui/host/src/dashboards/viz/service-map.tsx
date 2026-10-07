import { Stack, Text } from "@mantine/core";
import { useMemo } from "react";
import { chartThemeFor } from "../../../../panels/compile";
import { analysisSummary } from "../../../../panels/analysis";
import { serviceMapOption } from "../../../../panels/rollups";
import type { AnalysisProps } from "./analysis-chart";
import type { Selection } from "../../../../panels/types";
import { EChartCanvas } from "../echart-canvas";

export function ServiceMapViz({ panel, title = panel.title, result, dark, height, onSelect, onPoint }: AnalysisProps) {
  const option = useMemo(() => serviceMapOption(result.frame!, chartThemeFor(dark)), [result.frame, dark]);
  const graph = option.series[0];
  return <Stack gap={4} style={{ flex: "1 1 auto", minHeight: 0 }}>
    <EChartCanvas option={option} height={Math.max(60, height - 24)} label={`${title}: service dependency graph; ${analysisSummary({ ...panel, title }, result)}`} onClick={onSelect || onPoint ? (params) => {
      const item = params as { dataType?: string; name?: string; data?: { selection?: Selection } };
      if (item.dataType === "node" && item.name) {
        onSelect?.(item.name);
        onPoint?.(item.data?.selection ?? { dimensions: { service: item.name } });
      }
    } : undefined} />
    <Text c="dimmed" size="xs" style={{ flexShrink: 0 }}>{graph.data.length} services · {graph.links.length} {graph.links.length === 1 ? "route" : "routes"}</Text>
  </Stack>;
}
