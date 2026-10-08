import { Box, Button, Group, NativeSelect, Stack, Text, TextInput, VisuallyHidden } from "@mantine/core";
import { useEffect, useMemo, useRef, useState } from "react";
import type { KeyboardPoint, PointEvent } from "../../../panels/keyboard";

/** One virtual focused point, independent of the number of displayed data. */
export function ChartKeyboard({ points, label, summary, onClick, onHighlight, onZoom, bounds, pointWindow }: {
  points: readonly KeyboardPoint[]; label: string; summary(point: KeyboardPoint): string;
  onClick?(event: PointEvent): void; onHighlight(point?: KeyboardPoint): void;
  onZoom?(from: number, to: number): void; bounds?: {from: number; to: number};
  pointWindow?(point: KeyboardPoint): {from: number; to: number} | undefined;
}) {
  const [opened, setOpened] = useState(false);
  const [index, setIndex] = useState(0);
  const [from, setFrom] = useState("");
  const [to, setTo] = useState("");
  const [error, setError] = useState<string>();
  const anchor = useRef<KeyboardPoint | undefined>(undefined);
  const pointRef = useRef<HTMLDivElement>(null);
  const explore = useRef<HTMLButtonElement>(null);
  const currentIndex = Math.max(0, Math.min(index, points.length - 1));
  const current = points[currentIndex];
  const highlight = useRef(onHighlight); highlight.current = onHighlight;
  const rangeAvailable = Boolean(onZoom && bounds && Number.isFinite(bounds.from) && Number.isFinite(bounds.to) && bounds.from < bounds.to);
  useEffect(() => {
    setIndex(currentIndex);
    if (opened) highlight.current(current);
  }, [points, currentIndex, current, opened]);
  useEffect(() => { anchor.current = undefined; }, [points]);
  useEffect(() => () => highlight.current(undefined), []);
  useEffect(() => {
    if (opened) pointRef.current?.focus();
    else highlight.current(undefined);
  }, [opened]);
  useEffect(() => {
    setFrom(bounds ? new Date(bounds.from).toISOString() : "");
    setTo(bounds ? new Date(bounds.to).toISOString() : "");
    setError(undefined); anchor.current = undefined;
  }, [bounds?.from, bounds?.to]);
  const choose = (next: number, extend = false) => {
    const bounded = Math.max(0, Math.min(next, points.length - 1));
    if (extend && rangeAvailable && current) {
      anchor.current ??= current;
      const a = pointWindow?.(anchor.current), b = points[bounded] && pointWindow?.(points[bounded]);
      if (a && b) {
        setFrom(new Date(Math.max(bounds!.from, Math.min(a.from, b.from))).toISOString());
        setTo(new Date(Math.min(bounds!.to, Math.max(a.to, b.to))).toISOString());
        setError(undefined);
      }
    } else anchor.current = undefined;
    setIndex(bounded);
  };
  const series = useMemo(() => [...new Map(points.map(point => [point.series_index, point.event.seriesName ?? "Series"])).entries()], [points]);
  const seriesIndex = current?.series_index;
  const peers = useMemo(() => points.filter(point => point.series_index === seriesIndex), [points, seriesIndex]);
  const position = peers.indexOf(current);
  const moveSeries = (delta: number, extend = false) => {
    if (!current) return;
    const next = series[Math.max(0, Math.min(series.length - 1, series.findIndex(([id]) => id === current.series_index) + delta))];
    const candidates = points.filter(point => point.series_index === next[0]);
    choose(points.indexOf(candidates[Math.min(position, candidates.length - 1)]), extend);
  };
  const text = current ? summary(current) : "No selectable points.";
  return <Box style={{ flexShrink: 0, maxHeight: "55%", overflow: "auto" }}>
    <Button ref={explore} size="compact-xs" variant="subtle" aria-label={`${opened ? "Close exploration" : "Explore chart"}: ${label}`} aria-expanded={opened}
      disabled={!points.length && !rangeAvailable} onClick={() => { if (opened) explore.current?.focus(); setOpened(!opened); }}>
      {opened ? "Close exploration" : "Explore chart"}
    </Button>
    {opened && <Stack gap={6} p={4}>
      <Text size="xs" c="dimmed">Arrows move between points and series. Enter selects. Shift+Left/Right extends a time range.</Text>
      <Box ref={pointRef} data-chart-point role="group" aria-label={text} tabIndex={0} className="chart-keyboard-point"
        onFocus={() => highlight.current(current)} onBlur={() => highlight.current(undefined)}
        onKeyDown={event => {
          if (event.defaultPrevented || event.repeat || event.ctrlKey || event.metaKey || event.altKey) return;
          if (event.key === "Enter" && !event.shiftKey && current && onClick) { event.preventDefault(); onClick(current.event); }
          else if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
            event.preventDefault();
            const next = peers[Math.max(0, Math.min(peers.length - 1, position + (event.key === "ArrowLeft" ? -1 : 1)))];
            if (next) choose(points.indexOf(next), event.shiftKey);
          } else if (event.key === "ArrowUp" || event.key === "ArrowDown") {
            event.preventDefault(); moveSeries(event.key === "ArrowUp" ? -1 : 1, event.shiftKey);
          }
        }}><Text size="xs">{text}</Text></Box>
      <VisuallyHidden aria-live="polite" aria-atomic="true">{text}</VisuallyHidden>
      {current && <Group gap={6} align="end">
        <NativeSelect size="xs" label="Series" value={current.series_index} data={series.map(([id, name]) => ({value: String(id), label: name}))}
          onChange={event => choose(points.findIndex(point => point.series_index === Number(event.currentTarget.value)))} />
        <TextInput size="xs" label="Point" type="number" min={1} max={peers.length} value={position + 1} w={80}
          onChange={event => { const n = Number(event.currentTarget.value); if (Number.isInteger(n) && n >= 1 && n <= peers.length) choose(points.indexOf(peers[n - 1])); }} />
        <Button size="compact-xs" variant="default" disabled={position <= 0} onClick={() => choose(points.indexOf(peers[position - 1]))}>Previous point</Button>
        <Button size="compact-xs" variant="default" disabled={position >= peers.length - 1} onClick={() => choose(points.indexOf(peers[position + 1]))}>Next point</Button>
        {onClick && <Button size="compact-xs" variant="default" onClick={() => onClick(current.event)}>Select point</Button>}
      </Group>}
      {rangeAvailable && <>
        <Text size="xs" c="dimmed">Observed window (UTC): {new Date(bounds!.from).toISOString()} – {new Date(bounds!.to).toISOString()}</Text>
        <Group gap={6} align="end">
          <TextInput size="xs" label="Range start (UTC)" value={from} onChange={e => setFrom(e.currentTarget.value)} />
          <TextInput size="xs" label="Range end (UTC)" value={to} onChange={e => setTo(e.currentTarget.value)} />
          <Button size="compact-xs" onClick={() => {
            const start = /(?:Z|[+-]\d{2}:\d{2})$/.test(from) ? Date.parse(from) : NaN;
            const end = /(?:Z|[+-]\d{2}:\d{2})$/.test(to) ? Date.parse(to) : NaN;
            if (!Number.isFinite(start) || !Number.isFinite(end) || start >= end || start < bounds!.from || end > bounds!.to) {
              setError("Enter UTC times within the observed window, with start before end."); return;
            }
            setError(undefined); onZoom!(start, end);
          }}>Zoom to range</Button>
        </Group>
        {error && <Text size="xs" c="bad" role="alert">{error}</Text>}
      </>}
    </Stack>}
  </Box>;
}
