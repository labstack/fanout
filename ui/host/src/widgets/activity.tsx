import { ScrollArea, Table, Text } from "@mantine/core";
import type { Overview } from "../../../contracts";
import { duration, percent } from "../../../format";
import { useObservability, widgetParams } from "./data";
import { Empty, HealthBadge, WidgetError } from "./pieces";
import type { WidgetBodyProps } from "./widget-card";

const order: Record<string, number> = { unhealthy: 0, degraded: 1, healthy: 2 };

export default function ActivityWidget({ widget, filters }: WidgetBodyProps) {
  const overview = useObservability<Overview>("overview", widgetParams(filters, widget.config, ["service"]));
  if (overview.isError) return <WidgetError retry={() => void overview.refetch()} />;
  const services = [...(overview.data?.data.services ?? [])].sort((left, right) => (order[left.health] ?? 3) - (order[right.health] ?? 3) || right.error_rate - left.error_rate);
  if (overview.data && services.length === 0) return <Empty text="No recent activity" />;
  return <ScrollArea type="auto" offsetScrollbars h="100%">
    <Table verticalSpacing="xs" fz="sm" highlightOnHover>
      <Table.Thead><Table.Tr><Table.Th>Service</Table.Th><Table.Th ta="right">P95</Table.Th><Table.Th ta="right">Errors</Table.Th></Table.Tr></Table.Thead>
      <Table.Tbody>{services.map((entry) => <Table.Tr key={entry.service}>
        <Table.Td><HealthBadge health={entry.health} label={entry.service} /></Table.Td>
        <Table.Td ta="right"><Text component="span" size="sm" ff="monospace">{duration(entry.p95_ms)}</Text></Table.Td>
        <Table.Td ta="right"><Text component="span" size="sm" c={entry.error_rate >= 0.01 ? "bad" : "dimmed"}>{percent(entry.error_rate)}</Text></Table.Td>
      </Table.Tr>)}</Table.Tbody>
    </Table>
  </ScrollArea>;
}
