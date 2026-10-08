import { Anchor, Box, Group, Progress, SimpleGrid, Stack, Text } from "@mantine/core";
import { healthColor, statusHex } from "../../../../chart";
import { integer } from "../../../../format";
import type { AnalysisProps } from "./analysis-chart";
import { frameRows } from "../../../../panels/rows";
import { worstHealthServices } from "../../../../panels/rollups";
import { Metric, HealthShape, HealthTrend } from "./health-pieces";

/** Task 6's overview tiles, with accessible shapes and sanitized frame metadata. */
export function HealthViz({ result, dark, onSelect, onVariable, vars }: AnalysisProps) {
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
  const select = onSelect ?? (onVariable && vars && Object.hasOwn(vars,"service") ? (value:string)=>onVariable("service",value) : undefined);
  const worst = empty ? [] : worstHealthServices(frameRows(result.frame!));
  return <Stack gap="sm" style={{ flex: "1 1 auto", minHeight: 0 }} role="region" aria-label={`Service health: ${empty ? "No data" : health}; ${data?.service_count ?? 0} services`}>
    <SimpleGrid cols={2} spacing="sm" data-health-tiles style={{ flex: "1 1 auto", minHeight: 0 }}>
      <Metric label="Health" value={<Group gap={6}><HealthShape health={health} />{empty ? "No data" : health.charAt(0).toUpperCase() + health.slice(1)}</Group>}
        color={healthColor(health)} hint={empty ? "No telemetry in this window" : `${integer.format(data.service_count)} services`}>
        {worst.map(service => {
          const content=<><HealthShape health={service.health}/><Text component="span" fz={12} truncate style={{flex:1}} title={service.name}>{service.name}</Text><Text component="span" fz={12} ff="monospace" style={{flexShrink:0}}>{service.metric}</Text></>;
          const style={display:"flex",alignItems:"center",gap:6,minHeight:20,minWidth:0};
          return select ? <Anchor key={service.name} component="button" type="button" data-health-service={service.name} style={style} onClick={()=>select(service.name)}>{content}</Anchor> : <Box key={service.name} data-health-service={service.name} style={style}>{content}</Box>;
        })}
      </Metric>
      <Metric label="Error rate" value={empty ? "—" : `${data.error_rate.toFixed(2)}%`} color={!empty && data.error_rate >= 1 ? "bad" : undefined}
        hint={empty ? undefined : `${integer.format(data.total_spans)} operations`}>
        {!empty && data.error_trend.length > 1 && <HealthTrend values={data.error_trend} color={status.bad} />}
      </Metric>
    </SimpleGrid>
    {!empty && <Box style={{ flexShrink: 0 }}>
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
