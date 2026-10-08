import { Box, Button, Group, Text, VisuallyHidden } from "@mantine/core";
import { createContext, useContext, useEffect, useId, useLayoutEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { duration } from "../../../format";
import type { KeyboardPoint, PointEvent } from "../../../panels/keyboard";

// Identity excludes values, rendered indices and object identity: updates may
// change all three while the same time bucket/category is still displayed.
function pointIdentity(point: KeyboardPoint): string {
  const selection = (point.event.data as {selection?: {time?: number; from?: string; dimensions?: unknown; bucket?: unknown}} | undefined)?.selection;
  const value = point.event.value;
  return JSON.stringify([point.event.seriesName, point.event.name, selection?.from ?? selection?.time ?? (Array.isArray(value) ? value[0] : undefined), selection?.dimensions, selection?.bucket]);
}
type Range = {anchor: string; from: string; to: string};
type Window = {from: number; to: number};

function rangeLabel(range: Range): string {
  const today = new Date().toISOString().slice(0, 10);
  const fromDate = range.from.slice(0, 10), toDate = range.to.slice(0, 10);
  const clock = (value: string) => value.slice(11, -1).replace(/\.000$/, "");
  const from = (fromDate === today ? "" : fromDate + " ") + clock(range.from);
  const to = (toDate !== fromDate && toDate !== today ? toDate + " " : "") + clock(range.to);
  return `${from} – ${to} UTC · ${duration(Date.parse(range.to) - Date.parse(range.from))}`;
}
const surface = {background: "color-mix(in srgb, var(--mantine-color-body) 92%, transparent)", border: "1px solid var(--mantine-color-default-border)", borderRadius: "var(--mantine-radius-sm)", padding: "2px 6px"};

// Panel chrome owns the visual hint; standalone charts retain its accessible description.
export const ChartHintContext = createContext<{id: string; setHint(hint?: string): void} | undefined>(undefined);

/** A single plot tab stop; keyboard chrome never participates in plot layout. */
export function ChartKeyboard({ points, label, summary, children, onClick, canSelect, onHighlight, onActiveChange, onRangePending, onRangeChange, onRangeBarHeight, rangeReset, height = "100%", onZoom, bounds, pointWindow }: {
  points: readonly KeyboardPoint[]; label: string; summary(point: KeyboardPoint): string; children?: ReactNode;
  onClick?(event: PointEvent): void; canSelect?(event: PointEvent): boolean; onHighlight(point?: KeyboardPoint): void;
  onActiveChange?(active: boolean): void; onRangePending?(pending: boolean): void;
  onRangeChange?(range?: Window): void; onRangeBarHeight?(height: number): void; rangeReset?: number; height?: number | string;
  onZoom?(from: number, to: number): void; bounds?: {from: number; to: number};
  pointWindow?(point: KeyboardPoint): {from: number; to: number} | undefined;
}) {
  const [focused, setFocused] = useState(false);
  const [selectedKey, setSelectedKey] = useState<string>();
  const [pending, setPending] = useState<Range | null>(null);
  const plot = useRef<HTMLDivElement>(null);
  const hintId = useId();
  const chrome = useContext(ChartHintContext);
  const rangeBar = useRef<HTMLDivElement>(null);
  const callbacks = useRef({onHighlight, onActiveChange, onRangePending, onRangeChange, onRangeBarHeight});
  callbacks.current = {onHighlight, onActiveChange, onRangePending, onRangeChange, onRangeBarHeight};
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
    if (points.length && !identities.has(pending.anchor) || bounds && (Number.isFinite(from) && from < bounds.from || Number.isFinite(to) && to > bounds.to)) {
      setPending(null);
    }
  }, [identities, points.length, bounds?.from, bounds?.to, pending]);
  useEffect(() => { callbacks.current.onRangePending?.(Boolean(pending)); }, [Boolean(pending)]);
  useEffect(() => { callbacks.current.onRangeChange?.(pending ? {from: Date.parse(pending.from), to: Date.parse(pending.to)} : undefined); }, [pending]);
  useEffect(() => { setPending(null); }, [rangeReset]);
  useEffect(() => () => { callbacks.current.onHighlight(undefined); callbacks.current.onRangePending?.(false); callbacks.current.onRangeChange?.(undefined); }, []);
  const choose = (next: KeyboardPoint | undefined, extend = false) => {
    if (!next) return;
    if (extend && rangeAvailable && current) {
      const anchor = pending?.anchor ?? pointIdentity(current);
      const first = identities.get(anchor);
      const a = first && pointWindow?.(first), b = pointWindow?.(next);
      if (a && b) {
        const from = Math.max(bounds!.from, Math.min(a.from, b.from)), to = Math.min(bounds!.to, Math.max(a.to, b.to));
        if (Number.isFinite(from) && Number.isFinite(to) && from < to) setPending({anchor, from: new Date(from).toISOString(), to: new Date(to).toISOString()});
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
  const cancel = () => { setPending(null); plot.current?.focus(); };
  const hint = rangeAvailable ? "←→ points · ↑↓ series · Enter drill · Shift+←→ range" : "←→ points · ↑↓ series · Enter drill";
  useLayoutEffect(() => {
    chrome?.setHint(focused ? hint : undefined);
    return () => chrome?.setHint(undefined);
  }, [chrome, focused, hint]);
  useLayoutEffect(() => {
    const bar = rangeBar.current;
    if (!bar) return;
    callbacks.current.onRangeBarHeight?.(bar.getBoundingClientRect().height);
    const observer = new ResizeObserver(([entry]) => {
      if (!entry) return;
      const style = getComputedStyle(bar);
      const extra = [style.paddingTop, style.paddingBottom, style.borderTopWidth, style.borderBottomWidth].reduce((sum, value) => sum + (parseFloat(value) || 0), 0);
      callbacks.current.onRangeBarHeight?.(entry.borderBoxSize?.[0]?.blockSize ?? entry.contentRect.height + extra);
    });
    observer.observe(bar);
    return () => { observer.disconnect(); callbacks.current.onRangeBarHeight?.(0); };
  }, [Boolean(pending && rangeAvailable)]);
  return <Box ref={plot} data-chart-plot tabIndex={0} role="group" aria-label={label} aria-describedby={focused ? chrome?.id ?? hintId : undefined}
    data-mantine-stop-propagation={pending ? "true" : undefined} className="chart-keyboard-point"
    style={{position: "relative", height, width: "100%", flex: "1 1 auto", minHeight: 0, minWidth: 0}}
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
    <Box data-chart-overlay style={{position: "absolute", inset: 0, zIndex: 1, pointerEvents: "none", overflow: "hidden"}}>
      {pending && rangeAvailable && <Group ref={rangeBar} data-chart-range-bar gap={6} style={{...surface, position: "absolute", bottom: 4, left: 4, right: 4, maxHeight: "100%", overflow: "auto", pointerEvents: "auto"}}>
        <Text data-chart-range-label size="xs" title={pending.from + " – " + pending.to} truncate style={{flex: "1 1 140px", minWidth: 0}}>{rangeLabel(pending)}</Text>
        <Button size="compact-xs" data-mantine-stop-propagation="true" onClick={() => {
          onZoom!(Date.parse(pending.from), Date.parse(pending.to)); cancel();
        }}>Zoom to range</Button>
        <Button size="compact-xs" variant="subtle" data-mantine-stop-propagation="true" onClick={cancel}>Cancel</Button>
      </Group>}
    </Box>
    {!chrome && focused && <VisuallyHidden id={hintId}>{hint}</VisuallyHidden>}
    <VisuallyHidden aria-live="polite" aria-atomic="true">{focused && current ? summary(current) : ""}</VisuallyHidden>
  </Box>;
}
