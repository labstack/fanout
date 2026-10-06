import { Box, Group, Progress, SimpleGrid, Stack, Text } from "@mantine/core";
import { healthColor, statusHex } from "../../../../chart";
import { integer } from "../../../../format";
import type { AnalysisProps } from "./analysis-chart";
import { Metric, HealthShape, HealthTrend } from "./health-pieces";

/** Task 6's overview tiles, with accessible shapes and sanitized frame metadata. */
export function HealthViz({ result, dark }: AnalysisProps) {
  const source = result.frame?.health;
  const finite = (value: number) => Number.isFinite(value) ? value : 0;
  const data = source ? {
    ...source, total_spans: finite(source.total_spans), service_count: finite(source.service_count),
    counts: { healthy: finite(source.counts.healthy), degraded: finite(source.counts.degraded), unhealthy: finite(source.counts.unhealthy) },
  } : undefined;
  const empty = !data || data.health === "unknown" || data.service_count === 0 || !Number.isFinite(data.error_rate);
  const health = empty ? "unknown" : data.health;
  const total = Math.max(data?.service_count ?? 0, 1);
  const status = statusHex(dark);
  return <Stack gap="sm" role="region" aria-label={`Service health: ${empty ? "No data" : health}; ${data?.service_count ?? 0} services`}>
    <SimpleGrid cols={2} spacing="sm">
      <Metric label="Health" value={<Group gap={6}><HealthShape health={health} />{empty ? "No data" : health.charAt(0).toUpperCase() + health.slice(1)}</Group>}
        color={healthColor(health)} hint={empty ? "No telemetry in this window" : `${integer.format(data.service_count)} services`} />
      <Metric label="Error rate" value={empty ? "—" : `${data.error_rate.toFixed(2)}%`} color={!empty && data.error_rate >= 1 ? "bad" : undefined}
        hint={empty ? undefined : `${integer.format(data.total_spans)} operations`}>
        {!empty && data.error_trend.length > 1 && <HealthTrend values={data.error_trend} color={status.bad} />}
      </Metric>
    </SimpleGrid>
    {!empty && <Box>
      <Progress.Root size="md" aria-label="Service health distribution">
        <Progress.Section value={data.counts.healthy / total * 100} color="ok" />
        <Progress.Section value={data.counts.degraded / total * 100} color="warn" />
        <Progress.Section value={data.counts.unhealthy / total * 100} color="bad" />
      </Progress.Root>
      <Group mt={6} gap="md">
        {(["healthy", "degraded", "unhealthy"] as const).map(health => <Group key={health} gap={6}>
          <HealthShape health={health} /><Text c="dimmed" size="xs">{data.counts[health]} {health}</Text>
        </Group>)}
      </Group>
    </Box>}
  </Stack>;
}
