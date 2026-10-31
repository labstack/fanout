import { Alert, Badge, Box, CloseButton, Code, Group, Loader, Paper, Stack, Table, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import type { TraceSpan } from "../../../../contracts";
import { duration } from "../../../../format";

export type SpanEvent = { time_unix_nano: number; name: string; attributes?: Record<string, string> };
/** What the trace read leaves out of each span, read on demand for the selected one. */
export type SpanExtras = { attributes: Record<string, unknown>; resource: Record<string, unknown>; events: SpanEvent[]; scope: string };

const statuses: Record<string, string> = { STATUS_CODE_ERROR: "Error", STATUS_CODE_OK: "OK", STATUS_CODE_UNSET: "Unset" };

/** Attributes as a key/value table; nested values print as JSON. */
export function Attributes({ label, values }: { label: string; values: Record<string, unknown> }) {
  const keys = Object.keys(values).sort();
  return <Box>
    <Text size="xs" fw={600} c="dimmed" mb={4}>{label}</Text>
    {keys.length === 0 ? <Text size="xs" c="dimmed">None</Text>
      : <Table fz={12} verticalSpacing={2} layout="fixed" aria-label={label}><Table.Tbody>{keys.map(key => <Table.Tr key={key}>
        <Table.Td w="38%" ff="monospace" c="dimmed" style={{ overflowWrap: "anywhere" }}>{key}</Table.Td>
        <Table.Td ff="monospace" style={{ overflowWrap: "anywhere", whiteSpace: "pre-wrap" }}>{typeof values[key] === "string" ? String(values[key]) : JSON.stringify(values[key])}</Table.Td>
      </Table.Tr>)}</Table.Tbody></Table>}
  </Box>;
}

export type SpanLoader = (traceId: string, span: TraceSpan, signal?: AbortSignal) => Promise<SpanExtras>;

export function SpanDetails({ traceId, span, load, onClose }: { traceId: string; span: TraceSpan; load?: SpanLoader; onClose(): void }) {
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => { box.current?.scrollIntoView?.({ block: "nearest" }); }, [span]);
  const extras = useQuery({ queryKey: ["span-extras", traceId, span.span_id, span.start], queryFn: ({ signal }) => load!(traceId, span, signal), enabled: Boolean(load), retry: false, staleTime: Infinity });
  const failed = span.status === "STATUS_CODE_ERROR";
  const exceptions = extras.data?.events.filter(event => event.name === "exception") ?? [];
  return <Paper ref={box} withBorder p="sm" radius="md" data-span-details aria-label={`Span ${span.operation}`} role="region">
    <Stack gap="sm">
      <Group justify="space-between" wrap="nowrap" align="flex-start">
        <Box miw={0}>
          <Text fw={600} style={{ overflowWrap: "anywhere" }}>{span.operation}</Text>
          <Text size="xs" c="dimmed">{span.service} · {span.kind.replace(/^SPAN_KIND_/, "").toLowerCase()} · {duration(span.duration_ms)} · {new Date(span.start).toISOString()}</Text>
        </Box>
        <CloseButton aria-label="Close span details" onClick={onClose} />
      </Group>
      <Group gap="xs">
        <Badge variant="light" color={failed ? "bad" : "gray"}>{statuses[span.status] ?? span.status}</Badge>
        <Text size="xs" ff="monospace" c="dimmed">span {span.span_id}{span.parent_span_id ? ` · parent ${span.parent_span_id}` : ""}</Text>
      </Group>
      {span.status_message && <Text size="sm" c={failed ? "bad" : undefined} style={{ overflowWrap: "anywhere" }}>{span.status_message}</Text>}
      {extras.isFetching && <Loader size="xs" role="status" aria-label="Loading span details" />}
      {extras.error && <Alert color="bad" p="xs">{extras.error.message}</Alert>}
      {exceptions.map((event, index) => <Alert key={index} color="bad" p="xs" title={event.attributes?.["exception.type"] ?? "Exception"}>
        {event.attributes?.["exception.message"] && <Text size="sm" style={{ overflowWrap: "anywhere" }}>{event.attributes["exception.message"]}</Text>}
        {event.attributes?.["exception.stacktrace"] && <Code block mt={4} fz={11} style={{ maxHeight: 220, overflow: "auto" }}>{event.attributes["exception.stacktrace"]}</Code>}
      </Alert>)}
      {extras.data && <>
        <Attributes label="Attributes" values={extras.data.attributes} />
        {extras.data.events.length > 0 && <Box>
          <Text size="xs" fw={600} c="dimmed" mb={4}>Events</Text>
          <Stack gap={4}>{extras.data.events.map((event, index) => <Box key={index}>
            <Text size="xs"><Text span ff="monospace" c="dimmed">+{duration((event.time_unix_nano - new Date(span.start).valueOf() * 1e6) / 1e6)}</Text> {event.name}</Text>
            {event.name !== "exception" && event.attributes && Object.keys(event.attributes).length > 0 && <Box pl="md"><Attributes label="Event attributes" values={event.attributes} /></Box>}
          </Box>)}</Stack>
        </Box>}
        <Attributes label="Resource" values={extras.data.resource} />
        {extras.data.scope && <Text size="xs" c="dimmed">Instrumentation scope: {extras.data.scope}</Text>}
      </>}
    </Stack>
  </Paper>;
}
