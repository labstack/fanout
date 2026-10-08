import { Text } from "@mantine/core";
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { chartThemeFor } from "../../../../panels/compile";
import { analysisSummary } from "../../../../panels/analysis";
import { healthGlyph, serviceMapModel } from "../../../../panels/rollups";
import { formatValue } from "../../../../panels/units";
import { brand, fonts, typeScale } from "../../../../tokens";
import type { AnalysisProps } from "./analysis-chart";
import ServiceMapWorker from "./service-map.worker?worker&inline";
import { fitServiceMap, layoutServiceMapRaw, nodeMetrics, serviceCardLabels, serviceMapStructure, type MapLayout } from "./service-map-layout";
const clamp = (n: number, min: number, max: number) => Math.min(max, Math.max(min, n));
export type MapView = { canFit: boolean; fit(): void };
const fittedView = { x: 0, y: 0, zoom: 1 };

export function ServiceMapViz({ panel, title = panel.title, result, dark, height, onSelect, onPoint, onMapView }: AnalysisProps & { onMapView?: (view: MapView) => void }) {
  const viewport = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ width: 500, height: Math.max(40, height - 24) });
  const [measured, setMeasured] = useState(false);
  const [fontVersion, setFontVersion] = useState(0);
  const measureText = useMemo(() => {
    const context = document.createElement("canvas").getContext("2d");
    return context ? (text: string, font: string) => { context.font = font; return context.measureText(text).width; } : undefined;
  }, [fontVersion]);
  useEffect(() => { const fonts = document.fonts, refresh = () => setFontVersion(v => v + 1); fonts?.addEventListener("loadingdone", refresh); return () => fonts?.removeEventListener("loadingdone", refresh); }, []);
  const [hover, setHover] = useState<string>(), [focused, setFocused] = useState<string>();
  const [view, setView] = useState(fittedView);
  const dragging = useRef<{ x: number; y: number; initial: typeof view; moved: boolean } | null>(null);
  const suppressClick = useRef(false);
  const model = useMemo(() => serviceMapModel(result.frame!, result), [result.frame, result.from_ms, result.to_ms]);
  const structure = useMemo(() => serviceMapStructure(model, measureText), [model, measureText]);
  const [cached, setCached] = useState<{ key: string; layout: MapLayout; model: typeof model }>();
  const [layoutError, setLayoutError] = useState<string>();
  const latest = useRef({ model, size, structure }); latest.current = { model, size, structure };
  useLayoutEffect(() => {
    if (!measured) return;
    const { model, size, structure } = latest.current;
    setLayoutError(undefined);
    if (model.nodes.length <= 60) { setCached({ key: structure.key, layout: layoutServiceMapRaw(model, size, structure.widths), model }); return; }
    let worker: Worker;
    try { worker = new ServiceMapWorker(); } catch { setLayoutError("Service layout unavailable"); return; }
    let active = true;
    worker.onmessage = (event: MessageEvent<MapLayout>) => { if (active) setCached({ key: structure.key, layout: event.data, model }); };
    worker.onerror = () => { if (active) setLayoutError("Service layout unavailable"); };
    try { worker.postMessage({ model, size: { width: size.width, height: size.height }, widths: structure.widths }); }
    catch { active = false; worker.terminate(); setLayoutError("Service layout unavailable"); return; }
    return () => { active = false; worker.terminate(); };
  }, [structure.key, measured]);
  const graph = useMemo(() => cached ? fitServiceMap(cached.layout, cached.key === structure.key ? model : cached.model, size) : { nodes: [], edges: [], scale: 1, compact:true, contentWidth: size.width, contentHeight: size.height, uncalledLabel: undefined }, [cached, model, size, structure.key]);
  const theme = chartThemeFor(dark), ring = brand[dark ? 4 : 7], id = useId().replaceAll(":", "");
  const active = hover ?? focused;
  const neighbours = new Set([active, ...model.edges.filter(e => e.caller === active || e.callee === active).flatMap(e => [e.caller, e.callee])]);
  const maxZoom = Math.max(12, 1 / graph.scale);
  const maxRate = Math.max(1e-9, ...model.edges.map(e => e.request_rate ?? e.calls));
  const fit = useCallback(() => setView(fittedView), []);
  useLayoutEffect(() => {
    const element = viewport.current; if (!element) return;
    const measure = () => { const rect = element.getBoundingClientRect(); const width = element.clientWidth || rect.width, height = element.clientHeight || rect.height; if (width > 0 && height > 0) setSize(old => old.width === width && old.height === height ? old : { width, height }); };
    measure(); setMeasured(true); const observer = new ResizeObserver(measure); observer.observe(element);
    return () => observer.disconnect();
  }, []);
  useLayoutEffect(fit, [fit, structure.topology]);
  useEffect(() => { onMapView?.({ canFit: view.zoom !== 1 || view.x !== 0 || view.y !== 0, fit }); return () => onMapView?.({ canFit: false, fit: () => undefined }); }, [onMapView, view, fit]);
  useEffect(() => {
    const element = viewport.current; if (!element) return;
    const wheel = (event: WheelEvent) => {
      if (!cached || layoutError || event.deltaY === 0) return;
      event.preventDefault(); const rect = element.getBoundingClientRect(), x = event.clientX - rect.left, y = event.clientY - rect.top;
      setView(old => { const zoom = clamp(old.zoom * Math.exp(-event.deltaY * .002), 1, maxZoom); if (zoom === 1) return fittedView; const ratio = zoom / old.zoom; return { zoom, x: x - (x - old.x) * ratio, y: y - (y - old.y) * ratio }; });
    };
    element.addEventListener("wheel", wheel, { passive: false });
    return () => element.removeEventListener("wheel", wheel);
  }, [maxZoom, Boolean(cached), layoutError]);
  const select = (service: string) => { if (suppressClick.current) { suppressClick.current = false; return; } if (panel.click && onSelect) onSelect(service); else onPoint?.({ dimensions: { service } }); };
  return <div role="region" aria-label={`${title}: service dependency graph; ${analysisSummary({ ...panel, title }, result)}`} style={{ display: "flex", flexDirection: "column", flex: "1 1 auto", minHeight: 0, minWidth: 0, width: "100%" }}>
    <div ref={viewport} {...(import.meta.env.DEV ? { "data-service-viewport": true, "data-layout-scale": graph.scale, "data-card-mode":graph.compact?"compact":"full" } : {})} style={{ position: "relative", flex: "1 1 auto", minHeight: 0, overflow: "hidden", touchAction: "none", cursor: dragging.current ? "grabbing" : "grab" }} onMouseLeave={() => setHover(undefined)}
      onPointerDown={e => { if (e.button !== 0) return; suppressClick.current = false; dragging.current = { x: e.clientX, y: e.clientY, initial: view, moved: false }; }}
      onPointerMove={e => { const drag = dragging.current; if (!drag) return; const dx = e.clientX - drag.x, dy = e.clientY - drag.y; if (Math.hypot(dx, dy) > 3) drag.moved = true; if (drag.moved) { e.currentTarget.setPointerCapture?.(e.pointerId); setView({ ...drag.initial, x: drag.initial.x + dx, y: drag.initial.y + dy }); } }}
      onPointerUp={() => { suppressClick.current = dragging.current?.moved ?? false; dragging.current = null; }} onPointerCancel={() => { dragging.current = null; suppressClick.current = false; }}>
      {(layoutError || !cached) && <Text role="status" c="dimmed">{layoutError ?? "Laying out services…"}</Text>}
      <div {...(import.meta.env.DEV ? { "data-service-content": true } : {})} style={{ position: "relative", width: graph.contentWidth, height: graph.contentHeight, transform: `translate(${view.x}px, ${view.y}px) scale(${view.zoom})`, transformOrigin: "0 0" }}>
      <svg aria-hidden="true" width={graph.contentWidth} height={graph.contentHeight} viewBox={`0 0 ${graph.contentWidth} ${graph.contentHeight}`} style={{ position: "absolute", inset: 0 }}>
        <defs>{[theme.muted, theme.status.warn, theme.status.bad].map((color, i) => <marker key={color} id={`${id}-arrow-${i}`} viewBox="0 0 6 6" refX={5} refY={3} markerWidth={5} markerHeight={5} orient="auto-start-reverse" markerUnits="userSpaceOnUse"><path d="M0,0 L6,3 L0,6 Z" fill={color} /></marker>)}</defs>
        <g>
          {graph.edges.map(e => <path key={e.id} {...(import.meta.env.DEV ? { "data-service-edge": true, "data-caller": e.caller, "data-callee": e.callee } : {})} d={e.path} fill="none" stroke={e.status ? theme.status[e.status] : theme.muted} strokeLinejoin="round" strokeWidth={1 + 3 * Math.log1p(e.request_rate ?? e.calls) / Math.log1p(maxRate)} opacity={active && e.caller !== active && e.callee !== active ? .25 : 1} markerEnd={`url(#${id}-arrow-${e.status === "bad" ? 2 : e.status === "warn" ? 1 : 0})`} style={{ pointerEvents: "stroke" }}><title>{`${e.caller} → ${e.callee}\n${e.edge_type} · ${formatValue("per_second", e.request_rate)} · ${formatValue("count", e.calls)} calls\n${formatValue("percent", e.error_rate)} errors · ${formatValue("ms", e.average_ms)} average`}</title></path>)}
        </g>
      </svg>
      <div style={{ position: "absolute", inset: 0, pointerEvents: "none" }}>
        {graph.uncalledLabel && <span {...(import.meta.env.DEV ? { "data-service-uncalled-label": true } : {})} style={{ position: "absolute", left: "50%", top: graph.uncalledLabel.y, transform: "translateX(-50%)", color: theme.muted, fontSize: typeScale.micro / view.zoom, lineHeight: 1, whiteSpace: "nowrap" }}>No traced calls in this window</span>}
        {graph.nodes.map(n => {
          const color = n.health === "unhealthy" ? theme.status.bad : n.health === "degraded" ? theme.status.warn : n.health === "healthy" ? theme.status.ok : theme.muted;
          const label = serviceCardLabels(n, { width: n.width / graph.scale, scale: graph.scale * view.zoom, compact:graph.compact, measureText });
          return <button type="button" key={n.id} {...(import.meta.env.DEV ? { "data-service-node": n.id, "data-service-entry": n.entry } : {})} title={`${n.id} · ${n.health}\n${nodeMetrics(n)}`} aria-label={`${n.id}, ${n.health}, ${nodeMetrics(n)}`} onMouseEnter={() => setHover(n.id)} onMouseLeave={() => setHover(undefined)} onFocus={() => setFocused(n.id)} onBlur={() => setFocused(undefined)} onClick={() => select(n.id)} onKeyDown={e => { if (e.key === "Enter") { e.preventDefault(); suppressClick.current = false; select(n.id); } }}
            style={{ position: "absolute", left: n.x, top: n.y, width: n.width, height: n.height, display: "flex", flexDirection:"column", justifyContent:"center", gap: 3 / view.zoom, boxSizing: "border-box", border: `${1 / view.zoom}px solid ${n.health === "unhealthy" || n.health === "degraded" ? color : theme.border}`, borderRadius: 6, padding: `0 ${5 / view.zoom}px`, background: theme.surface, color: theme.text, textAlign: "left", opacity: active && !neighbours.has(n.id) ? .25 : 1, outline: focused === n.id ? `2px solid ${ring}` : undefined, outlineOffset: -2, pointerEvents: "auto", cursor: "pointer", fontFamily: fonts.display }}>
            <span {...(import.meta.env.DEV ? { "data-service-text": true } : {})} style={{ display: "flex", alignItems: "center", gap: 4 / view.zoom, width: "100%", minWidth: 0, fontSize: Math.max(typeScale.micro / view.zoom, 12 * graph.scale), lineHeight: 1, whiteSpace: "nowrap" }}>
              <span aria-hidden="true" style={{ color, flex: "none" }}>{healthGlyph[n.health] ?? "○"}</span>
              <span {...(import.meta.env.DEV ? { "data-service-name": true } : {})} style={{ flex: "none", fontWeight: 600, whiteSpace: "nowrap" }}>{label.name}</span>
            </span>
            {!graph.compact && <span {...(import.meta.env.DEV ? { "data-service-text": true, "data-service-metric": true } : {})} style={{ width:"100%", fontSize:label.metricSize*graph.scale, lineHeight:1, color: theme.muted, whiteSpace:"nowrap" }}>{label.metric}</span>}
          </button>;
        })}
      </div>
      </div>
    </div>
    <Text c="dimmed" fz={12} mt={4} style={{ flexShrink: 0 }}>{model.nodes.length} services · {model.edges.length} {model.edges.length === 1 ? "route" : "routes"}</Text>
  </div>;
}
