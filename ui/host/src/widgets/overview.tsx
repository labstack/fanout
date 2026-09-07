import { Box, Group, Progress, SimpleGrid, Stack, Text } from "@mantine/core";
import type { Overview, Performance } from "../../../contracts";
import { healthColor, statusHex } from "../../../chart";
import { integer, percent } from "../../../format";
import { useObservability, widgetParams } from "./data";
import { Metric, Sparkline, WidgetError } from "./pieces";
import type { WidgetBodyProps } from "./widget-card";

export default function OverviewWidget({ widget, filters, dark }: WidgetBodyProps) {
  const params = widgetParams(filters, widget.config, ["service"]);
  const overview = useObservability<Overview>("overview", params);
  const performance = useObservability<Performance>("performance", params);
  if (overview.isError) return <WidgetError retry={() => void overview.refetch()} />;
  const data = overview.data?.data;
  const errorTrend = (performance.data?.data.points ?? []).map((point) => point.error_rate * 100);
  const total = Math.max(data?.service_count ?? 0, 1);
  const status = statusHex(dark);
  // An empty window says so. "Healthy · 0 services" is the one reading this
  // tile must never produce: it is the answer a mistyped namespace filter gets,
  // and it looks exactly like a system that is fine.
  const empty = data?.health === "unknown";
  const healthWord = !data ? "—" : empty ? "No data" : data.health.charAt(0).toUpperCase() + data.health.slice(1);
  return <Stack gap="sm">
    <SimpleGrid cols={2} spacing="sm">
      <Metric label="Health" value={healthWord} color={data ? healthColor(data.health) : undefined} hint={data ? (empty ? "Nothing reported in this window" : `${integer.format(data.service_count)} services`) : undefined} />
      <Metric label="Error rate" value={data && !empty ? percent(data.error_rate) : "—"} color={data && data.error_rate >= 0.01 ? "bad" : undefined} hint={data && !empty ? `${integer.format(data.total_spans)} operations` : undefined}>
        {errorTrend.length > 1 && <Sparkline values={errorTrend} color={status.bad} label="Error rate trend" />}
      </Metric>
    </SimpleGrid>
    {data && !empty && <Box>
      <Progress.Root size="md" aria-label="Service health distribution">
        <Progress.Section value={data.counts.healthy / total * 100} color="ok" />
        <Progress.Section value={data.counts.degraded / total * 100} color="warn" />
        <Progress.Section value={data.counts.unhealthy / total * 100} color="bad" />
      </Progress.Root>
      <Group mt={6} gap="md">
        <Legend color="ok" text={`${data.counts.healthy} healthy`} />
        <Legend color="warn" text={`${data.counts.degraded} degraded`} />
        <Legend color="bad" text={`${data.counts.unhealthy} unhealthy`} />
      </Group>
    </Box>}
  </Stack>;
}

function Legend({ color, text }: { color: string; text: string }) {
  return <Group gap={6}><Box w={8} h={8} bg={color} style={{ borderRadius: "50%" }} /><Text c="dimmed" size="xs">{text}</Text></Group>;
}
