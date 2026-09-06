import { BarChart } from "echarts/charts";
import { Badge, Box, ScrollArea, Stack, Table, Text } from "@mantine/core";
import { useMemo } from "react";
import type { Logs } from "../../../contracts";
import { chartTheme, severityColor, severityHex } from "../../../chart";
import { timelineTimestamp } from "../../../format";
import { EChart, useECharts } from "../echart";
import { useObservability, widgetParams } from "./data";
import { Empty, WidgetError } from "./pieces";
import type { WidgetBodyProps } from "./widget-card";

useECharts([BarChart]);

export default function LogsWidget({ widget, filters, dark }: WidgetBodyProps) {
  const logs = useObservability<Logs>("logs", widgetParams(filters, widget.config, ["service", "severity", "search"]));
  const result = logs.data;
  const option = useMemo(() => {
    if (!result || result.data.buckets.length === 0) return null;
    const colors = chartTheme(dark);
    const window = result.provenance.window;
    const times = [...new Set(result.data.buckets.map((bucket) => bucket.time))];
    const severities = [...new Set(result.data.buckets.map((bucket) => bucket.severity))];
    const values = new Map(result.data.buckets.map((bucket) => [`${bucket.time} ${bucket.severity}`, bucket.count]));
    return {
      color: severities.map((severity) => severityHex(severity, dark)),
      grid: { left: 28, right: 8, top: 8, bottom: 20 },
      tooltip: { trigger: "axis", axisPointer: { type: "shadow" }, backgroundColor: colors.surface, borderColor: colors.border, textStyle: { color: colors.text, fontSize: 10 } },
      xAxis: { type: "category", data: times.map((time) => timelineTimestamp(time, window)), axisLabel: { color: colors.muted, fontSize: 8, hideOverlap: true }, axisLine: { lineStyle: { color: colors.border } } },
      yAxis: { type: "value", minInterval: 1, splitLine: { lineStyle: { color: colors.grid } }, axisLabel: { color: colors.muted, fontSize: 8 } },
      series: severities.map((severity) => ({ name: severity, type: "bar", stack: "logs", barMaxWidth: 14, data: times.map((time) => values.get(`${time} ${severity}`) ?? 0), itemStyle: { borderRadius: [2, 2, 0, 0] } })),
    };
  }, [dark, result]);
  if (logs.isError) return <WidgetError retry={() => void logs.refetch()} />;
  const entries = (result?.data.entries ?? []).slice(0, 6);
  if (result && entries.length === 0) return <Empty text="No matching logs in this window" />;
  return <Stack gap="xs" h="100%">
    {option && <Box h={72}><EChart option={option} height={72} label="Log volume by severity" /></Box>}
    <ScrollArea type="auto" offsetScrollbars style={{ flex: 1 }}>
      <Table verticalSpacing={4} fz="sm">
        <Table.Tbody>{entries.map((entry, index) => <Table.Tr key={`${entry.time}-${index}`}>
          <Table.Td style={{ whiteSpace: "nowrap" }}><Text component="span" size="xs" ff="monospace" c="dimmed">{result ? timelineTimestamp(entry.time, result.provenance.window, true) : entry.time}</Text></Table.Td>
          {/* The body cell takes the whole row, so every other cell has to say
              it will not be squeezed: a shrunk badge loses INFO/WARN/ERROR and
              leaves severity as colour alone. */}
          <Table.Td style={{ whiteSpace: "nowrap", width: 1 }}><Badge size="xs" color={severityColor(entry.severity)} variant="light">{entry.severity || "LOG"}</Badge></Table.Td>
          <Table.Td style={{ whiteSpace: "nowrap" }}><Text component="span" size="sm" fw={600}>{entry.service}</Text></Table.Td>
          <Table.Td style={{ width: "100%" }}><Text size="sm" lineClamp={1} title={entry.body}>{entry.body}</Text></Table.Td>
        </Table.Tr>)}</Table.Tbody>
      </Table>
    </ScrollArea>
  </Stack>;
}
