import { Badge, Box, Button, Group, Paper, ScrollArea, SimpleGrid, Stack, Table, Text, Tooltip } from "@mantine/core";
import { ListBullets, Path } from "@phosphor-icons/react";
import { StrictMode, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import { EmptyState, MetaFooter, Metric, PageControls, Tabs, ViewHeader, ViewShell, ViewStatus, seriesColor, usePagedItems } from "./components";
import type { LogEntry, Result, TraceDetail, TraceSpan } from "./contracts";
import { duration, integer, windowLabel } from "./format";
import { askAbout, useFanoutApp } from "./use-fanout-app";
import "./app.css";

type View = "waterfall" | "flame" | "logs";

function TraceApp() {
  const { app, callTool, error, host, result, toolError } = useFanoutApp<Result<TraceDetail>>("Fanout trace detail");
  const [view, setView] = useState<View>("waterfall");
  const dark = host?.theme === "dark";
  return <ViewShell dark={dark}>
    <ViewHeader eyebrow="Request journey" title={result?.data.trace_id ? `Trace ${shortID(result.data.trace_id)}` : "Trace analysis"} summary={result ? `${result.data.spans.length} spans across ${result.data.services.length} services` : undefined} onRefresh={() => callTool("trace_detail")} disabled={!app} />
    <ViewStatus error={toolError ?? (error ? "This view could not be loaded. Please try again." : null)} loading={!result && !error && !toolError ? "Finding a representative trace…" : undefined} />
    {result && result.data.spans.length === 0 && <><EmptyState tall icon={<Path size={20} weight="duotone" />} title="No traces in this window">Try a wider time window.</EmptyState><MetaFooter left={windowLabel(result.provenance.window)} right="No traces found" /></>}
    {result && result.data.spans.length > 0 && <>
      <SimpleGrid cols={{ base: 2, sm: 4 }} spacing="sm" px={{ base: "md", sm: "lg" }} pb="md"><Metric label="Duration" value={duration(result.data.duration_ms)} /><Metric label="Spans" value={integer.format(result.data.spans.length)} /><Metric label="Services" value={integer.format(result.data.services.length)} /><Metric label="Status" value={result.data.has_error ? "Error" : "OK"} color={result.data.has_error ? "bad" : "ok"} /></SimpleGrid>
      <Tabs active={view} onChange={setView} items={[{ id: "waterfall", label: "Waterfall", count: result.data.spans.length }, { id: "flame", label: "Flame graph" }, { id: "logs", label: "Correlated logs", count: result.data.logs.length }]} />
      {view === "waterfall" && <Waterfall spans={result.data.spans} dark={dark} onSpan={(span) => askAbout(app, `Investigate span ${span.span_id} (${span.service} ${span.operation}) in trace ${result.data.trace_id}.`)} />}
      {view === "flame" && <FlameGraph spans={result.data.spans} dark={dark} onSpan={(span) => askAbout(app, `Investigate span ${span.span_id} (${span.service} ${span.operation}) in trace ${result.data.trace_id}.`)} />}
      {view === "logs" && <TraceLogs entries={result.data.logs} />}
      <MetaFooter left={windowLabel(result.provenance.window)} right={`Trace ${shortID(result.data.trace_id)}`} />
    </>}
  </ViewShell>;
}

function Waterfall({ spans, dark, onSpan }: { spans: TraceSpan[]; dark: boolean; onSpan: (span: TraceSpan) => void }) {
  const start = Math.min(...spans.map((span) => new Date(span.start).valueOf()));
  const end = Math.max(...spans.map((span) => new Date(span.start).valueOf() + span.duration_ms));
  const total = Math.max(end - start, 1);
  const visibleSpans = usePagedItems(spans, 8);
  return <><Table.ScrollContainer minWidth={680}><Table highlightOnHover verticalSpacing="sm">
    <Table.Thead><Table.Tr><Table.Th w={230}>Operation</Table.Th><Table.Th>Timeline</Table.Th><Table.Th w={90}>Duration</Table.Th></Table.Tr></Table.Thead>
    <Table.Tbody>{visibleSpans.pageItems.map((span) => {
      const offset = (new Date(span.start).valueOf() - start) / total * 100;
      const width = Math.max(span.duration_ms / total * 100, .6);
      const failed = span.status.toUpperCase().includes("ERROR");
      return <Table.Tr key={span.span_id} tabIndex={0} onClick={() => onSpan(span)} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") onSpan(span); }} style={{ cursor: "pointer" }}>
        <Table.Td><Group gap="xs" wrap="nowrap"><Box w={8} h={8} bg={seriesColor(span.service, dark)} style={{ borderRadius: "50%", flex: "0 0 auto" }} /><Box miw={0}><Text fw={600} size="sm" truncate>{span.operation}</Text><Text c="dimmed" size="xs" truncate>{span.service}</Text></Box></Group></Table.Td>
        <Table.Td><Tooltip label={`${span.service} · ${span.operation} · ${duration(span.duration_ms)}`} withArrow><Box pos="relative" h={14} bg="var(--mantine-color-default-hover)" style={{ borderRadius: "var(--mantine-radius-sm)" }}><Box pos="absolute" left={`${offset}%`} w={`${Math.min(width, 100 - offset)}%`} h="100%" bg={failed ? "bad" : seriesColor(span.service, dark)} style={{ borderRadius: "var(--mantine-radius-sm)", minWidth: 3 }} /></Box></Tooltip></Table.Td>
        <Table.Td><Text size="sm" ff="monospace">{duration(span.duration_ms)}</Text></Table.Td>
      </Table.Tr>;
    })}</Table.Tbody>
  </Table></Table.ScrollContainer><PageControls {...visibleSpans} onChange={visibleSpans.setPage} /></>;
}

function FlameGraph({ spans, dark, onSpan }: { spans: TraceSpan[]; dark: boolean; onSpan: (span: TraceSpan) => void }) {
  const model = useMemo(() => flameModel(spans), [spans]);
  const services = [...new Set(spans.map((span) => span.service))];
  return <Stack px={{ base: "md", sm: "lg" }} pb="md" gap="xs">
    <Group justify="space-between"><Group gap="md">{services.map((service) => <Group gap={5} key={service}><Box w={8} h={8} bg={seriesColor(service, dark)} style={{ borderRadius: "50%" }} /><Text c="dimmed" size="xs">{service}</Text></Group>)}</Group><Badge variant="light">{duration(model.total)}</Badge></Group>
    <Paper withBorder radius="md" p="sm">
      <ScrollArea type="auto" offsetScrollbars>
        <Box miw={760}>
          <Box pos="relative" h={22} mb={4}>{[0, 25, 50, 75, 100].map((position) => <Text key={position} pos="absolute" left={`${position}%`} c="dimmed" size="xs" style={{ transform: position === 100 ? "translateX(-100%)" : position ? "translateX(-50%)" : undefined }}>{duration(model.total * position / 100)}</Text>)}</Box>
          <Box pos="relative" h={Math.max(150, model.laneCount * 36 + 12)} bg="var(--mantine-color-default-hover)" style={{ overflow: "hidden", borderRadius: "var(--mantine-radius-md)" }}>
            {[0, 25, 50, 75, 100].map((position) => <Box key={position} pos="absolute" left={`${position}%`} top={0} bottom={0} style={{ borderLeft: "1px solid var(--mantine-color-default-border)" }} />)}
            {model.frames.map(({ span, lane, left, width }) => {
              const failed = span.status.toUpperCase().includes("ERROR");
              const compact = width < 7;
              return <Tooltip key={span.span_id} label={`${span.service} · ${span.operation} · ${duration(span.duration_ms)}`} withArrow><Button variant="filled" color={failed ? "bad" : seriesColor(span.service, dark)} pos="absolute" left={`${left}%`} top={lane * 36 + 6} w={`${Math.max(width, .35)}%`} h={30} px={compact ? 2 : "xs"} size="compact-xs" onClick={() => onSpan(span)} style={{ overflow: "hidden", minWidth: 3 }}><Text component="span" size="xs" fw={700} truncate>{compact ? "" : span.operation}{width >= 12 ? ` · ${duration(span.duration_ms)}` : ""}</Text></Button></Tooltip>;
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

function TraceLogs({ entries }: { entries: LogEntry[] }) {
  const logs = usePagedItems(entries, 6);
  if (entries.length === 0) return <EmptyState tall icon={<ListBullets size={20} weight="duotone" />} title="No correlated logs">No logs in this window carry the selected trace ID.</EmptyState>;
  return <><Table.ScrollContainer minWidth={620}><Table striped verticalSpacing="xs"><Table.Thead><Table.Tr><Table.Th>Time</Table.Th><Table.Th>Level</Table.Th><Table.Th>Service</Table.Th><Table.Th>Message</Table.Th></Table.Tr></Table.Thead><Table.Tbody>{logs.pageItems.map((entry, index) => <Table.Tr key={`${entry.time}-${logs.from + index}`}><Table.Td><Text size="xs" ff="monospace">{new Date(entry.time).toLocaleTimeString([], { hour: "numeric", minute: "2-digit", second: "2-digit" })}</Text></Table.Td><Table.Td><Badge size="sm" color={severityColor(entry.severity)} variant="light">{entry.severity || "LOG"}</Badge></Table.Td><Table.Td><Text fw={600} size="sm">{entry.service}</Text></Table.Td><Table.Td><Text size="sm" lineClamp={2} title={entry.body}>{entry.body}</Text></Table.Td></Table.Tr>)}</Table.Tbody></Table></Table.ScrollContainer><PageControls {...logs} onChange={logs.setPage} /></>;
}

function shortID(value: string) { return value.length > 12 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value; }
function severityColor(value: string) { const severity = value.toUpperCase(); if (severity === "ERROR" || severity === "FATAL") return "bad"; if (severity === "WARN" || severity === "WARNING") return "warn"; if (severity === "INFO") return "info"; return "gray"; }

createRoot(document.getElementById("root")!).render(<StrictMode><TraceApp /></StrictMode>);
