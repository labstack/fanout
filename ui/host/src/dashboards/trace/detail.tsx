import { DataState, Notice, StatusBadge } from "@labstack/ui";
import { Badge, Box, Button, Group, Paper, Stack, Tabs, Text, VisuallyHidden } from "@mantine/core";
import { Tooltip } from "@mantine/core";
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from "react";
import type { Result, TraceDetail, TraceSpan } from "../../../../contracts";
import { chartTheme, seriesSlot, statusHex } from "../../../../chart";
import { series } from "../../../../theme";
import { duration } from "../../../../format";
import { EmptyState } from "./components";
import { ListBullets } from "@phosphor-icons/react";
import { Waterfall } from "./waterfall";
import { SpanDetails, type SpanLoader } from "./span-details";
import { LogsViz } from "../viz/logs";
import type { PanelResult } from "../../../../panels/types";

/** Spans open their details below the chart unless the caller handles span clicks itself. */
export function TraceDetailView({ result, dark, onSpan, loadSpan }: { result: Result<TraceDetail>; dark: boolean; onSpan?: (span: TraceSpan) => void; loadSpan?: SpanLoader }) {
  const [view, setView] = useState("waterfall");
  const [picked, setPicked] = useState<TraceSpan>();
  const select = onSpan ?? setPicked;
  const data = result.data;
  const logResult: PanelResult = useMemo(() => ({ id: "correlated_logs", status: data.logs.length ? "ok" : "empty", elapsed_ms: 0, frame: {
    columns: [{ name: "time", type: "time", role: "time" }, ...["severity", "service", "body"].map(name => ({ name, type: "string" as const, role: "dimension" as const }))],
    rows: data.logs.length, values: [data.logs.map(log => Date.parse(log.time)), data.logs.map(log => log.severity), data.logs.map(log => log.service), data.logs.map(log => log.body)],
  } }), [data.logs]);
  const loadedMs = useMemo(() => spanBounds(data.spans).total, [data.spans]);
  return <Stack gap="sm">
    <Group><Text fw={600} ff="monospace" size="sm">{data.trace_id}</Text><StatusBadge tone={data.has_error ? "danger" : "success"}>{data.has_error ? "Error" : "OK"}</StatusBadge></Group>
    {data.spans.length > 0 ? <>
      <Tabs value={view} onChange={value => value && setView(value)} aria-label="Trace view"><Tabs.List><Tabs.Tab value="waterfall" aria-pressed={view === "waterfall"}>Waterfall</Tabs.Tab><Tabs.Tab value="flame" aria-pressed={view === "flame"}>Flame graph</Tabs.Tab></Tabs.List></Tabs>
      {/* Beside the chart it qualifies: a truncated trace draws only the spans that loaded. */}
      {data.truncated && <Box data-trace-truncated><Notice tone="warning">This trace is truncated: {data.spans.length} of {data.span_count} spans, {data.services.length} of {data.service_count} services.{loadedMs < data.duration_ms ? ` The timeline shows the loaded spans: ${duration(loadedMs)} of the trace's ${duration(data.duration_ms)}.` : ""}</Notice></Box>}
      {view === "waterfall" ? <Waterfall spans={data.spans} dark={dark} onSpan={select} selected={picked?.span_id} /> : <FlameGraph spans={data.spans} dark={dark} onSpan={select} traceMs={data.truncated ? data.duration_ms : undefined} />}
      {picked && <SpanDetails key={picked.span_id} traceId={data.trace_id} span={picked} load={loadSpan} onClose={() => setPicked(undefined)} />}
    </> : <Text c="dimmed">No spans were found for this trace.</Text>}
    <Text fw={600}>Correlated logs</Text>{data.logs.length ? <Box style={{ overflow: "auto", minWidth: 0 }}><LogsViz foldConstants={false} panel={{ id: "correlated_logs", title: "Correlated logs", viz: "logs" }} result={logResult} dark={dark} height={320} /></Box> : <EmptyState tall icon={<ListBullets size={20} weight="duotone" />} title="No correlated logs">No logs in this window carry the selected trace ID.</EmptyState>}
  </Stack>;
}

/** Five ticks where their labels fit, fewer on a narrow axis. Centred labels
 *  reach half a label past their tick and the end labels a whole one inward,
 *  so neighbouring ticks need a label and a half plus a gap between them. */
function tickPositions(width: number, total: number): number[] {
  if (!width) return [0, 25, 50, 75, 100];
  const label = Math.max(...[25, 50, 75, 100].map(position => Array.from(duration(total * position / 100)).length)) * 7.2;
  return [4, 2].map(parts => Array.from({ length: parts + 1 }, (_, i) => i * 100 / parts)).find(ticks => width / (ticks.length - 1) >= label * 1.5 + 12) ?? [0, 100];
}

function FlameGraph({ spans, dark, onSpan, traceMs }: { spans: TraceSpan[]; dark: boolean; onSpan: (span: TraceSpan) => void; traceMs?: number }) {
  const model = useMemo(() => flameModel(spans), [spans]);
  const services = [...new Set(spans.map((span) => span.service))];
  // Services past the fixed palette share the muted colour, so the legend names them as one entry.
  const named = services.slice(0, series.light.length), others = services.slice(series.light.length);
  const axis = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(0);
  useLayoutEffect(() => {
    if (!axis.current || typeof ResizeObserver === "undefined") return;
    const observer = new ResizeObserver(([entry]) => { if (entry) setWidth(Math.round(entry.contentRect.width)); });
    observer.observe(axis.current);
    return () => observer.disconnect();
  }, []);
  const ticks = tickPositions(width, model.total);
  return <Stack px={{ base: "md", sm: "lg" }} pb="md" gap="xs">
    <Group justify="space-between"><Group gap="md" data-flame-legend>{named.map((service, index) => <Group gap={5} key={service}><Box w={8} h={8} bg={seriesSlot(index, dark)} style={{ borderRadius: "50%" }} /><Text c="dimmed" size="xs">{service}</Text></Group>)}
      {others.length === 1 && <Group gap={5}><Box w={8} h={8} bg={chartTheme(dark).muted} style={{ borderRadius: "50%" }} /><Text c="dimmed" size="xs">{others[0]}</Text></Group>}
      {others.length > 1 && <OtherServices names={others} color={chartTheme(dark).muted} />}
    </Group>{/* A truncated trace draws only its loaded spans; the badge says how much of the trace that is. */}
    <Badge variant="light" tt="none" data-flame-duration>{traceMs !== undefined && model.total < traceMs ? `${duration(model.total)} of ${duration(traceMs)} shown` : duration(model.total)}</Badge></Group>
    <Paper withBorder radius="md" p="sm" data-flame-graph>
      <Box ref={axis} pos="relative" h={22} mb={4}>{ticks.map((position) => <Text key={position} pos="absolute" left={`${position}%`} c="dimmed" size="xs" style={{ whiteSpace: "nowrap", transform: position === 100 ? "translateX(-100%)" : position ? "translateX(-50%)" : undefined }}>{duration(model.total * position / 100)}</Text>)}</Box>
      <Box pos="relative" h={Math.max(150, model.laneCount * 36 + 12)} bg="var(--mantine-color-default-hover)" style={{ overflow: "hidden", borderRadius: "var(--mantine-radius-md)" }}>
        {ticks.map((position) => <Box key={position} pos="absolute" left={`${position}%`} top={0} bottom={0} style={{ borderLeft: "1px solid var(--mantine-color-default-border)" }} />)}
        {model.frames.map(({ span, lane, left, width }) => {
          const failed = span.status === "STATUS_CODE_ERROR";
          const compact = width < 7;
          const color = failed ? statusHex(dark).bad : seriesSlot(services.indexOf(span.service), dark);
          const label = `${span.operation} · ${duration(span.duration_ms)} · ${span.service}`;
          const content = <Text component="span" size="xs" fw={700} truncate>{compact ? "" : span.operation}{width >= 12 ? ` · ${duration(span.duration_ms)}` : ""}</Text>;
          const geometry = { position: "absolute" as const, left: `${left}%`, top: lane * 36 + 6, width: `${Math.max(width, .35)}%`, height: 30, overflow: "hidden", minWidth: 3 };
          return <Tooltip key={span.span_id} label={`${span.service} · ${span.operation} · ${duration(span.duration_ms)}`} withArrow events={{ hover: true, focus: true, touch: false }}>
            <Button variant="filled" color={color} aria-label={label} px={compact ? 2 : "xs"} size="compact-xs" onClick={() => onSpan(span)} style={geometry}>{content}</Button>
          </Tooltip>;
        })}
      </Box>
    </Paper>
    <Text c="dimmed" size="xs">Width represents wall-clock duration; lanes preserve span hierarchy without overlap.</Text>
  </Stack>;
}

// Names shown on hover, focus or touch close on Escape before the drawer does
// (WCAG 1.4.13), wherever focus is. Mantine's drawer listens on the window in
// the capture phase; a listener added when this module loads, before any
// drawer mounts, runs first and keeps the Escape from it.
const dismissers = new Set<() => void>();
if (typeof window !== "undefined") window.addEventListener("keydown", event => {
  if (event.key !== "Escape" || !dismissers.size) return;
  event.stopImmediatePropagation();
  for (const dismiss of [...dismissers]) dismiss();
}, { capture: true });

/** The names are read with the entry, and shown on hover, focus or touch. */
function OtherServices({ names, color }: { names: string[]; color: string }) {
  const [open, setOpen] = useState(false);
  useEffect(() => {
    if (!open) return;
    const dismiss = () => setOpen(false);
    dismissers.add(dismiss);
    return () => { dismissers.delete(dismiss); };
  }, [open]);
  return <Tooltip label={names.join(", ")} multiline maw={360} withArrow opened={open}>
    <Group gap={5} tabIndex={0} data-flame-others
      onMouseEnter={() => setOpen(true)} onMouseLeave={() => setOpen(false)} onFocus={() => setOpen(true)} onBlur={() => setOpen(false)} onTouchStart={() => setOpen(true)}>
      <Box w={8} h={8} bg={color} style={{ borderRadius: "50%" }} /><Text c="dimmed" size="xs">{names.length} other services<VisuallyHidden>: {names.join(", ")}</VisuallyHidden></Text>
    </Group>
  </Tooltip>;
}

/** Where the loaded spans start and how long they run, end to end. */
function spanBounds(spans: TraceSpan[]) {
  const start = Math.min(...spans.map((span) => new Date(span.start).valueOf()));
  const end = Math.max(...spans.map((span) => new Date(span.start).valueOf() + span.duration_ms));
  return { start, total: Math.max(end - start, 1) };
}

function flameModel(spans: TraceSpan[]) {
  const { start, total } = spanBounds(spans);
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

