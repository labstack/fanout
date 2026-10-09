import { Box, Group, Table, Text, Tooltip } from "@mantine/core";
import type { TraceSpan } from "../../../../contracts";
import { seriesSlot } from "../../../../chart";
import { duration } from "../../../../format";
import { PageControls, usePagedItems } from "./components";
export function Waterfall({
  spans,
  dark,
  onSpan,
}: {
  spans: TraceSpan[];
  dark: boolean;
  onSpan?: (span: TraceSpan) => void;
}) {
  const start = Math.min(
    ...spans.map((span) => new Date(span.start).valueOf()),
  );
  const end = Math.max(
    ...spans.map((span) => new Date(span.start).valueOf() + span.duration_ms),
  );
  const total = Math.max(end - start, 1);
  const services = [...new Set(spans.map(span => span.service))];
  const visibleSpans = usePagedItems(spans, 8);
  return (
    <>
      <Table.ScrollContainer minWidth={680}>
        <Table highlightOnHover={Boolean(onSpan)} verticalSpacing="sm">
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
                  tabIndex={onSpan ? 0 : undefined}
                  onClick={onSpan ? () => onSpan(span) : undefined}
                  onKeyDown={onSpan ? (event) => {
                    if (event.key === "Enter" || event.key === " ") {
                      event.preventDefault();
                      onSpan(span);
                    }
                  } : undefined}
                  style={{ cursor: onSpan ? "pointer" : undefined }}
                >
                  <Table.Td>
                    <Group gap="xs" wrap="nowrap">
                      <Box
                        w={8}
                        h={8}
                        bg={seriesSlot(services.indexOf(span.service), dark)}
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
                          bg={failed ? "bad" : seriesSlot(services.indexOf(span.service), dark)}
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
