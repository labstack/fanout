import { Text } from "@mantine/core";
import { graphlib, layout } from "@dagrejs/dagre";
import { useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { chartThemeFor, type ChartSize } from "../../../../panels/compile";
import { analysisSummary } from "../../../../panels/analysis";
import { healthGlyph, serviceMapModel, type ServiceGraph, type ServiceNode } from "../../../../panels/rollups";
import { formatValue } from "../../../../panels/units";
import { brand, fonts } from "../../../../tokens";
import type { AnalysisProps } from "./analysis-chart";

const order = (a: string, b: string) => a < b ? -1 : a > b ? 1 : 0;
const clamp = (n: number, min: number, max: number) => Math.min(max, Math.max(min, n));
type Point = { x: number; y: number };
export type MapView = { zoomed: boolean; fit(): void };

/** Fit spacing before cards. Overflow remains scrollable at a readable scale. */
export function layoutServiceMap(model: ServiceGraph, size: ChartSize) {
  const nodes = [...model.nodes].sort((a, b) => order(a.id, b.id));
  const edges = [...model.edges].sort((a, b) => order(a.id, b.id));
  const connected = new Set(edges.flatMap(e => [e.caller, e.callee]));
  const uncalled = nodes.filter(n => !connected.has(n.id));
  const width = 140, height = 44;
  const gap = clamp(size.width * .012, 2, 16);
  const g = new graphlib.Graph({ directed: true, multigraph: true });
  let ranksep = clamp(size.width * .035, 8, 48);
  const run = (factor: number) => {
    g.setGraph({ rankdir: "LR", ranker: "network-simplex", acyclicer: "greedy", ranksep, nodesep: gap * factor, edgesep: 8 * factor, marginx: 0, marginy: 0 });
    if (g.nodeCount()) layout(g);
  };
  g.setDefaultEdgeLabel(() => ({}));
  for (const n of nodes.filter(n => connected.has(n.id))) g.setNode(n.id, { width, height });
  for (const e of edges) g.setEdge(e.caller, e.callee, { weight: 1, minlen: 1 }, e.id);
  run(1);
  const ranks = new Set(g.nodes().map(id => g.node(id).x)).size;
  if (ranks > 1) ranksep = clamp(ranksep + (size.width - 24 - g.graph().width) / (ranks - 1), 8, 96);
  // Compact spacing, not typography, when a rank is tall. Edge routes are
  // recomputed by Dagre on each pass; boxes still retain their full design size.
  let spacing = 1;
  for (const factor of [1, .5, 0]) {
    spacing = factor;
    run(factor);
    const isolatedHeight = uncalled.length ? gap + 22 + height : 0;
    if ((g.graph().height + isolatedHeight) * .85 <= size.height - 24) break;
  }
  const dimensions = () => {
    const graphWidth = g.graph().width ?? 0, graphHeight = g.graph().height ?? 0;
    const rowWidth = Math.max(graphWidth, Math.min(uncalled.length, Math.max(1, Math.floor(size.width / (width + gap)))) * (width + gap) - gap, width, uncalled.length ? 252 : 0);
    const columns = Math.max(1, Math.floor((rowWidth + gap) / (width + gap)));
    const uncalledTop = graphHeight + (uncalled.length ? gap + 22 : 0);
    const totalHeight = Math.max(1, graphHeight, uncalled.length ? uncalledTop + Math.ceil(uncalled.length / columns) * (height + gap) - gap : 0);
    return {graphWidth,rowWidth,columns,uncalledTop,totalHeight};
  };
  const initial = dimensions();
  const readableScale = Math.max(.85,Math.min(1,Math.max(1,size.width-24)/initial.rowWidth,Math.max(1,size.height-24)/initial.totalHeight));
  if(ranks>1 && initial.graphWidth>=initial.rowWidth) {
    // A height fit must not leave the ranks bunched in the middle. Redistribute
    // their horizontal spacing at the final card scale, keeping card sizes.
    ranksep=Math.max(8,ranksep+((size.width-24)/readableScale-initial.graphWidth)/(ranks-1));
    run(spacing);
  }
  const {rowWidth,columns,uncalledTop,totalHeight}=dimensions();
  const scale = Math.max(.85, Math.min(1, Math.max(1, size.width - 24) / rowWidth, Math.max(1, size.height - 24) / totalHeight));
  const contentWidth = Math.max(size.width, rowWidth * scale + 24), contentHeight = Math.max(size.height, totalHeight * scale + 24);
  const offsetX = (contentWidth - rowWidth * scale) / 2, offsetY = (contentHeight - totalHeight * scale) / 2;
  const fit = (p: Point) => ({ x: offsetX + p.x * scale, y: offsetY + p.y * scale });
  return {
    nodes: nodes.map(n => {
      const i = uncalled.indexOf(n), isolated = i >= 0;
      const raw = isolated ? { x: (i % columns) * (width + gap) + width / 2, y: uncalledTop + Math.floor(i / columns) * (height + gap) + height / 2 } : g.node(n.id);
      return { ...n, ...fit({ x: raw.x - width / 2, y: raw.y - height / 2 }), width: width * scale, height: height * scale, uncalled: isolated };
    }),
    edges: edges.map(e => {
      const points = (g.edge(e.caller, e.callee, e.id).points as Point[]).map(fit);
      // Cubics through routed points with controls inside each segment's bounds.
      const path = points.slice(1).reduce((path, p, i) => {
        const prev = points[i], mid = (prev.x + p.x) / 2;
        return `${path} C${mid},${prev.y} ${mid},${p.y} ${p.x},${p.y}`;
      }, `M${points[0].x},${points[0].y}`);
      return { ...e, path };
    }),
    scale,
    contentWidth, contentHeight,
    uncalledLabel: uncalled.length ? fit({ x: 0, y: uncalledTop - 8 }) : undefined,
  };
}

const nodeMetrics = (n: ServiceNode) => `${formatValue("per_second", n.request_rate)} · ${formatValue("percent", n.error_rate)} err · ${formatValue("ms", n.p95_ms)} p95`;
type Transform = { scale: number; x: number; y: number };
const fitted: Transform = { scale: 1, x: 0, y: 0 };
export function ServiceMapViz({ panel, title = panel.title, result, dark, height, onSelect, onPoint, onMapView }: AnalysisProps & { onMapView?: (view: MapView) => void }) {
  const viewport = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ width: 500, height: Math.max(40, height - 24) });
  const [transform, setTransform] = useState(fitted);
  const [hover, setHover] = useState<string>();
  const [focused, setFocused] = useState<string>();
  const dragging = useRef<{ x: number; y: number; initial: Transform; moved: boolean } | null>(null);
  const suppressClick = useRef(false);
  const model = useMemo(() => serviceMapModel(result.frame!, result), [result.frame, result.from_ms, result.to_ms]);
  const graph = useMemo(() => layoutServiceMap(model, size), [model, size]);
  const theme = chartThemeFor(dark), ring = brand[dark ? 4 : 7];
  const id = useId().replaceAll(":", "");
  const active = hover ?? focused;
  const neighbours = new Set([active, ...model.edges.filter(e => e.caller === active || e.callee === active).flatMap(e => [e.caller, e.callee])]);
  const maxRate = Math.max(1e-9, ...model.edges.map(e => e.request_rate ?? e.calls));
  const scrollTo = (x: number, y: number, scale = transform.scale) => {
    const el = viewport.current; if (!el) return;
    el.scrollLeft = clamp(x, 0, Math.max(0, graph.contentWidth * scale - (el.clientWidth||size.width)));
    el.scrollTop = clamp(y, 0, Math.max(0, graph.contentHeight * scale - (el.clientHeight||size.height)));
    setTransform({scale,x:-el.scrollLeft,y:-el.scrollTop});
  };
  const fit = () => { scrollTo(0,0,1); };
  useLayoutEffect(() => {
    const el=viewport.current;if(el){el.scrollLeft=-transform.x;el.scrollTop=-transform.y;}
  },[transform]);
  useEffect(() => {
    const el = viewport.current; if (!el) return;
    const measure = () => {
      const rect = el.getBoundingClientRect();
      if (rect.width > 0 && rect.height > 0) setSize(old => old.width === rect.width && old.height === rect.height ? old : { width: rect.width, height: rect.height });
      setTransform(fitted);
      el.scrollLeft=0;el.scrollTop=0;
    };
    measure(); const observer = new ResizeObserver(measure); observer.observe(el);
    return () => observer.disconnect();
  }, []);
  useEffect(() => { fit(); }, [model]);
  useEffect(() => { onMapView?.({ zoomed: transform.scale !== 1 || transform.x !== 0 || transform.y !== 0 || graph.contentHeight > size.height || graph.contentWidth > size.width, fit }); return () => onMapView?.({ zoomed: false, fit: () => undefined }); }, [onMapView, transform, graph.contentHeight, graph.contentWidth, size]);
  useEffect(() => {
    const el = viewport.current; if (!el) return;
    const wheel = (e: WheelEvent) => {
      e.preventDefault();
      if (!e.ctrlKey && !e.metaKey) { scrollTo(el.scrollLeft+e.deltaX,el.scrollTop+e.deltaY); return; }
      const rect = el.getBoundingClientRect(), x = e.clientX - rect.left, y = e.clientY - rect.top;
      const scale = clamp(transform.scale * Math.exp(-e.deltaY * .002), 1, 6), ratio = scale / transform.scale;
      scrollTo((x+el.scrollLeft)*ratio-x,(y+el.scrollTop)*ratio-y,scale);
    };
    el.addEventListener("wheel", wheel, { passive: false }); return () => el.removeEventListener("wheel", wheel);
  }, [size,graph.contentHeight,graph.contentWidth,transform]);
  const select = (service: string) => {
    if (suppressClick.current) { suppressClick.current = false; return; }
    if (panel.click && onSelect) onSelect(service);
    else onPoint?.({ dimensions: { service } });
  };
  return <div role="region" aria-label={`${title}: service dependency graph; ${analysisSummary({ ...panel, title }, result)}`} style={{ display: "flex", flexDirection: "column", flex: "1 1 auto", minHeight: 0, minWidth: 0 }}>
    <div style={{position:"relative",flex:"1 1 auto",minHeight:0}}>
    <div ref={viewport} data-service-viewport data-layout-scale={graph.scale} data-content-width={graph.contentWidth} data-content-height={graph.contentHeight} data-zoom={transform.scale} data-pan-x={transform.x} data-pan-y={transform.y} style={{ position: "relative", height: "100%", minHeight: 0, overflow: "auto", touchAction: "none", cursor: "grab" }} onMouseLeave={() => setHover(undefined)} onScroll={e=>{const {scrollLeft,scrollTop}=e.currentTarget;setTransform(old=>({...old,x:-scrollLeft,y:-scrollTop}));}}
      onPointerDown={e => { if (e.button !== 0) return; suppressClick.current = false; dragging.current = { x: e.clientX, y: e.clientY, initial: transform, moved: false }; if (!(e.target as Element).closest("button")) e.currentTarget.setPointerCapture?.(e.pointerId); }}
      onPointerMove={e => { const drag = dragging.current; if (!drag) return; const dx = e.clientX - drag.x, dy = e.clientY - drag.y; if (Math.hypot(dx, dy) > 3) drag.moved = true; if (drag.moved) { e.currentTarget.setPointerCapture?.(e.pointerId); scrollTo(-drag.initial.x-dx,-drag.initial.y-dy,drag.initial.scale); } }}
      onPointerUp={() => { suppressClick.current = dragging.current?.moved ?? false; dragging.current = null; }} onPointerCancel={() => { dragging.current = null; suppressClick.current = false; }}>
      <div data-service-content style={{position:"relative",width:graph.contentWidth*transform.scale,height:graph.contentHeight*transform.scale}}>
      <svg aria-hidden="true" width={graph.contentWidth*transform.scale} height={graph.contentHeight*transform.scale} viewBox={`0 0 ${graph.contentWidth} ${graph.contentHeight}`} style={{ position: "absolute", inset: 0 }}>
        <defs>{[theme.muted, theme.status.warn, theme.status.bad].map((color, i) => <marker key={color} id={`${id}-arrow-${i}`} viewBox="0 0 6 6" refX={5} refY={3} markerWidth={5} markerHeight={5} orient="auto-start-reverse" markerUnits="userSpaceOnUse"><path d="M0,0 L6,3 L0,6 Z" fill={color} /></marker>)}</defs>
        <g>
          {graph.edges.map(e => <path key={e.id} data-service-edge={e.id} d={e.path} fill="none" stroke={e.status ? theme.status[e.status] : theme.muted} strokeWidth={1 + 3 * Math.log1p(e.request_rate ?? e.calls) / Math.log1p(maxRate)} opacity={active && e.caller !== active && e.callee !== active ? .25 : 1} markerEnd={`url(#${id}-arrow-${e.status === "bad" ? 2 : e.status === "warn" ? 1 : 0})`} style={{ pointerEvents: "stroke" }}><title>{`${e.caller} → ${e.callee}\n${e.edge_type} · ${formatValue("per_second", e.request_rate)} · ${formatValue("count", e.calls)} calls\n${formatValue("percent", e.error_rate)} errors · ${formatValue("ms", e.average_ms)} average`}</title></path>)}
        </g>
      </svg>
      <div style={{ position: "absolute", inset: 0, transformOrigin: "0 0", transform: `scale(${transform.scale})`, pointerEvents: "none" }}>
        {graph.uncalledLabel && <span data-uncalled-label style={{ position: "absolute", left: graph.uncalledLabel.x, top: graph.uncalledLabel.y - 12 * graph.scale, transform: `scale(${graph.scale})`, transformOrigin: "0 0", color: theme.muted, fontSize: Math.ceil(1200/graph.scale)/100, whiteSpace: "nowrap" }}>No traced calls in this window</span>}
        {graph.nodes.map(n => {
          const color = n.health === "unhealthy" ? theme.status.bad : n.health === "degraded" ? theme.status.warn : n.health === "healthy" ? theme.status.ok : theme.muted;
          return <button type="button" key={n.id} data-service-node={n.id} data-focused={focused === n.id} data-uncalled={n.uncalled} title={`${n.id} · ${n.health}\n${nodeMetrics(n)}`} aria-label={`${n.id}, ${n.health}, ${nodeMetrics(n)}`} onMouseEnter={() => setHover(n.id)} onMouseLeave={() => setHover(undefined)} onFocus={() => setFocused(n.id)} onBlur={() => setFocused(undefined)} onClick={() => select(n.id)} onKeyDown={e => { if (e.key === "Enter") { e.preventDefault(); suppressClick.current = false; select(n.id); } }}
            style={{ position: "absolute", left: n.x, top: n.y, width: n.width, height: n.height, boxSizing: "border-box", border: `1px solid ${n.health === "unhealthy" || n.health === "degraded" ? color : theme.border}`, borderRadius: 6, padding: 0, background: theme.surface, color: theme.text, textAlign: "left", overflow: "hidden", opacity: active && !neighbours.has(n.id) ? .25 : 1, outline: focused === n.id ? `2px solid ${ring}` : undefined, outlineOffset: -2, pointerEvents: "auto", cursor: "pointer", fontFamily: fonts.display }}>
            <span style={{ display: "block", width: n.width / graph.scale, height: 44, boxSizing: "border-box", padding: "3px 6px", transform: `scale(${graph.scale})`, transformOrigin: "0 0" }}>
            <span data-service-text style={{ display: "flex", gap: 4, fontWeight: 600, fontSize: Math.max(12,Math.ceil(1100/graph.scale)/100), lineHeight: "16px", minWidth: 0 }}><span aria-hidden="true" style={{ color }}>{healthGlyph[n.health] ?? "○"}</span><span style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{n.id}</span></span>
            <span data-service-text style={{ display: "block", color: theme.muted, fontSize: Math.ceil(1100/graph.scale)/100, lineHeight: "14px", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{nodeMetrics(n)}</span>
            </span>
          </button>;
        })}
      </div>
      </div>
    </div>
    {graph.contentHeight*transform.scale-size.height+transform.y>1 && <div data-service-overflow-fade style={{position:"absolute",bottom:0,left:0,right:0,height:16,pointerEvents:"none",background:`linear-gradient(transparent, ${theme.surface})`}}/>}
    </div>
    <Text c="dimmed" fz={12} mt={4} style={{ flexShrink: 0 }}>{model.nodes.length} services · {model.edges.length} {model.edges.length === 1 ? "route" : "routes"}</Text>
  </div>;
}
