import { Box, Group, Paper, Progress, SimpleGrid, Stack, Text } from "@mantine/core";
import { useMemo, type ReactNode } from "react";
import { healthColor, statusHex } from "../../../../chart";
import { integer } from "../../../../format";
import { healthGlyph } from "../../../../panels/rollups";
import type { PanelResult } from "../../../../panels/types";
import { EChartCanvas } from "../echart-canvas";

/** Port of the overview widget. All data arrives with the panel query. */
export function HealthViz({ result, dark }: { result: PanelResult; dark: boolean }) {
  const data = result.frame?.health;
  if (!data) return null;
  const empty = data.health === "unknown";
  const word = empty ? "No data" : data.health.charAt(0).toUpperCase() + data.health.slice(1);
  const total = Math.max(data.service_count, 1);
  return <Stack gap="sm">
    <SimpleGrid cols={2} spacing="sm">
      <Metric label="Health" value={<><span aria-hidden>{healthGlyph[data.health] ?? healthGlyph.unknown}</span> {word}</>} color={healthColor(data.health)} hint={empty ? "No telemetry in this window" : `${integer.format(data.service_count)} services`} />
      <Metric label="Error rate" value={empty ? "—" : `${data.error_rate.toFixed(2)}%`} color={data.error_rate >= 1 ? "bad" : undefined} hint={empty ? undefined : `${integer.format(data.total_spans)} operations`}>
        {data.error_trend.length > 1 && <ErrorTrend values={data.error_trend} dark={dark} />}
      </Metric>
    </SimpleGrid>
    {!empty && <Box>
      <Progress.Root size="md" aria-label="Service health distribution">
        <Progress.Section value={data.counts.healthy / total * 100} color="ok" />
        <Progress.Section value={data.counts.degraded / total * 100} color="warn" />
        <Progress.Section value={data.counts.unhealthy / total * 100} color="bad" />
      </Progress.Root>
      <Group mt={6} gap="md">
        {(["healthy", "degraded", "unhealthy"] as const).map((health) => <Group key={health} gap={6}>
          <Text component="span" c={healthColor(health)} size="xs" aria-hidden>{healthGlyph[health]}</Text>
          <Text c="dimmed" size="xs">{data.counts[health]} {health}</Text>
        </Group>)}
      </Group>
    </Box>}
  </Stack>;
}

function Metric({ label, value, color, hint, children }: { label: string; value: ReactNode; color?: string; hint?: string; children?: ReactNode }) {
  return <Paper withBorder radius="md" p="sm" bg="var(--mantine-color-default)" miw={0} h="100%">
    <Text c="dimmed" size="xs" truncate>{label}</Text>
    <Text fw={600} fz="xl" c={color} mt={2} lh={1.2} truncate>{value}</Text>
    {hint && <Text c="dimmed" size="xs" mt={2} truncate>{hint}</Text>}
    {children && <Box mt={4}>{children}</Box>}
  </Paper>;
}

function ErrorTrend({ values, dark }: { values: number[]; dark: boolean }) {
  const color = statusHex(dark).bad;
  const peak = Math.max(...values);
  const option = useMemo(() => ({ animation: false, grid: { left: 0, right: 0, top: 2, bottom: 2 },
    xAxis: { type: "category", show: false, data: values.map((_, i) => i) }, yAxis: { type: "value", show: false, min: 0 }, tooltip: { show: false },
    aria: { enabled: true, decal: { show: false } },
    series: [{ type: "line", data: values, showSymbol: false, smooth: 0.3, lineStyle: { width: 1.5, color }, areaStyle: { opacity: 0.12, color } }],
  }), [values, color]);
  return <Box><EChartCanvas option={option} height={28} label="Error rate trend" />
    {peak > 0 && <Text c="dimmed" size="xs" ta="right" mt={2}>peak {peak.toFixed(2)}%</Text>}
  </Box>;
}
