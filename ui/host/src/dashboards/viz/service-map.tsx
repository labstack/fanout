import { Text } from "@mantine/core";
import { useCallback, useEffect, useId, useLayoutEffect, useMemo, useRef, useState } from "react";
import { chartThemeFor } from "../../../../panels/compile";
import { analysisSummary } from "../../../../panels/analysis";
import { healthGlyph, serviceMapModel } from "../../../../panels/rollups";
import { formatValue } from "../../../../panels/units";
import { brand, fonts } from "../../../../tokens";
import type { AnalysisProps } from "./analysis-chart";

import { fitServiceMap, layoutServiceMapRaw, nodeMetrics, serviceCardLabels, serviceMapStructure, type MapLayout } from "./service-map-layout";
const clamp = (n: number, min: number, max: number) => Math.min(max, Math.max(min, n));
export type MapView = { canFit: boolean; fit(): void };

export function ServiceMapViz({ panel, title = panel.title, result, dark, height, onSelect, onPoint, onMapView }: AnalysisProps & { onMapView?: (view: MapView) => void }) {
  const viewport = useRef<HTMLDivElement>(null);
  const programmaticTop = useRef<number | null>(null);
  const [size, setSize] = useState({ width: 500, height: Math.max(40, height - 24) });
  const [scrollY, setScrollY] = useState(0);
  const [measured,setMeasured] = useState(false);
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
  const dragging = useRef<{ x: number; y: number; initial: number; moved: boolean } | null>(null);
  const suppressClick = useRef(false);
  const model = useMemo(() => serviceMapModel(result.frame!, result), [result.frame, result.from_ms, result.to_ms]);
  const structure = useMemo(()=>serviceMapStructure(model,measureText),[model,measureText]);
  const [cached, setCached] = useState<{topology:string;layout:MapLayout;model:ReturnType<typeof serviceMapModel>}>();
  const [layoutError,setLayoutError] = useState<string>();
  const fittedKey = useRef<string | undefined>(undefined);
  const compactRequired=Boolean(cached && !cached.layout.compact && cached.layout.width*.85>Math.max(1,size.width-24));
  const latest = useRef({model,size,structure,cached}); latest.current={model,size,structure,cached};
  useLayoutEffect(() => {
    if (!measured) return;
    const {model,size,structure,cached}=latest.current;
    if (!compactRequired && cached?.topology===structure.topology && cached.layout.nodes.every(n=>
      n.width===structure.widths[n.id][cached.layout.compact ? cached.layout.metrics ? "compact" : "identity" : "full"])) return;
    setLayoutError(undefined);
    if (model.nodes.length <= 60) {setCached({topology:structure.topology,layout:layoutServiceMapRaw(model,size,structure.widths),model});return;}
    let worker: Worker;
    try { worker = new Worker(new URL("./service-map.worker.ts", import.meta.url), {type:"module"}); }
    catch { setLayoutError("Service layout unavailable"); return; }
    let active=true;
    worker.onmessage = (event: MessageEvent<MapLayout>) => {if(active)setCached({topology:structure.topology,layout:event.data,model});};
    worker.onerror = () => {if(active)setLayoutError("Service layout unavailable");};
    worker.postMessage({model,size:{width:size.width,height:size.height},widths:structure.widths});
    return () => {active=false;worker.terminate();};
  },[structure.key,measured,compactRequired]);
  const graph = useMemo(() => cached ? fitServiceMap(cached.layout,cached.topology===structure.topology?model:cached.model,size) : {nodes:[],edges:[],scale:1,compact:true,folded:false,initialScrollY:0,contentWidth:size.width,contentHeight:size.height,uncalledLabel:undefined},[cached,model,size,structure.key]);
  const theme = chartThemeFor(dark), ring = brand[dark ? 4 : 7];
  const id = useId().replaceAll(":", "");
  const active = hover ?? focused;
  const neighbours = new Set([active, ...model.edges.filter(e => e.caller === active || e.callee === active).flatMap(e => [e.caller, e.callee])]);
  const maxRate = Math.max(1e-9, ...model.edges.map(e => e.request_rate ?? e.calls));
  const scrollTo = useCallback((y: number) => {
    const el = viewport.current; if (!el) return;
    el.scrollLeft = 0;
    const next = clamp(y, 0, Math.max(0, graph.contentHeight - (el.clientHeight||size.height)));
    if (el.scrollTop !== next) {
      el.scrollTop = next;
      programmaticTop.current = el.scrollTop;
    }
    const top = el.scrollTop;
    setScrollY(old=>old===top?old:top);
  },[graph.contentHeight,size.height]);
  const fit = useCallback(() => { scrollTo(graph.initialScrollY); },[scrollTo,graph.initialScrollY]);
  useLayoutEffect(() => {
    const el=viewport.current;
    if(el){
      el.scrollLeft=0;
      if(el.scrollTop!==scrollY){
        el.scrollTop=scrollY;
        programmaticTop.current=el.scrollTop;
      }
    }
  },[scrollY]);
  useLayoutEffect(() => {
    const el = viewport.current; if (!el) return;
    const measure = () => {
      const rect = el.getBoundingClientRect();
      const width = el.clientWidth || rect.width, height = el.clientHeight || rect.height;
      if (width > 0 && height > 0) setSize(old => old.width === width && old.height === height ? old : { width, height });
    };
    measure(); setMeasured(true); const observer = new ResizeObserver(measure); observer.observe(el);
    return () => observer.disconnect();
  }, []);
  useLayoutEffect(() => {
    if (!cached || !viewport.current) return;
    if (fittedKey.current !== cached.topology) fit();
    else scrollTo(viewport.current.scrollTop);
    fittedKey.current = cached.topology;
  }, [graph,cached]);
  useEffect(() => { onMapView?.({ canFit: scrollY !== 0 || graph.contentHeight > size.height, fit }); return () => onMapView?.({ canFit: false, fit: () => undefined }); }, [onMapView, scrollY, graph.contentHeight, size, fit]);
  const select = (service: string) => {
    if (suppressClick.current) { suppressClick.current = false; return; }
    if (panel.click && onSelect) onSelect(service);
    else onPoint?.({ dimensions: { service } });
  };
  const below=graph.nodes.filter(n=>n.y+n.height>size.height+scrollY+.5).length;
  return <div role="region" aria-label={`${title}: service dependency graph; ${analysisSummary({ ...panel, title }, result)}`} style={{ display: "flex", flexDirection: "column", flex: "1 1 auto", minHeight: 0, minWidth: 0 }}>
    <div style={{position:"relative",flex:"1 1 auto",minHeight:0}}>
    <div ref={viewport} {...(import.meta.env.DEV ? { "data-service-viewport": true, "data-card-mode": (graph.compact ? "compact" : "full"), "data-initial-scroll-y": graph.initialScrollY, "data-layout-scale": graph.scale, "data-content-width": graph.contentWidth, "data-content-height": graph.contentHeight } : {})} style={{ position: "relative", height: "100%", minHeight: 0, overflowX: "hidden", overflowY:"auto", touchAction: "pan-y", cursor: "grab" }} onMouseLeave={() => setHover(undefined)} onScroll={e=>{
      const top = e.currentTarget.scrollTop;
      e.currentTarget.scrollLeft=0;
      // Native scroll events are queued and may coalesce. Keep the last
      // programmed position until a user scroll differs, ignoring its echoes.
      if (programmaticTop.current === top) return;
      programmaticTop.current = null;
      setScrollY(old=>old===top?old:top);
    }}
      onPointerDown={e => { if (e.button !== 0 || e.pointerType === "touch") return; suppressClick.current = false; dragging.current = { x: e.clientX, y: e.clientY, initial: scrollY, moved: false }; if (!(e.target as Element).closest("button")) e.currentTarget.setPointerCapture?.(e.pointerId); }}
      onPointerMove={e => { const drag = dragging.current; if (!drag) return; const dx = e.clientX - drag.x, dy = e.clientY - drag.y; if (Math.hypot(dx, dy) > 3) drag.moved = true; if (drag.moved) { e.currentTarget.setPointerCapture?.(e.pointerId); scrollTo(drag.initial-dy); } }}
      onPointerUp={() => { suppressClick.current = dragging.current?.moved ?? false; dragging.current = null; }} onPointerCancel={() => { dragging.current = null; suppressClick.current = false; }}>
      {(layoutError || !cached) && <Text role="status" c="dimmed">{layoutError ?? "Laying out services…"}</Text>}
      <div {...(import.meta.env.DEV ? { "data-service-content": true } : {})} style={{position:"relative",width:graph.contentWidth,height:graph.contentHeight}}>
      <svg aria-hidden="true" width={graph.contentWidth} height={graph.contentHeight} viewBox={`0 0 ${graph.contentWidth} ${graph.contentHeight}`} style={{ position: "absolute", inset: 0 }}>
        <defs>{[theme.muted, theme.status.warn, theme.status.bad].map((color, i) => <marker key={color} id={`${id}-arrow-${i}`} viewBox="0 0 6 6" refX={5} refY={3} markerWidth={5} markerHeight={5} orient="auto-start-reverse" markerUnits="userSpaceOnUse"><path d="M0,0 L6,3 L0,6 Z" fill={color} /></marker>)}</defs>
        <g>
          {graph.edges.map(e => <path key={e.id} d={e.path} fill="none" stroke={e.status ? theme.status[e.status] : theme.muted} strokeWidth={1 + 3 * Math.log1p(e.request_rate ?? e.calls) / Math.log1p(maxRate)} opacity={active && e.caller !== active && e.callee !== active ? .25 : 1} markerEnd={`url(#${id}-arrow-${e.status === "bad" ? 2 : e.status === "warn" ? 1 : 0})`} style={{ pointerEvents: "stroke" }}><title>{`${e.caller} → ${e.callee}\n${e.edge_type} · ${formatValue("per_second", e.request_rate)} · ${formatValue("count", e.calls)} calls\n${formatValue("percent", e.error_rate)} errors · ${formatValue("ms", e.average_ms)} average`}</title></path>)}
        </g>
      </svg>
      <div style={{ position: "absolute", inset: 0, transformOrigin: "0 0", pointerEvents: "none" }}>
        {graph.uncalledLabel && <span style={{ position: "absolute", left: graph.uncalledLabel.x, top: graph.uncalledLabel.y - 12 * graph.scale, transform: `scale(${graph.scale})`, transformOrigin: "0 0", color: theme.muted, fontSize: Math.ceil(1200/graph.scale)/100, lineHeight:1, whiteSpace: "nowrap" }}>No traced calls in this window</span>}
        {graph.nodes.map(n => {
          const color = n.health === "unhealthy" ? theme.status.bad : n.health === "degraded" ? theme.status.warn : n.health === "healthy" ? theme.status.ok : theme.muted;
          const label = serviceCardLabels(n,{width:n.width/graph.scale,scale:graph.scale,compact:graph.compact,measureText});
          return <button type="button" key={n.id} {...(import.meta.env.DEV ? { "data-service-node": n.id, "data-service-entry": n.entry } : {})} title={`${n.id} · ${n.health}\n${nodeMetrics(n)}`} aria-label={`${n.id}, ${n.health}, ${nodeMetrics(n)}`} onMouseEnter={() => setHover(n.id)} onMouseLeave={() => setHover(undefined)} onFocus={() => setFocused(n.id)} onBlur={() => setFocused(undefined)} onClick={() => select(n.id)} onKeyDown={e => { if (e.key === "Enter") { e.preventDefault(); suppressClick.current = false; select(n.id); } }}
            style={{ position: "absolute", left: n.x, top: n.y, width: n.width, height: n.height, boxSizing: "border-box", border: `1px solid ${n.health === "unhealthy" || n.health === "degraded" ? color : theme.border}`, borderRadius: 6, padding: 0, background: theme.surface, color: theme.text, textAlign: "left", overflow: "hidden", opacity: active && !neighbours.has(n.id) ? .25 : 1, outline: focused === n.id ? `2px solid ${ring}` : undefined, outlineOffset: -2, pointerEvents: "auto", cursor: "pointer", fontFamily: fonts.display }}>
            <span style={{ display: "block", width: n.width / graph.scale, height: graph.compact ? 20 : 44, boxSizing: "border-box", padding: graph.compact?"1px 6px":"3px 6px", transform: `scale(${graph.scale})`, transformOrigin: "0 0" }}>
            {graph.compact ? <span {...(import.meta.env.DEV ? { "data-service-text": true } : {})} style={{display:"flex",alignItems:"center",gap:4,fontSize:label.metricSize,lineHeight:"18px",whiteSpace:"nowrap",minWidth:0}}>
              <span aria-hidden="true" style={{color,flex:"none"}}>{healthGlyph[n.health] ?? "○"}</span><span {...(import.meta.env.DEV ? { "data-service-name": true } : {})} style={{minWidth:0,maxWidth:label.nameWidth,fontWeight:600,fontSize:label.nameSize,whiteSpace:"nowrap"}}>{label.name}</span>
              <span {...(import.meta.env.DEV ? { "data-service-metric": true } : {})} style={{display:label.metric?"inline":"none",marginLeft:"auto",flex:"none",fontSize:label.metricSize,color:theme.muted,whiteSpace:"nowrap"}}>{label.metric}</span>
            </span> : <>
              <span {...(import.meta.env.DEV ? { "data-service-text": true } : {})} style={{display:"flex",gap:4,fontWeight:600,fontSize:label.nameSize,lineHeight:"16px",minWidth:0,whiteSpace:"nowrap"}}><span aria-hidden="true" style={{color,flex:"none"}}>{healthGlyph[n.health] ?? "○"}</span><span {...(import.meta.env.DEV ? { "data-service-name": true } : {})} style={{fontSize:label.nameSize,maxWidth:label.nameWidth,whiteSpace:"nowrap"}}>{label.name}</span></span>
              <span {...(import.meta.env.DEV ? { "data-service-text": true, "data-service-metric": true } : {})} style={{display:"block",color:theme.muted,fontSize:label.metricSize,lineHeight:"14px",whiteSpace:"nowrap"}}>{label.metric}</span>
            </>}
            </span>
          </button>;
        })}
      </div>
      </div>
    </div>
    {scrollY > 1 && <div {...(import.meta.env.DEV ? { "data-service-overflow-fade": "top" } : {})} style={{position:"absolute",top:0,left:0,right:0,height:16,pointerEvents:"none",background:`linear-gradient(${theme.surface}, transparent)`}}/>}
    {graph.contentHeight-size.height-scrollY>1 && <div {...(import.meta.env.DEV ? { "data-service-overflow-fade": true } : {})} style={{position:"absolute",bottom:0,left:0,right:0,height:16,pointerEvents:"none",background:`linear-gradient(transparent, ${theme.surface})`}}/>}
    {below>0&&<span {...(import.meta.env.DEV ? { "data-service-below-hint": true } : {})} style={{position:"absolute",bottom:4,right:12,fontSize:12,color:theme.muted,background:theme.surface,padding:"0 4px",pointerEvents:"none"}}>+{below} below</span>}
    </div>
    <Text c="dimmed" fz={12} mt={4} style={{ flexShrink: 0 }}>{model.nodes.length} services · {model.edges.length} {model.edges.length === 1 ? "route" : "routes"}</Text>
  </div>;
}
