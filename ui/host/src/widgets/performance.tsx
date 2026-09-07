import { LineChart } from "echarts/charts";
import { Badge, Box, Stack, Table, Text } from "@mantine/core";
import { useMemo } from "react";
import type { Performance } from "../../../contracts";
import { chartTheme, seriesColor, statusHex } from "../../../chart";
import { typeScale } from "../../../tokens";
import { duration, integer, timelineTimestamp } from "../../../format";
import { EChart, useECharts } from "../echart";
import { useObservability, widgetParams } from "./data";
import { Empty, WidgetError } from "./pieces";
import type { WidgetBodyProps } from "./widget-card";

useECharts([LineChart]);

export default function PerformanceWidget({ widget, filters, dark }: WidgetBodyProps) {
  const performance = useObservability<Performance>("performance", widgetParams(filters, widget.config, ["service"]));
  const result = performance.data;
  const option = useMemo(() => {
    if (!result || result.data.points.length === 0) return null;
    const colors = chartTheme(dark);
    const window = result.provenance.window;
    return {
      color: [seriesColor("operations", dark), statusHex(dark).warn],
      grid: { left: 36, right: 40, top: 24, bottom: 22 },
      legend: { top: 0, left: 0, textStyle: { color: colors.muted, fontSize: typeScale.micro }, icon: "circle", itemWidth: 7, itemHeight: 7 },
      tooltip: { trigger: "axis", backgroundColor: colors.surface, borderColor: colors.border, textStyle: { color: colors.text, fontSize: typeScale.micro } },
      xAxis: { type: "category", data: result.data.points.map((point) => timelineTimestamp(point.time, window)), boundaryGap: false, axisLine: { lineStyle: { color: colors.border } }, axisTick: { show: false }, axisLabel: { color: colors.muted, fontSize: typeScale.micro, hideOverlap: true } },
      yAxis: [
        { type: "value", splitLine: { lineStyle: { color: colors.grid } }, axisLabel: { color: colors.muted, fontSize: typeScale.micro } },
        { type: "value", splitLine: { show: false }, axisLabel: { color: colors.muted, fontSize: typeScale.micro, formatter: (value: number) => duration(value) } },
      ],
      series: [
        { name: "Operations", type: "line", data: result.data.points.map((point) => point.spans), smooth: 0.22, showSymbol: false, lineStyle: { width: 2 }, areaStyle: { opacity: 0.05 } },
        { name: "P95", type: "line", yAxisIndex: 1, data: result.data.points.map((point) => point.p95_ms), smooth: 0.22, showSymbol: false, lineStyle: { width: 2 } },
      ],
    };
  }, [dark, result]);
  if (performance.isError) return <WidgetError retry={() => void performance.refetch()} />;
  if (result && result.data.points.length === 0) return <Empty text="No endpoint activity in this window" />;
  const endpoints = (result?.data.endpoints ?? []).slice(0, 3);
  return <Stack gap="xs" h="100%">
    {option && <Box style={{ flex: 1, minHeight: 120 }}><EChart option={option} height="100%" label="Operations and P95 latency" /></Box>}
    {endpoints.length > 0 && <Table verticalSpacing={4} fz="sm">
      <Table.Tbody>{endpoints.map((endpoint) => <Table.Tr key={`${endpoint.method}-${endpoint.path}`}>
        {/* The path beside it can take the whole cell; a squeezed Badge
            collapses its grid track and loses the method. */}
        <Table.Td><Badge variant="light" size="xs" mr={6} style={{ minWidth: "max-content" }}>{endpoint.method}</Badge><Text component="span" size="sm" ff="monospace">{endpoint.path}</Text></Table.Td>
        <Table.Td ta="right"><Text component="span" size="sm" c="dimmed">{integer.format(endpoint.calls)} calls</Text></Table.Td>
        <Table.Td ta="right"><Text component="span" size="sm" ff="monospace">{duration(endpoint.p95_ms)}</Text></Table.Td>
      </Table.Tr>)}</Table.Tbody>
    </Table>}
  </Stack>;
}
