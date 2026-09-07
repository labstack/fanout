import { ScrollArea, Table, Text } from "@mantine/core";
import type { KeyboardEvent } from "react";
import type { Overview } from "../../../contracts";
import { duration, percent } from "../../../format";
import { errorRateTone, latencyTone } from "../../../health";
import { useObservability, widgetParams } from "./data";
import { Empty, HealthBadge, WidgetError } from "./pieces";
import type { WidgetBodyProps } from "./widget-card";

const order: Record<string, number> = { unhealthy: 0, degraded: 1, healthy: 2 };

export default function ActivityWidget({ widget, filters, agentAvailable, onOpenChat }: WidgetBodyProps) {
  const overview = useObservability<Overview>("overview", widgetParams(filters, widget.config, ["service"]));
  if (overview.isError) return <WidgetError retry={() => void overview.refetch()} />;
  const services = [...(overview.data?.data.services ?? [])].sort((left, right) => (order[left.health] ?? 3) - (order[right.health] ?? 3) || right.error_rate - left.error_rate);
  if (overview.data && services.length === 0) return <Empty text="No recent activity" />;
  // The rows lit up on hover and did nothing when clicked. They lead somewhere
  // now — the same investigation a service-map node opens — and only look
  // clickable when there is an agent to answer.
  const investigate = (service: string) => onOpenChat(`Investigate the ${service} service. Explain its errors and latency.`);
  return <ScrollArea type="auto" offsetScrollbars h="100%">
    <Table verticalSpacing="xs" fz="sm" highlightOnHover={agentAvailable}>
      <Table.Thead><Table.Tr><Table.Th>Service</Table.Th><Table.Th ta="right">P95</Table.Th><Table.Th ta="right">Errors</Table.Th></Table.Tr></Table.Thead>
      <Table.Tbody>{services.map((entry) => <Table.Tr key={entry.service}
        {...(agentAvailable && {
          onClick: () => investigate(entry.service),
          onKeyDown: (event: KeyboardEvent) => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); investigate(entry.service); } },
          tabIndex: 0,
          role: "button",
          "aria-label": `Investigate ${entry.service}`,
          style: { cursor: "pointer" },
        })}>
        <Table.Td><HealthBadge health={entry.health} label={entry.service} /></Table.Td>
        <Table.Td ta="right"><Text component="span" size="sm" ff="monospace" c={latencyTone(entry.p95_ms)}>{duration(entry.p95_ms)}</Text></Table.Td>
        {/* Each number is coloured by the signal it shows, against the same
            thresholds the badge is computed from. Colouring the rate by the row
            verdict instead put "0.00%" in red on a service that was unhealthy
            purely on latency — red where there were no errors, and nothing on
            the figure that caused it. */}
        <Table.Td ta="right"><Text component="span" size="sm" c={errorRateTone(entry.error_rate)}>{percent(entry.error_rate)}</Text></Table.Td>
      </Table.Tr>)}</Table.Tbody>
    </Table>
  </ScrollArea>;
}
