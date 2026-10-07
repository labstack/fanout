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
  let width = size.width >= 900 ? 168 : 140;
  let height = 44, compact = false, gap = clamp(size.width * .012, 2, 16);
  const g = new graphlib.Graph({ directed: true, multigraph: true });
  let ranksep = clamp(size.width * .035, 8, 48);
  const run = (factor: number) => {
    g.setGraph({ rankdir: "LR", ranker: "network-simplex", acyclicer: "greedy", ranksep, nodesep: Math.max(compact ? .1 : 0, gap * factor), edgesep: 8 * factor, marginx: 0, marginy: 0 });
    if (g.nodeCount()) layout(g);
  };
  g.setDefaultEdgeLabel(() => ({}));
  for (const n of nodes.filter(n => connected.has(n.id))) g.setNode(n.id, { width, height });
  for (const e of edges) g.setEdge(e.caller, e.callee, { weight: 1, minlen: 1 }, e.id);
  run(1);
  const ranks = new Set(g.nodes().map(id => g.node(id).x)).size;
  if (ranks > 1) ranksep = clamp(ranksep + (size.width - 24 - g.graph().width) / (ranks - 1), 8, 96);
  let spacing = 1;
  const dimensions = () => {
    const graphWidth = g.graph().width ?? 0, graphHeight = g.graph().height ?? 0;
    const rowWidth = Math.max(graphWidth, Math.min(uncalled.length, Math.max(1, Math.floor(size.width / (width + gap)))) * (width + gap) - gap, width, uncalled.length ? 252 : 0);
    const columns = Math.max(1, Math.floor((rowWidth + gap) / (width + gap)));
    const uncalledTop = graphHeight + (uncalled.length ? gap + (compact ? 17 : 22) : 0);
    const totalHeight = Math.max(1, graphHeight, uncalled.length ? uncalledTop + Math.ceil(uncalled.length / columns) * (height + gap) - gap : 0);
    return {graphWidth,rowWidth,columns,uncalledTop,totalHeight};
  };
  const fitSpacing = () => {
    for (const factor of [1, .5, 0]) {
      spacing = factor;
      run(factor);
      if (dimensions().totalHeight <= size.height - 24) break;
    }
  };
  fitSpacing();
  if (dimensions().totalHeight > size.height - 24) {
    compact = true; width = 140; height = 26; gap = Math.min(gap, 3);
    for (const id of g.nodes()) g.setNode(id, { width, height });
    // Route again at the actual compact sizes before applying the scale floor.
    fitSpacing();
  }
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
  let contentWidth = Math.max(size.width, rowWidth * scale + 24);
  const contentHeight = Math.max(size.height, totalHeight * scale + 24);
  const offsetX = (contentWidth - rowWidth * scale) / 2, offsetY = (contentHeight - totalHeight * scale) / 2;
  const fit = (p: Point) => ({ x: offsetX + p.x * scale, y: offsetY + p.y * scale });
  const entries = nodes.filter(n => edges.some(e => e.caller === n.id) && !edges.some(e => e.callee === n.id));
  const positioned = nodes.map(n => {
    const i = uncalled.indexOf(n), isolated = i >= 0;
    const raw = isolated ? { x: (i % columns) * (width + gap) + width / 2, y: uncalledTop + Math.floor(i / columns) * (height + gap) + height / 2 } : g.node(n.id);
    return { ...n, ...fit({ x: raw.x - width / 2, y: raw.y - height / 2 }), width: width * scale, height: height * scale, uncalled: isolated, entry: entries.includes(n) };
  });
  // Follow the highest-traffic branch from each entry, with a visited set for cycles.
  const roots = entries.length ? entries : [...nodes].sort((a,b) => (b.request_rate ?? b.spans ?? 0) - (a.request_rate ?? a.spans ?? 0) || order(a.id,b.id)).slice(0,1);
  let weightedY = 0, weight = 0;
  for (const root of roots) {
    const traffic = Math.max(1e-9, root.request_rate ?? root.spans ?? 1), seen = new Set<string>();
    let current: string | undefined = root.id;
    while (current && !seen.has(current)) {
      seen.add(current);
      const node = positioned.find(n => n.id === current)!;
      weightedY += (node.y + node.height / 2) * traffic; weight += traffic;
      current = edges.filter(e => e.caller === current && !seen.has(e.callee)).sort((a,b) => (b.request_rate ?? b.calls) - (a.request_rate ?? a.calls) || order(a.id,b.id))[0]?.callee;
    }
  }
  const entryBoxes = positioned.filter(n => n.entry);
  let entryLaneWidth = 0;
  if (entryBoxes.length && (Math.max(...entryBoxes.map(n=>n.y+n.height)) - Math.min(...entryBoxes.map(n=>n.y)) > size.height - 24 || entryBoxes.some(n=>n.x+n.width>size.width-12))) {
    // Disconnected paths or late-ranked roots can fall outside any initial
    // viewport. Keep entries together; routes still connect to their callees.
    const rowGap = 3 * scale, perColumn = Math.max(1, Math.floor((size.height - 24 + rowGap) / (height * scale + rowGap)));
    const columnCount = Math.ceil(entryBoxes.length / perColumn), rows = Math.min(perColumn, entryBoxes.length);
    const packHeight = rows * height * scale + (rows - 1) * rowGap;
    const top = clamp(weight ? weightedY / weight - packHeight / 2 : 12, 12, contentHeight - packHeight - 12);
    const left = Math.min(...positioned.map(n=>n.x));
    // Reuse an empty entry rank; otherwise reserve a separate leading lane.
    if (columnCount > 1 || positioned.some(n=>!n.entry&&n.x<left+width*scale)) {
      entryLaneWidth = columnCount * (width * scale + rowGap);
      for (const node of positioned) if (!node.entry) node.x += entryLaneWidth;
      contentWidth += entryLaneWidth;
    }
    entryBoxes.sort((a,b)=>(b.request_rate??b.spans??0)-(a.request_rate??a.spans??0)||order(a.id,b.id)).forEach((n,i)=>{
      n.x = left + Math.floor(i / perColumn) * (width * scale + rowGap);
      n.y = top + (i % perColumn) * (height * scale + rowGap);
    });
  }
  const maxScroll = Math.max(0, contentHeight - size.height);
  const lower = Math.max(0, ...entryBoxes.map(n => n.y + n.height - size.height));
  const upper = Math.min(maxScroll, ...entryBoxes.map(n => n.y));
  const initialScrollY = clamp(weight ? weightedY / weight - size.height / 2 : 0, lower, upper);
  return {
    nodes: positioned,
    edges: edges.map(e => {
      const points = (g.edge(e.caller, e.callee, e.id).points as Point[]).map(p=>({...fit(p),x:fit(p).x+entryLaneWidth}));
      const source = positioned.find(n=>n.id===e.caller)!;
      if (source.entry) points[0] = {x:source.x+source.width,y:source.y+source.height/2};
      // Cubics through routed points with controls inside each segment's bounds.
      const path = points.slice(1).reduce((path, p, i) => {
        const prev = points[i], mid = (prev.x + p.x) / 2;
        return `${path} C${mid},${prev.y} ${mid},${p.y} ${p.x},${p.y}`;
      }, `M${points[0].x},${points[0].y}`);
      return { ...e, path };
    }),
    scale, compact, initialScrollY,
    contentWidth, contentHeight,
    uncalledLabel: uncalled.length ? fit({ x: entryLaneWidth / scale, y: uncalledTop - 8 }) : undefined,
  };
}

const nodeMetrics = (n: ServiceNode) => `${formatValue("per_second", n.request_rate)} · ${formatValue("percent", n.error_rate)} err · ${formatValue("ms", n.p95_ms)} p95`;
const shortFormat = new Intl.NumberFormat("en-US", { maximumSignificantDigits: 2, notation: "compact", useGrouping: false });
const shortNumber = (value: number | null): string => {
  if (value === null || !Number.isFinite(value)) return "—";
  const plain = shortFormat.format(value), scientific = value.toExponential(1).replace(/\.0e/, "e").replace("e+", "e");
  return scientific.length < plain.length ? scientific : plain;
};
const shortDuration = (value: number | null) => value === null ? "—" : Number(value.toPrecision(2)) >= 1000 ? `${shortNumber(value / 1000)}s` : `${shortNumber(value)}ms`;

/** Fit actual glyph advances, including the scale compensation for readable text. */
export function serviceCardLabels(n: ServiceNode, {width,scale,compact,measureText}: {width:number;scale:number;compact:boolean;measureText?:ChartSize["measureText"]}) {
  const nameSize = Math.max(12,Math.ceil(1100/scale)/100), metricSize = Math.ceil(1100/scale)/100;
  const nameFont = `600 ${nameSize}px ${fonts.display}`, metricFont = `${metricSize}px ${fonts.display}`;
  const measure = measureText ?? ((text:string,font:string) => text.length * Number(font.match(/([\d.]+)px/)?.[1] ?? 11) * .6);
  const available = Math.max(0,width-12), rate = `${shortNumber(n.request_rate)}/s`, error = `${shortNumber(n.error_rate)}${n.error_rate===null?"":"%"} err`;
  const key = n.error_rate ? error : rate, pair = `${rate} · ${error}`, all = `${pair} · p95 ${shortDuration(n.p95_ms)}`;
  const metric = compact ? key : measure(all,metricFont) <= available ? all : measure(pair,metricFont) <= available ? pair : key;
  const metricWidth = compact ? measure(metric,metricFont) : available;
  const nameWidth = Math.max(0,available-measure(healthGlyph[n.health]??"○",nameFont)-4-(compact?metricWidth+4:0));
  let name = n.id;
  if (measure(name,nameFont) > nameWidth) {
    let lo = 0, hi = name.length;
    while(lo < hi){const mid=Math.ceil((lo+hi)/2);if(measure(name.slice(0,mid)+"…",nameFont)<=nameWidth)lo=mid;else hi=mid-1;}
    name = measure("…",nameFont) <= nameWidth ? name.slice(0,lo)+"…" : "";
  }
  return {name,metric,nameSize,metricSize,nameWidth,metricWidth};
}

type Transform = { scale: number; x: number; y: number };
const fitted: Transform = { scale: 1, x: 0, y: 0 };
export function ServiceMapViz({ panel, title = panel.title, result, dark, height, onSelect, onPoint, onMapView }: AnalysisProps & { onMapView?: (view: MapView) => void }) {
  const viewport = useRef<HTMLDivElement>(null);
  const [size, setSize] = useState({ width: 500, height: Math.max(40, height - 24) });
  const [transform, setTransform] = useState(fitted);
  const [fontVersion,setFontVersion] = useState(0);
  const measureText = useMemo(() => {
    const context = document.createElement("canvas").getContext("2d");
    return context ? (text:string,font:string) => {context.font=font;return context.measureText(text).width;} : undefined;
  },[fontVersion]);
  useEffect(()=>{
    const fonts=document.fonts,refresh=()=>setFontVersion(v=>v+1);
    fonts?.addEventListener("loadingdone",refresh);
    return ()=>fonts?.removeEventListener("loadingdone",refresh);
  },[]);
  const [hover, setHover] = useState<string>();
  const [focused, setFocused] = useState<string>();
  const dragging = useRef<{ x: number; y: number; initial: Transform; moved: boolean } | null>(null);
  const suppressClick = useRef(false);
  const model = useMemo(() => serviceMapModel(result.frame!, result), [result.frame, result.from_ms, result.to_ms]);
  const fittedModel = useRef(model);
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
  const fit = () => { scrollTo(0,graph.initialScrollY,1); };
  useLayoutEffect(() => {
    const el=viewport.current;if(el){el.scrollLeft=-transform.x;el.scrollTop=-transform.y;}
  },[transform]);
  useLayoutEffect(() => {
    const el = viewport.current; if (!el) return;
    const measure = () => {
      const rect = el.getBoundingClientRect();
      const width = el.clientWidth || rect.width, height = el.clientHeight || rect.height;
      if (width > 0 && height > 0) setSize(old => old.width === width && old.height === height ? old : { width, height });
    };
    measure(); const observer = new ResizeObserver(measure); observer.observe(el);
    return () => observer.disconnect();
  }, []);
  useLayoutEffect(() => {
    if (fittedModel.current !== model || transform.scale === 1) fit();
    else scrollTo(-transform.x,-transform.y,transform.scale);
    fittedModel.current = model;
  }, [graph]);
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
    <div ref={viewport} data-service-viewport data-card-mode={graph.compact ? "compact" : "full"} data-initial-scroll-y={graph.initialScrollY} data-layout-scale={graph.scale} data-content-width={graph.contentWidth} data-content-height={graph.contentHeight} data-zoom={transform.scale} data-pan-x={transform.x} data-pan-y={transform.y} style={{ position: "relative", height: "100%", minHeight: 0, overflow: "auto", touchAction: "none", cursor: "grab" }} onMouseLeave={() => setHover(undefined)} onScroll={e=>{const {scrollLeft,scrollTop}=e.currentTarget;setTransform(old=>({...old,x:-scrollLeft,y:-scrollTop}));}}
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
          const label = serviceCardLabels(n,{width:n.width/graph.scale,scale:graph.scale,compact:graph.compact,measureText});
          return <button type="button" key={n.id} data-service-node={n.id} data-service-entry={n.entry} data-focused={focused === n.id} data-uncalled={n.uncalled} title={`${n.id} · ${n.health}\n${nodeMetrics(n)}`} aria-label={`${n.id}, ${n.health}, ${nodeMetrics(n)}`} onMouseEnter={() => setHover(n.id)} onMouseLeave={() => setHover(undefined)} onFocus={() => setFocused(n.id)} onBlur={() => setFocused(undefined)} onClick={() => select(n.id)} onKeyDown={e => { if (e.key === "Enter") { e.preventDefault(); suppressClick.current = false; select(n.id); } }}
            style={{ position: "absolute", left: n.x, top: n.y, width: n.width, height: n.height, boxSizing: "border-box", border: `1px solid ${n.health === "unhealthy" || n.health === "degraded" ? color : theme.border}`, borderRadius: 6, padding: 0, background: theme.surface, color: theme.text, textAlign: "left", overflow: "hidden", opacity: active && !neighbours.has(n.id) ? .25 : 1, outline: focused === n.id ? `2px solid ${ring}` : undefined, outlineOffset: -2, pointerEvents: "auto", cursor: "pointer", fontFamily: fonts.display }}>
            <span style={{ display: "block", width: n.width / graph.scale, height: graph.compact ? 26 : 44, boxSizing: "border-box", padding: "3px 6px", transform: `scale(${graph.scale})`, transformOrigin: "0 0" }}>
            {graph.compact ? <span data-service-text style={{display:"flex",alignItems:"center",gap:4,fontSize:label.metricSize,lineHeight:"18px",whiteSpace:"nowrap",minWidth:0}}>
              <span aria-hidden="true" style={{color,flex:"none"}}>{healthGlyph[n.health] ?? "○"}</span><span data-service-name data-text-width={label.nameWidth} style={{minWidth:0,maxWidth:label.nameWidth,fontWeight:600,fontSize:label.nameSize,whiteSpace:"nowrap"}}>{label.name}</span>
              <span data-service-metric data-text-width={label.metricWidth} style={{marginLeft:"auto",flex:"none",fontSize:label.metricSize,color:theme.muted,whiteSpace:"nowrap"}}>{label.metric}</span>
            </span> : <>
              <span data-service-text style={{display:"flex",gap:4,fontWeight:600,fontSize:label.nameSize,lineHeight:"16px",minWidth:0,whiteSpace:"nowrap"}}><span aria-hidden="true" style={{color,flex:"none"}}>{healthGlyph[n.health] ?? "○"}</span><span data-service-name data-text-width={label.nameWidth} style={{fontSize:label.nameSize,maxWidth:label.nameWidth,whiteSpace:"nowrap"}}>{label.name}</span></span>
              <span data-service-text data-service-metric data-text-width={label.metricWidth} style={{display:"block",color:theme.muted,fontSize:label.metricSize,lineHeight:"14px",whiteSpace:"nowrap"}}>{label.metric}</span>
            </>}
            </span>
          </button>;
        })}
      </div>
      </div>
    </div>
    {transform.y < -1 && <div data-service-overflow-fade="top" style={{position:"absolute",top:0,left:0,right:0,height:16,pointerEvents:"none",background:`linear-gradient(${theme.surface}, transparent)`}}/>}
    {graph.contentHeight*transform.scale-size.height+transform.y>1 && <div data-service-overflow-fade style={{position:"absolute",bottom:0,left:0,right:0,height:16,pointerEvents:"none",background:`linear-gradient(transparent, ${theme.surface})`}}/>}
    </div>
    <Text c="dimmed" fz={12} mt={4} style={{ flexShrink: 0 }}>{model.nodes.length} services · {model.edges.length} {model.edges.length === 1 ? "route" : "routes"}</Text>
  </div>;
}
