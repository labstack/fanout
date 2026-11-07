import { LoadingSkeleton } from "../../loading-skeleton";
import { formatTimestamp } from "../../../../panels/units";
import { DataState, KeyValueList, Notice, StatusBadge, useToneColor } from "@labstack/ui";
import { Box, CloseButton, Code, Group, Paper, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useRef } from "react";
import type { TraceSpan } from "../../../../contracts";
import { duration } from "../../../../format";

/** A span event; offset_ns is its time after the span start. */
export type SpanEvent = { offset_ns: number; name: string; attributes: Record<string, unknown> };
/** What the trace read leaves out of each span, read on demand for the selected one. */
export type SpanExtras = { attributes: Record<string, unknown>; resource: Record<string, unknown>; events: SpanEvent[]; scope: string };

const statuses: Record<string, string> = { STATUS_CODE_ERROR: "Error", STATUS_CODE_OK: "OK", STATUS_CODE_UNSET: "Unset" };

/** An event attribute as text: strings verbatim, other values as JSON. */
const text = (value: unknown): string | undefined => value == null ? undefined : typeof value === "string" ? value : JSON.stringify(value);

/** Untyped attributes as description lists; nested values print as JSON. */
export function Attributes({ label, values }: { label: string; values: Record<string, unknown> }) {
  const keys = Object.keys(values).sort();
  return <Box>
    <Text size="xs" fw={600} c="dimmed" mb={4}>{label}</Text>
    {keys.length === 0 ? <Text size="xs" c="dimmed">None</Text>
      : <KeyValueList label={label} items={keys.map(key => ({ label: <Text span inherit ff="monospace">{key}</Text>, value: <Text span inherit ff="monospace" style={{ whiteSpace: "pre-wrap" }}>{typeof values[key] === "string" ? String(values[key]) : JSON.stringify(values[key])}</Text> }))} />}
  </Box>;
}

export type SpanLoader = (traceId: string, span: TraceSpan, signal?: AbortSignal) => Promise<SpanExtras>;

export function SpanDetails({ traceId, span, load, onClose }: { traceId: string; span: TraceSpan; load?: SpanLoader; onClose(): void }) {
  const box = useRef<HTMLDivElement>(null);
  useEffect(() => { box.current?.scrollIntoView?.({ block: "nearest" }); }, [span]);
  const extras = useQuery({ queryKey: ["span-extras", traceId, span.span_id, span.start], queryFn: ({ signal }) => load!(traceId, span, signal), enabled: Boolean(load), retry: false, staleTime: Infinity });
  const danger = useToneColor("danger");
  const failed = span.status === "STATUS_CODE_ERROR";
  const exceptions = extras.data?.events.filter(event => event.name === "exception") ?? [];
  return <Paper ref={box} withBorder p="sm" radius="md" data-span-details aria-label={`Span ${span.operation}`} role="region">
    <Stack gap="sm">
      <Group justify="space-between" wrap="nowrap" align="flex-start">
        <Box miw={0}>
          <Text fw={600} style={{ overflowWrap: "anywhere" }}>{span.operation}</Text>
          <Text size="xs" c="dimmed">{span.service} · {span.kind.replace(/^SPAN_KIND_/, "").toLowerCase()} · {duration(span.duration_ms)} · <Text span ff="monospace" size="xs">{formatTimestamp(Date.parse(span.start))}</Text></Text>
        </Box>
        <CloseButton aria-label="Close span details" onClick={onClose} />
      </Group>
      <Box><KeyValueList label="Span metadata" items={[
        { label: "Status", value: <StatusBadge tone={failed ? "danger" : "neutral"}>{statuses[span.status] ?? span.status}</StatusBadge> },
        { label: "Span", value: <Text span inherit ff="monospace">{span.span_id}</Text> },
        ...(span.parent_span_id ? [{ label: "Parent", value: <Text span inherit ff="monospace">{span.parent_span_id}</Text> }] : []),
      ]} /></Box>
      {span.status_message && <Text size="sm" c={failed ? danger : undefined} style={{ overflowWrap: "anywhere" }}>{span.status_message}</Text>}
      {extras.isLoading && <DataState state="loading" loadingLabel="Loading span details" skeleton={<LoadingSkeleton shape="key_value" rows={3} />} />}
      {extras.error && <Box p="xs"><DataState state="error" error={{ message: extras.error.message, onRetry: () => void extras.refetch() }} /></Box>}
      {exceptions.map((event, index) => {
        const message = text(event.attributes["exception.message"]), stack = text(event.attributes["exception.stacktrace"]);
        return <Box key={index} p="xs"><Notice tone="danger" title={text(event.attributes["exception.type"]) ?? "Exception"}>
          {message && <Text size="sm" style={{ overflowWrap: "anywhere" }}>{message}</Text>}
          {stack && <Code block mt={4} fz={12} style={{ maxHeight: 220, overflow: "auto" }}>{stack}</Code>}
        </Notice></Box>;
      })}
      {extras.data && <>
        <Attributes label="Attributes" values={extras.data.attributes} />
        {extras.data.events.length > 0 && <Box>
          <Text size="xs" fw={600} c="dimmed" mb={4}>Events</Text>
          <Stack gap={4}>{extras.data.events.map((event, index) => <Box key={index}>
            <Text size="xs"><Text span ff="monospace" c="dimmed">+{duration(event.offset_ns / 1e6)}</Text> {event.name}</Text>
            {event.name !== "exception" && Object.keys(event.attributes).length > 0 && <Box pl="md"><Attributes label="Event attributes" values={event.attributes} /></Box>}
          </Box>)}</Stack>
        </Box>}
        <Attributes label="Resource" values={extras.data.resource} />
        {extras.data.scope && <Box><KeyValueList items={[{ label: "Instrumentation scope", value: <Text span inherit ff="monospace">{extras.data.scope}</Text> }]} /></Box>}
      </>}
    </Stack>
  </Paper>;
}
