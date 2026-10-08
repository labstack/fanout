import { Box, Button, Group, Text, TextInput, VisuallyHidden } from "@mantine/core";
import { useEffect, useId, useMemo, useRef, useState, type ReactNode } from "react";
import type { KeyboardPoint, PointEvent } from "../../../panels/keyboard";

// Identity excludes values, rendered indices and object identity: updates may
// change all three while the same time bucket/category is still displayed.
function pointIdentity(point: KeyboardPoint): string {
  const selection = (point.event.data as {selection?: {time?: number; from?: string; dimensions?: unknown; bucket?: unknown}} | undefined)?.selection;
  const value = point.event.value;
  return JSON.stringify([point.event.seriesName, point.event.name, selection?.from ?? selection?.time ?? (Array.isArray(value) ? value[0] : undefined), selection?.dimensions, selection?.bucket]);
}
type Range = {anchor: string; from: string; to: string; edited?: boolean};

/** A single plot tab stop; controls occupy space only for a pending range. */
export function ChartKeyboard({ points, label, summary, children, onClick, canSelect, onHighlight, onActiveChange, onRangePending, onZoom, bounds, pointWindow }: {
  points: readonly KeyboardPoint[]; label: string; summary(point: KeyboardPoint): string; children?: ReactNode;
  onClick?(event: PointEvent): void; canSelect?(event: PointEvent): boolean; onHighlight(point?: KeyboardPoint): void;
  onActiveChange?(active: boolean): void; onRangePending?(pending: boolean): void;
  onZoom?(from: number, to: number): void; bounds?: {from: number; to: number};
  pointWindow?(point: KeyboardPoint): {from: number; to: number} | undefined;
}) {
  const [focused, setFocused] = useState(false);
  const [selectedKey, setSelectedKey] = useState<string>();
  const [pending, setPending] = useState<Range | null>(null);
  const [error, setError] = useState<string>();
  const plot = useRef<HTMLDivElement>(null);
  const hintId = useId();
  const callbacks = useRef({onHighlight, onActiveChange, onRangePending});
  callbacks.current = {onHighlight, onActiveChange, onRangePending};
  const identities = useMemo(() => new Map(points.map(point => [pointIdentity(point), point])), [points]);
  const current = selectedKey ? identities.get(selectedKey) ?? points[0] : points[0];
  const rangeAvailable = Boolean(onZoom && bounds && Number.isFinite(bounds.from) && Number.isFinite(bounds.to) && bounds.from < bounds.to);
  const series = useMemo(() => [...new Set(points.map(point => point.event.seriesName))], [points]);
  const peers = useMemo(() => points.filter(point => point.event.seriesName === current?.event.seriesName), [points, current?.event.seriesName]);
  const position = current ? peers.indexOf(current) : -1;
  useEffect(() => {
    if (current && selectedKey !== pointIdentity(current)) setSelectedKey(pointIdentity(current));
    if (focused) callbacks.current.onHighlight(current);
  }, [current, selectedKey, focused]);
  useEffect(() => {
    if (!pending) return;
    const from = Date.parse(pending.from), to = Date.parse(pending.to);
    if (points.length && !identities.has(pending.anchor) || !pending.edited && bounds && (Number.isFinite(from) && from < bounds.from || Number.isFinite(to) && to > bounds.to)) {
      setPending(null); setError(undefined);
    }
  }, [identities, points.length, bounds?.from, bounds?.to, pending]);
  useEffect(() => { callbacks.current.onRangePending?.(Boolean(pending)); }, [Boolean(pending)]);
  useEffect(() => () => { callbacks.current.onHighlight(undefined); callbacks.current.onRangePending?.(false); }, []);
  const choose = (next: KeyboardPoint | undefined, extend = false) => {
    if (!next) return;
    if (extend && rangeAvailable && current) {
      const anchor = pending?.anchor ?? pointIdentity(current);
      const first = identities.get(anchor);
      const a = first && pointWindow?.(first), b = pointWindow?.(next);
      if (a && b) {
        setPending({anchor, from: new Date(Math.max(bounds!.from, Math.min(a.from, b.from))).toISOString(), to: new Date(Math.min(bounds!.to, Math.max(a.to, b.to))).toISOString()});
        setError(undefined);
      }
    }
    setSelectedKey(pointIdentity(next));
  };
  const location = (point: KeyboardPoint) => pointWindow?.(point)?.from ?? point.event.name ?? (Array.isArray(point.event.value) ? point.event.value[0] : undefined);
  const moveSeries = (delta: number) => {
    if (!current) return;
    const name = series[Math.max(0, Math.min(series.length - 1, series.indexOf(current.event.seriesName) + delta))];
    const candidates = points.filter(point => point.event.seriesName === name);
    const at = location(current);
    const exact = candidates.find(point => location(point) === at);
    const nearest = typeof at === "number" ? candidates.reduce<KeyboardPoint | undefined>((best, point) => {
      const next = location(point), old = best && location(best);
      return typeof next === "number" && (!best || typeof old !== "number" || Math.abs(next - at) < Math.abs(old - at)) ? point : best;
    }, undefined) : undefined;
    choose(exact ?? nearest ?? candidates[0]);
  };
  const cancel = () => { setPending(null); setError(undefined); plot.current?.focus(); };
  const hint = rangeAvailable ? "←→ points · ↑↓ series · Enter drill · Shift+←→ range" : "←→ points · ↑↓ series · Enter drill";
  return <Box ref={plot} data-chart-plot tabIndex={0} role="group" aria-label={label} aria-describedby={focused ? hintId : undefined}
    data-mantine-stop-propagation={pending ? "true" : undefined} className="chart-keyboard-point"
    style={{position: "relative", display: "flex", flexDirection: "column", flex: "1 1 auto", minHeight: 0, minWidth: 0}}
    onFocus={event => { callbacks.current.onActiveChange?.(true); setFocused(event.target === event.currentTarget); }}
    onBlur={event => {
      setFocused(false);
      if (!event.currentTarget.contains(event.relatedTarget as Node | null)) { callbacks.current.onHighlight(undefined); callbacks.current.onActiveChange?.(false); }
    }}
    onKeyDown={event => {
      if (event.defaultPrevented || event.ctrlKey || event.metaKey || event.altKey || event.nativeEvent.isComposing) return;
      if (event.key === "Escape" && !event.repeat) {
        if (pending) { event.preventDefault(); event.stopPropagation(); cancel(); }
        else if (event.target === event.currentTarget && !event.currentTarget.closest("[data-panel-fullscreen],[data-fragment-fullscreen]")) {
          event.preventDefault(); event.stopPropagation(); event.currentTarget.blur();
        }
        return;
      }
      if (event.target !== event.currentTarget || event.repeat && !["ArrowLeft", "ArrowRight", "ArrowUp", "ArrowDown"].includes(event.key)) return;
      if (event.key === "Enter" && !event.shiftKey && current && onClick && (!canSelect || canSelect(current.event))) { event.preventDefault(); onClick(current.event); }
      else if (event.key === "ArrowLeft" || event.key === "ArrowRight") {
        event.preventDefault(); choose(peers[Math.max(0, Math.min(peers.length - 1, position + (event.key === "ArrowLeft" ? -1 : 1)))], event.shiftKey);
      } else if (!event.shiftKey && (event.key === "ArrowUp" || event.key === "ArrowDown")) {
        event.preventDefault(); moveSeries(event.key === "ArrowUp" ? -1 : 1);
      } else if (!event.shiftKey && (event.key === "Home" || event.key === "End")) {
        event.preventDefault(); choose(event.key === "Home" ? peers[0] : peers.at(-1));
      }
    }}>
    {children}
    {focused && <Box aria-hidden="true" style={{position:"absolute",bottom:pending ? undefined : 0,top:pending ? 0 : undefined,left:4,right:4,pointerEvents:"none",background:"var(--mantine-color-body)",padding:"2px 4px"}}>
      <Text id={hintId} size="xs" c="dimmed">{hint}</Text>
      {current && <Text size="xs">{summary(current)}</Text>}
    </Box>}
    <VisuallyHidden aria-live="polite" aria-atomic="true">{focused && current ? summary(current) : ""}</VisuallyHidden>
    {pending && rangeAvailable && <Box style={{flexShrink:0,maxHeight:"55%",overflow:"auto"}}>
      <Text size="xs" c="dimmed">Paused while selecting</Text>
      <Group gap={6} align="end">
        <TextInput size="xs" label="Range start (UTC)" data-mantine-stop-propagation="true" value={pending.from} onChange={e => { setPending({...pending,from:e.currentTarget.value,edited:true}); setError(undefined); }} />
        <TextInput size="xs" label="Range end (UTC)" data-mantine-stop-propagation="true" value={pending.to} onChange={e => { setPending({...pending,to:e.currentTarget.value,edited:true}); setError(undefined); }} />
        <Button size="compact-xs" data-mantine-stop-propagation="true" onClick={() => {
          const start = /(?:Z|[+-]\d{2}:\d{2})$/.test(pending.from) ? Date.parse(pending.from) : NaN;
          const end = /(?:Z|[+-]\d{2}:\d{2})$/.test(pending.to) ? Date.parse(pending.to) : NaN;
          if (!Number.isFinite(start) || !Number.isFinite(end) || start >= end || start < bounds!.from || end > bounds!.to) { setError("Enter UTC times within the observed window, with start before end."); return; }
          onZoom!(start,end); cancel();
        }}>Zoom to range</Button>
        <Button size="compact-xs" variant="subtle" data-mantine-stop-propagation="true" onClick={cancel}>Cancel range</Button>
      </Group>
      {error && <Text size="xs" c="bad" role="alert">{error}</Text>}
    </Box>}
  </Box>;
}
