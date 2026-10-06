import { Stack, Text } from "@mantine/core";
import { useMemo } from "react";
import { chartThemeFor } from "../../../../panels/compile";
import { serviceMapOption } from "../../../../panels/rollups";
import type { Panel, PanelResult } from "../../../../panels/types";
import { EChartCanvas } from "../echart-canvas";

export function ServiceMapViz({ panel, title = panel.title, result, dark, height, onSelect }: { panel: Panel; title?: string; result: PanelResult; dark: boolean; height: number; onSelect?: (value: string) => void }) {
  const option = useMemo(() => serviceMapOption(result.frame!, chartThemeFor(dark)), [result.frame, dark]);
  const graph = option.series[0];
  return <Stack gap={4}>
    <EChartCanvas option={option} height={Math.max(60, height - 24)} label={`${title}: service dependency graph`} onClick={onSelect ? (params) => {
      const item = params as { dataType?: string; name?: string };
      if (item.dataType === "node" && item.name) onSelect(item.name);
    } : undefined} />
    <Text c="dimmed" size="xs">{graph.data.length} services · {graph.links.length} {graph.links.length === 1 ? "route" : "routes"}</Text>
  </Stack>;
}
