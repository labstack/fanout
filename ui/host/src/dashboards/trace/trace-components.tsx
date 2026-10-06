import { Badge, Box, Group, Table, Text, Tooltip } from "@mantine/core";
import { ListBullets } from "@phosphor-icons/react";
// Adapted copy of ui/apps/src/trace.tsx (M1 echart.tsx precedent). Consolidate in M3.
import type { LogEntry, TraceSpan } from "../../../../contracts";
import { seriesColor, severityColor } from "../../../../chart";
import { duration, exactTimestamp, timeZoneLabel } from "../../../../format";
import { EmptyState, PageControls, usePagedItems } from "./components";
export function Waterfall({
  spans,
  dark,
  onSpan,
}: {
  spans: TraceSpan[];
  dark: boolean;
  onSpan: (span: TraceSpan) => void;
}) {
  const start = Math.min(
    ...spans.map((span) => new Date(span.start).valueOf()),
  );
  const end = Math.max(
    ...spans.map((span) => new Date(span.start).valueOf() + span.duration_ms),
  );
  const total = Math.max(end - start, 1);
  const visibleSpans = usePagedItems(spans, 8);
  return (
    <>
      <Table.ScrollContainer minWidth={680}>
        <Table highlightOnHover verticalSpacing="sm">
          <Table.Thead>
            <Table.Tr>
              <Table.Th w={230}>Operation</Table.Th>
              <Table.Th>Timeline</Table.Th>
              <Table.Th w={90} ta="right">
                Duration
              </Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {visibleSpans.pageItems.map((span) => {
              const offset =
                ((new Date(span.start).valueOf() - start) / total) * 100;
              const width = Math.max((span.duration_ms / total) * 100, 0.6);
              const failed = span.status.toUpperCase().includes("ERROR");
              return (
                <Table.Tr
                  key={span.span_id}
                  tabIndex={0}
                  onClick={() => onSpan(span)}
                  onKeyDown={(event) => {
                    if (event.key === "Enter" || event.key === " ")
                      onSpan(span);
                  }}
                  style={{ cursor: "pointer" }}
                >
                  <Table.Td>
                    <Group gap="xs" wrap="nowrap">
                      <Box
                        w={8}
                        h={8}
                        bg={seriesColor(span.service, dark)}
                        style={{ borderRadius: "50%", flex: "0 0 auto" }}
                      />
                      <Box miw={0}>
                        <Text fw={600} size="sm" truncate>
                          {span.operation}
                        </Text>
                        <Text c="dimmed" size="xs" truncate>
                          {span.service}
                        </Text>
                      </Box>
                    </Group>
                  </Table.Td>
                  <Table.Td>
                    <Tooltip
                      label={`${span.service} · ${span.operation} · ${duration(span.duration_ms)}`}
                      withArrow
                    >
                      <Box
                        pos="relative"
                        h={14}
                        bg="var(--mantine-color-default-hover)"
                        role="img"
                        aria-label={`${span.operation} on ${span.service} took ${duration(span.duration_ms)}`}
                        style={{ borderRadius: "var(--mantine-radius-sm)" }}
                      >
                        <Box
                          pos="absolute"
                          left={`${offset}%`}
                          w={`${Math.min(width, 100 - offset)}%`}
                          h="100%"
                          bg={failed ? "bad" : seriesColor(span.service, dark)}
                          style={{
                            borderRadius: "var(--mantine-radius-sm)",
                            minWidth: 3,
                          }}
                        />
                      </Box>
                    </Tooltip>
                  </Table.Td>
                  <Table.Td ta="right">
                    <Text size="sm" ff="monospace">
                      {duration(span.duration_ms)}
                    </Text>
                  </Table.Td>
                </Table.Tr>
              );
            })}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      <PageControls {...visibleSpans} onChange={visibleSpans.setPage} />
    </>
  );
}

export function TraceLogs({ entries }: { entries: LogEntry[] }) {
  const logs = usePagedItems(entries, 6);
  if (entries.length === 0)
    return (
      <EmptyState
        tall
        icon={<ListBullets size={20} weight="duotone" />}
        title="No correlated logs"
      >
        No logs in this window carry the selected trace ID.
      </EmptyState>
    );
  return (
    <>
      <Table.ScrollContainer minWidth={620}>
        <Table striped verticalSpacing="xs">
          <Table.Thead>
            <Table.Tr>
              <Table.Th>
                Time ({timeZoneLabel(logs.pageItems[0]?.time)})
              </Table.Th>
              <Table.Th>Level</Table.Th>
              <Table.Th>Service</Table.Th>
              <Table.Th>Message</Table.Th>
            </Table.Tr>
          </Table.Thead>
          <Table.Tbody>
            {logs.pageItems.map((entry, index) => (
              <Table.Tr key={`${entry.time}-${logs.from + index}`}>
                <Table.Td style={{ whiteSpace: "nowrap" }}>
                  <Text
                    size="xs"
                    ff="monospace"
                    title={exactTimestamp(entry.time)}
                  >
                    {new Date(entry.time).toLocaleTimeString([], {
                      hour: "numeric",
                      minute: "2-digit",
                      second: "2-digit",
                    })}
                  </Text>
                </Table.Td>
                <Table.Td>
                  <Badge
                    size="sm"
                    color={severityColor(entry.severity)}
                    variant="light"
                  >
                    {entry.severity || "LOG"}
                  </Badge>
                </Table.Td>
                <Table.Td>
                  <Text fw={600} size="sm">
                    {entry.service}
                  </Text>
                </Table.Td>
                <Table.Td>
                  <Text size="sm" lineClamp={2} title={entry.body}>
                    {entry.body}
                  </Text>
                </Table.Td>
              </Table.Tr>
            ))}
          </Table.Tbody>
        </Table>
      </Table.ScrollContainer>
      <PageControls {...logs} onChange={logs.setPage} />
    </>
  );
}
