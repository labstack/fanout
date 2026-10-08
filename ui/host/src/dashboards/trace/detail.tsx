import { Alert, Badge, Box, Button, Group, Paper, ScrollArea, Stack, Text } from "@mantine/core";
import { Tooltip } from "@mantine/core";
import { useMemo, useState } from "react";
import type { Result, TraceDetail, TraceSpan } from "../../../../contracts";
import { seriesSlot, statusHex } from "../../../../chart";
import { filledTextOn } from "../../../../theme";
import { duration } from "../../../../format";
import { EmptyState } from "./components";
import { ListBullets } from "@phosphor-icons/react";
import { Waterfall } from "./trace-components";
import { LogsViz } from "../viz/logs";
import type { PanelResult } from "../../../../panels/types";

export function TraceDetailView({ result, dark, onSpan }: { result: Result<TraceDetail>; dark: boolean; onSpan?: (span: TraceSpan) => void }) {
  const [view, setView] = useState("waterfall");
  const data = result.data;
  const logResult: PanelResult = useMemo(() => ({ id: "correlated_logs", status: data.logs.length ? "ok" : "empty", elapsed_ms: 0, frame: {
    columns: [{ name: "time", type: "time", role: "time" }, ...["severity", "service", "body"].map(name => ({ name, type: "string" as const, role: "dimension" as const }))],
    rows: data.logs.length, values: [data.logs.map(log => Date.parse(log.time)), data.logs.map(log => log.severity), data.logs.map(log => log.service), data.logs.map(log => log.body)],
  } }), [data.logs]);
  return <Stack gap="sm">
    <Group><Text fw={600}>{data.trace_id}</Text><Text c={data.has_error ? "bad" : "dimmed"}>{data.has_error ? "■ Error" : "● OK"}</Text></Group>
    {data.spans.length > 0 ? <>
      <Group role="group" aria-label="Trace view"><Button aria-pressed={view === "waterfall"} variant={view === "waterfall" ? "light" : "subtle"} onClick={() => setView("waterfall")}>Waterfall</Button><Button aria-pressed={view === "flame"} variant={view === "flame" ? "light" : "subtle"} onClick={() => setView("flame")}>Flame graph</Button></Group>
      {view === "waterfall" ? <Waterfall spans={data.spans} dark={dark} onSpan={onSpan} /> : <FlameGraph spans={data.spans} dark={dark} onSpan={onSpan} />}
    </> : <Text c="dimmed">No spans were found for this trace.</Text>}
    <Text fw={600}>Correlated logs</Text>{data.logs.length ? <Box style={{ overflow: "auto", minWidth: 0 }}><LogsViz foldConstants={false} panel={{ id: "correlated_logs", title: "Correlated logs", viz: "logs" }} result={logResult} dark={dark} height={320} /></Box> : <EmptyState tall icon={<ListBullets size={20} weight="duotone" />} title="No correlated logs">No logs in this window carry the selected trace ID.</EmptyState>}
    {data.truncated && <Alert color="warn">This trace is truncated: {data.spans.length} of {data.span_count} spans, {data.services.length} of {data.service_count} services.</Alert>}
  </Stack>;
}

function FlameGraph({ spans, dark, onSpan }: { spans: TraceSpan[]; dark: boolean; onSpan?: (span: TraceSpan) => void }) {
  const model = useMemo(() => flameModel(spans), [spans]);
  const services = [...new Set(spans.map((span) => span.service))];
  return <Stack px={{ base: "md", sm: "lg" }} pb="md" gap="xs">
    <Group justify="space-between"><Group gap="md">{services.map((service) => <Group gap={5} key={service}><Box w={8} h={8} bg={seriesSlot(services.indexOf(service), dark)} style={{ borderRadius: "50%" }} /><Text c="dimmed" size="xs">{service}</Text></Group>)}</Group><Badge variant="light">{duration(model.total)}</Badge></Group>
    <Paper withBorder radius="md" p="sm">
      <ScrollArea type="auto" offsetScrollbars>
        <Box miw={760}>
          <Box pos="relative" h={22} mb={4}>{[0, 25, 50, 75, 100].map((position) => <Text key={position} pos="absolute" left={`${position}%`} c="dimmed" size="xs" style={{ transform: position === 100 ? "translateX(-100%)" : position ? "translateX(-50%)" : undefined }}>{duration(model.total * position / 100)}</Text>)}</Box>
          <Box pos="relative" h={Math.max(150, model.laneCount * 36 + 12)} bg="var(--mantine-color-default-hover)" style={{ overflow: "hidden", borderRadius: "var(--mantine-radius-md)" }}>
            {[0, 25, 50, 75, 100].map((position) => <Box key={position} pos="absolute" left={`${position}%`} top={0} bottom={0} style={{ borderLeft: "1px solid var(--mantine-color-default-border)" }} />)}
            {model.frames.map(({ span, lane, left, width }) => {
              const failed = span.status.toUpperCase().includes("ERROR");
              const compact = width < 7;
              const color = failed ? statusHex(dark).bad : seriesSlot(services.indexOf(span.service), dark);
              const label = `${span.operation} · ${span.service} · ${duration(span.duration_ms)}`;
              const content = <Text component="span" size="xs" fw={700} truncate>{compact ? "" : span.operation}{width >= 12 ? ` · ${duration(span.duration_ms)}` : ""}</Text>;
              const geometry = { position: "absolute" as const, left: `${left}%`, top: lane * 36 + 6, width: `${Math.max(width, .35)}%`, height: 30, overflow: "hidden", minWidth: 3 };
              return <Tooltip key={span.span_id} label={`${span.service} · ${span.operation} · ${duration(span.duration_ms)}`} withArrow events={{ hover: true, focus: true, touch: false }}>
                {onSpan ? <Button variant="filled" color={color} aria-label={label} px={compact ? 2 : "xs"} size="compact-xs" onClick={() => onSpan(span)} style={geometry}>{content}</Button>
                  : <Box role="img" tabIndex={0} aria-label={label} style={{ ...geometry, display: "flex", alignItems: "center", paddingInline: compact ? 2 : 8, borderRadius: "var(--mantine-radius-sm)", background: color, color: filledTextOn(color) }}>{content}</Box>}
              </Tooltip>;
            })}
          </Box>
        </Box>
      </ScrollArea>
    </Paper>
    <Text c="dimmed" size="xs">Width represents wall-clock duration; lanes preserve span hierarchy without overlap.</Text>
  </Stack>;
}

function flameModel(spans: TraceSpan[]) {
  const start = Math.min(...spans.map((span) => new Date(span.start).valueOf()));
  const end = Math.max(...spans.map((span) => new Date(span.start).valueOf() + span.duration_ms));
  const total = Math.max(end - start, 1);
  const byID = new Map(spans.map((span) => [span.span_id, span]));
  const depth = (span: TraceSpan, seen = new Set<string>()): number => { if (!span.parent_span_id || seen.has(span.span_id)) return 0; const parent = byID.get(span.parent_span_id); if (!parent) return 0; seen.add(span.span_id); return 1 + depth(parent, seen); };
  const raw = spans.map((span) => { const spanStart = new Date(span.start).valueOf(); return { span, depth: depth(span), start: spanStart, end: spanStart + span.duration_ms, left: (spanStart - start) / total * 100, width: span.duration_ms / total * 100 }; }).sort((a, b) => a.depth - b.depth || a.start - b.start || b.span.duration_ms - a.span.duration_ms);
  const frames: Array<(typeof raw)[number] & { lane: number }> = [];
  let laneOffset = 0;
  for (const currentDepth of [...new Set(raw.map((frame) => frame.depth))].sort((a, b) => a - b)) {
    const laneEnds: number[] = [];
    for (const frame of raw.filter((item) => item.depth === currentDepth)) { let localLane = laneEnds.findIndex((laneEnd) => laneEnd <= frame.start); if (localLane === -1) localLane = laneEnds.length; laneEnds[localLane] = frame.end; frames.push({ ...frame, lane: laneOffset + localLane }); }
    laneOffset += Math.max(laneEnds.length, 1);
  }
  return { frames, laneCount: laneOffset, total };
}

