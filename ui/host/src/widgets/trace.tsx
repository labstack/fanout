import { ActionIcon, Box, Group, SimpleGrid, Stack, Text, Tooltip } from "@mantine/core";
import { Check, Copy } from "@phosphor-icons/react";
import type { TraceDetail } from "../../../contracts";
import { seriesColor } from "../../../chart";
import { duration, integer } from "../../../format";
import { useCopy } from "../copy";
import { useObservability, widgetParams } from "./data";
import { Empty, Metric, WidgetError } from "./pieces";
import type { WidgetBodyProps } from "./widget-card";

function shortID(value: string) { return value.length > 12 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value; }

export default function TraceWidget({ widget, filters, dark }: WidgetBodyProps) {
  const trace = useObservability<TraceDetail>("trace", widgetParams(filters, widget.config, ["trace_id"]));
  if (trace.isError) return <WidgetError retry={() => void trace.refetch()} />;
  const data = trace.data?.data;
  if (data && data.spans.length === 0) return <Empty text="No traces in this window" />;
  const spans = [...(data?.spans ?? [])].sort((left, right) => right.duration_ms - left.duration_ms).slice(0, 6);
  return <Stack gap="sm">
    <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="sm">
      <Metric label="Duration" value={data ? duration(data.duration_ms) : "—"} />
      <Metric label="Spans" value={data ? integer.format(data.spans.length) : "—"} />
      <Metric label="Services" value={data ? integer.format(data.services.length) : "—"} />
      <Metric label="Status" value={data ? (data.has_error ? "Error" : "OK") : "—"} color={data ? (data.has_error ? "bad" : "ok") : undefined} />
    </SimpleGrid>
    {spans.length > 0 && <SpanBars spans={spans} dark={dark} />}
    {data && <Group gap={4}>
      <Text c="dimmed" size="xs" ff="monospace">Trace {shortID(data.trace_id)}</Text>
      <CopyTraceID value={data.trace_id} />
    </Group>}
  </Stack>;
}

/* The window the bars are drawn against comes from the spans themselves, so
   it is computed here rather than beside the summary tiles: Math.min of an
   empty list is Infinity, and a trace still loading has no spans yet. */
function SpanBars({ spans, dark }: { spans: TraceDetail["spans"]; dark: boolean }) {
  const start = Math.min(...spans.map((span) => new Date(span.start).valueOf()));
  const end = Math.max(...spans.map((span) => new Date(span.start).valueOf() + span.duration_ms));
  const total = Math.max(end - start, 1);
  return <Stack gap={4}>
    {spans.map((span) => {
      const offset = (new Date(span.start).valueOf() - start) / total * 100;
      const width = Math.max(span.duration_ms / total * 100, 0.6);
      const failed = span.status.toUpperCase().includes("ERROR");
      return <Group key={span.span_id} gap="sm" wrap="nowrap">
        <Box w={170} miw={0}><Text size="xs" fw={600} truncate>{span.operation}</Text><Text c="dimmed" size="xs" truncate>{span.service}</Text></Box>
        <Tooltip label={`${span.service} · ${span.operation} · ${duration(span.duration_ms)}`} withArrow>
          <Box pos="relative" h={10} flex={1} bg="var(--mantine-color-default-hover)" style={{ borderRadius: "var(--mantine-radius-sm)" }}>
            <Box pos="absolute" left={`${offset}%`} w={`${Math.min(width, 100 - offset)}%`} h="100%" bg={failed ? "bad" : seriesColor(span.service, dark)} style={{ borderRadius: "var(--mantine-radius-sm)", minWidth: 3 }} />
          </Box>
        </Tooltip>
        <Text size="xs" ff="monospace" w={56} ta="right">{duration(span.duration_ms)}</Text>
      </Group>;
    })}
  </Stack>;
}

function CopyTraceID({ value }: { value: string }) {
  const { state, copy } = useCopy();
  return <Tooltip label={state === "failed" ? "Could not copy" : state === "copied" ? "Copied" : "Copy trace id"}>
    <ActionIcon variant="subtle" color="gray" size="xs" aria-label="Copy trace id" data-state={state === "idle" ? undefined : state} onClick={() => copy(value)}>
      {state === "copied" ? <Check size={12} weight="bold" /> : <Copy size={12} />}
    </ActionIcon>
  </Tooltip>;
}
