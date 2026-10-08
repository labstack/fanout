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

/** Fit routing and per-node cards horizontally; excess height remains scrollable. */
export function layoutServiceMap(model: ServiceGraph, size: ChartSize) {
  const nodes = [...model.nodes].sort((a,b)=>order(a.id,b.id)), edges = [...model.edges].sort((a,b)=>order(a.id,b.id));
  const connected = new Set(edges.flatMap(e=>[e.caller,e.callee]));
  const uncalled = nodes.filter(n=>!connected.has(n.id));
  const entries = nodes.filter(n=>edges.some(e=>e.caller===n.id)&&!edges.some(e=>e.callee===n.id));
  const g = new graphlib.Graph({directed:true,multigraph:true});
  g.setDefaultEdgeLabel(()=>({}));
  for(const e of edges)g.setEdge(e.caller,e.callee,{weight:1,minlen:1},e.id);
  const innerWidth=Math.max(1,size.width-24), innerHeight=Math.max(1,size.height-24);
  let compact=false, height=44, nodesep=16, ranksep=48, metrics=true;
  let widths=new Map<string,number>();
  type Box = ServiceNode & Point & {width:number;height:number;entry:boolean;uncalled:boolean};
  type Raw = {nodes:Box[];routes:Point[][];width:number;height:number;label?:Point};
  const configure=()=>{
    widths=new Map(nodes.map(n=>[n.id,serviceCardWidth(n,compact,size.measureText,metrics)]));
    for(const n of nodes.filter(n=>connected.has(n.id)))g.setNode(n.id,{width:widths.get(n.id),height});
  };
  const run=()=>{
    g.setGraph({rankdir:"LR",ranker:"network-simplex",acyclicer:"greedy",ranksep,nodesep,edgesep:compact?2:8,marginx:0,marginy:0});
    if(g.nodeCount())layout(g);
  };
  const regular=():Raw=>{
    // Isolated cards live beside the graph, sharing its height rather than
    // adding a footer rank that clips otherwise fitted connected services.
    const lane=uncalled.length?Math.max(252,...uncalled.map(n=>widths.get(n.id)!))+8:0;
    const boxes=nodes.map(n=>{
      const i=uncalled.indexOf(n), isolated=i>=0, width=widths.get(n.id)!;
      const raw=isolated?{x:0,y:17+i*(height+6)}:{x:lane+g.node(n.id).x-width/2,y:g.node(n.id).y-height/2};
      return {...n,...raw,width,height,entry:entries.includes(n),uncalled:isolated};
    });
    const routes=edges.map(e=>(g.edge(e.caller,e.callee,e.id).points as Point[]).map(p=>({x:p.x+lane,y:p.y})));
    return {nodes:boxes,routes,width:Math.max(1,lane+(g.graph().width??0),...boxes.map(n=>n.x+n.width))+16,height:Math.max(1,g.graph().height??0,...boxes.map(n=>n.y+n.height)),label:uncalled.length?{x:0,y:12}:undefined};
  };
  const fitRanks=()=>{
    for(const gap of [48,24,8,0]){
      ranksep=gap;run();if(regular().width*.85<=innerWidth)break;
    }
  };
  configure();
  for(const gap of [16,8,.1]){nodesep=gap;fitRanks();if(regular().height<=innerHeight)break;}
  if(regular().height>innerHeight||regular().width*.85>innerWidth){
    compact=true;height=24;nodesep=6;configure();fitRanks();
  }
  if(regular().width*.85>innerWidth){metrics=false;configure();fitRanks();}
  let raw=regular(),folded=false;
  const busyCentre=(boxes:Box[])=>{
    const roots=entries.length?entries:[...nodes].sort((a,b)=>(b.request_rate??b.spans??0)-(a.request_rate??a.spans??0)||order(a.id,b.id)).slice(0,1);
    let y=0,weight=0;
    for(const root of roots){
      const traffic=Math.max(1e-9,root.request_rate??root.spans??edges.filter(e=>e.caller===root.id).reduce((n,e)=>n+(e.request_rate??e.calls),0)??1),seen=new Set<string>();
      let current:string|undefined=root.id;
      while(current&&!seen.has(current)){
        seen.add(current);const n=boxes.find(n=>n.id===current)!;y+=(n.y+n.height/2)*traffic;weight+=traffic;
        current=edges.filter(e=>e.caller===current&&!seen.has(e.callee)).sort((a,b)=>(b.request_rate??b.calls)-(a.request_rate??a.calls)||order(a.id,b.id))[0]?.callee;
      }
    }
    return weight?y/weight:raw.height/2;
  };
  // Keep separated roots in a common leading rank. Measure the extra routing
  // lane before fitting; it must never be appended to already-fitted content.
  const roots=raw.nodes.filter(n=>n.entry);
  if(roots.length&&Math.max(...roots.map(n=>n.y+n.height))-Math.min(...roots.map(n=>n.y))>innerHeight/.85){
    const left=Math.min(...raw.nodes.filter(n=>!n.uncalled).map(n=>n.x));
    const perColumn=Math.max(1,Math.floor((innerHeight/.85+6)/(height+6))), columns=Math.ceil(roots.length/perColumn);
    const cellWidth=Math.max(...roots.map(n=>n.width))+6;
    if(columns>1||raw.nodes.some(n=>!n.entry&&!n.uncalled&&n.x<left+cellWidth)){
      const extra=columns*cellWidth;
      for(const n of raw.nodes)if(!n.entry&&!n.uncalled)n.x+=extra;
      for(const route of raw.routes)for(const p of route)p.x+=extra;
      raw.width+=extra;
    }
    const packHeight=Math.min(perColumn,roots.length)*(height+6)-6;
    const top=clamp(busyCentre(raw.nodes)-packHeight/2,0,Math.max(0,raw.height-packHeight));
    roots.sort((a,b)=>(b.request_rate??b.spans??0)-(a.request_rate??a.spans??0)||order(a.id,b.id)).forEach((n,i)=>{n.x=left+Math.floor(i/perColumn)*cellWidth;n.y=top+(i%perColumn)*(height+6);});
    edges.forEach((e,i)=>{const source=raw.nodes.find(n=>n.id===e.caller)!;if(source.entry)raw.routes[i][0]={x:source.x+source.width,y:source.y+source.height/2};});
  }
  if(raw.width*.85>innerWidth){
    folded=true;
    // A long chain cannot fit readable full identities in a narrow LR view.
    // Fold Dagre's ordered ranks into bounded rows, retaining the dependency
    // ordering and routed edges while allowing only vertical navigation.
    compact=true;height=24;nodesep=6;metrics=false;configure();run();
    const groups:ServiceNode[][]=[entries];
    const ranks=[...new Set(g.nodes().map(id=>g.node(id).x))].sort((a,b)=>a-b);
    for(const x of ranks)groups.push(nodes.filter(n=>connected.has(n.id)&&!entries.includes(n)&&g.node(n.id).x===x).sort((a,b)=>g.node(a.id).y-g.node(b.id).y||order(a.id,b.id)));
    groups.push(uncalled);
    const boxes:Box[]=[],capacity=Math.max(1,innerWidth/.85-16);
    let y=0,label:Point|undefined;
    for(const group of groups.filter(group=>group.length)){
      if(group===uncalled){label={x:0,y:y+12};y+=17;}
      let x=0;
      for(const n of group){const width=widths.get(n.id)!;if(x&&x+width>capacity){x=0;y+=height+6;}boxes.push({...n,x,y,width,height,entry:entries.includes(n),uncalled:uncalled.includes(n)});x+=width+6;}
      y+=height+6;
    }
    const routes=edges.map(e=>{
      const source=boxes.find(n=>n.id===e.caller)!,target=boxes.find(n=>n.id===e.callee)!;
      return [{x:source.x+source.width/2,y:source.y+source.height},{x:target.x+target.width/2,y:target.y}];
    });
    raw={nodes:boxes,routes,width:Math.max(1,label?252:0,...boxes.map(n=>n.x+n.width))+16,height:Math.max(1,y-6),label};
  }
  // Include routed points and stroke/arrow room, not only card rectangles.
  const points=raw.routes.flat(), minX=Math.min(0,...points.map(p=>p.x)),minY=Math.min(0,...points.map(p=>p.y));
  const width=Math.max(raw.width,...points.map(p=>p.x+8))-minX;
  const totalHeight=Math.max(raw.height,...points.map(p=>p.y))-minY;
  const scale=Math.min(1,innerWidth/width,Math.max(.85,innerHeight/totalHeight));
  const contentWidth=size.width,contentHeight=Math.max(size.height,totalHeight*scale+24);
  const offsetX=(size.width-width*scale)/2+8*scale,offsetY=(contentHeight-totalHeight*scale)/2;
  const fit=(p:Point)=>({x:offsetX+(p.x-minX)*scale,y:offsetY+(p.y-minY)*scale});
  const positioned=raw.nodes.map(n=>({...n,...fit(n),width:n.width*scale,height:n.height*scale}));
  const entryBoxes=positioned.filter(n=>n.entry),maxScroll=Math.max(0,contentHeight-size.height);
  const lower=Math.max(0,...entryBoxes.map(n=>n.y+n.height-size.height)),upper=Math.min(maxScroll,...entryBoxes.map(n=>n.y));
  const initialScrollY=clamp(fit({x:0,y:busyCentre(raw.nodes)}).y-size.height/2,lower,upper);
  return {nodes:positioned,edges:edges.map((e,index)=>{
    const points=raw.routes[index].map(fit);
    const path=points.slice(1).reduce((path,p,i)=>{const prev=points[i],mid=(prev.x+p.x)/2;return `${path} C${mid},${prev.y} ${mid},${p.y} ${p.x},${p.y}`;},`M${points[0].x},${points[0].y}`);
    return {...e,path};
  }),scale,compact,folded,initialScrollY,contentWidth,contentHeight,uncalledLabel:raw.label?fit(raw.label):undefined};
}

const nodeMetrics = (n: ServiceNode) => `${formatValue("per_second", n.request_rate)} · ${formatValue("percent", n.error_rate)} err · ${formatValue("ms", n.p95_ms)} p95`;
const shortFormat = new Intl.NumberFormat("en-US", { maximumSignificantDigits: 2, notation: "compact", useGrouping: false });
const shortNumber = (value: number | null): string => {
  if (value === null || !Number.isFinite(value)) return "—";
  const plain = shortFormat.format(value), scientific = value.toExponential(1).replace(/\.0e/, "e").replace("e+", "e");
  return scientific.length < plain.length ? scientific : plain;
};
const shortDuration = (value: number | null) => value === null ? "—" : Number(value.toPrecision(2)) >= 1000 ? `${shortNumber(value / 1000)}s` : `${shortNumber(value)}ms`;

const textMeasure: NonNullable<ChartSize["measureText"]> = (text,font)=>Array.from(text).length*Number(font.match(/([\d.]+)px/)?.[1]??11)*.6;
const protectedName=(name:string)=>Array.from(name).length>24?Array.from(name).slice(0,24).join("")+"…":name;

/** Keep the identity; optional metrics consume only its remaining space. */
export function serviceCardLabels(n: ServiceNode, {width,scale,compact,measureText}: {width:number;scale:number;compact:boolean;measureText?:ChartSize["measureText"]}) {
  const nameSize=Math.max(12,Math.ceil(1100/scale)/100),metricSize=Math.ceil(1100/scale)/100;
  const measure=measureText??textMeasure,nameFont=`600 ${nameSize}px ${fonts.display}`,metricFont=`${metricSize}px ${fonts.display}`;
  const name=protectedName(n.id),available=Math.max(0,width-14),glyph=measure(healthGlyph[n.health]??"○",nameFont);
  const rate=`${shortNumber(n.request_rate)}/s`,error=`${shortNumber(n.error_rate)}${n.error_rate===null?"":"%"} err`;
  const key=n.error_rate?error:rate,pair=`${rate} · ${error}`,all=`${pair} · p95 ${shortDuration(n.p95_ms)}`;
  const metric=compact?(measure(name,nameFont)+glyph+8+measure(key,metricFont)<=available?key:""):measure(all,metricFont)<=available?all:measure(pair,metricFont)<=available?pair:key;
  const metricWidth=compact?measure(metric,metricFont):available;
  const nameWidth=Math.max(0,available-glyph-4-(compact&&metric?metricWidth+4:0));
  return {name,metric,nameSize,metricSize,nameWidth,metricWidth};
}

function serviceCardWidth(n:ServiceNode,compact:boolean,measureText?:ChartSize["measureText"],metrics=true) {
  // Size for the floor's compensated fonts, so later scale selection cannot
  // turn a protected name into clipped text.
  const label=serviceCardLabels(n,{width:1000,scale:.85,compact,measureText}),measure=measureText??textMeasure;
  const font=`600 ${label.nameSize}px ${fonts.display}`;
  const name=measure(label.name,font)+measure(healthGlyph[n.health]??"○",font)+18;
  const withMetric=name+4+measure(label.metric,`${label.metricSize}px ${fonts.display}`);
  return compact?(metrics&&withMetric<=200?Math.ceil(withMetric):Math.ceil(name)):Math.ceil(Math.max(168,name));
}

type Transform = { scale: number; x: number; y: number };
const fitted: Transform = { scale: 1, x: 0, y: 0 };
export function ServiceMapViz({ panel, title = panel.title, result, dark, height, onSelect, onPoint, onMapView }: AnalysisProps & { onMapView?: (view: MapView) => void }) {
  const viewport = useRef<HTMLDivElement>(null);
  const programmaticTop = useRef<number | null>(null);
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
  const graph = useMemo(() => layoutServiceMap(model, {...size,measureText}), [model, size,measureText]);
  const theme = chartThemeFor(dark), ring = brand[dark ? 4 : 7];
  const id = useId().replaceAll(":", "");
  const active = hover ?? focused;
  const neighbours = new Set([active, ...model.edges.filter(e => e.caller === active || e.callee === active).flatMap(e => [e.caller, e.callee])]);
  const maxRate = Math.max(1e-9, ...model.edges.map(e => e.request_rate ?? e.calls));
  const scrollTo = (x: number, y: number, scale = transform.scale) => {
    const el = viewport.current; if (!el) return;
    el.scrollLeft = 0;
    const next = clamp(y, 0, Math.max(0, graph.contentHeight * scale - (el.clientHeight||size.height)));
    if (el.scrollTop !== next) {
      el.scrollTop = next;
      programmaticTop.current = el.scrollTop;
    }
    const top = el.scrollTop;
    setTransform(old => old.scale === 1 && old.x === 0 && old.y === -top ? old : {scale:1,x:0,y:-top});
  };
  const fit = () => { scrollTo(0,graph.initialScrollY,1); };
  useLayoutEffect(() => {
    const el=viewport.current;
    if(el){
      el.scrollLeft=0;
      if(el.scrollTop!==-transform.y){
        el.scrollTop=-transform.y;
        programmaticTop.current=el.scrollTop;
      }
    }
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
      if (e.deltaY !== 0) scrollTo(0,el.scrollTop+e.deltaY);
    };
    el.addEventListener("wheel", wheel, { passive: false }); return () => el.removeEventListener("wheel", wheel);
  }, [size,graph.contentHeight,graph.contentWidth,transform]);
  const select = (service: string) => {
    if (suppressClick.current) { suppressClick.current = false; return; }
    if (panel.click && onSelect) onSelect(service);
    else onPoint?.({ dimensions: { service } });
  };
  const below=graph.nodes.filter(n=>n.y+n.height>size.height-transform.y+.5).length;
  return <div role="region" aria-label={`${title}: service dependency graph; ${analysisSummary({ ...panel, title }, result)}`} style={{ display: "flex", flexDirection: "column", flex: "1 1 auto", minHeight: 0, minWidth: 0 }}>
    <div style={{position:"relative",flex:"1 1 auto",minHeight:0}}>
    <div ref={viewport} data-service-viewport data-card-mode={graph.compact ? "compact" : "full"} data-initial-scroll-y={graph.initialScrollY} data-layout-scale={graph.scale} data-content-width={graph.contentWidth} data-content-height={graph.contentHeight} data-zoom={transform.scale} data-pan-x={transform.x} data-pan-y={transform.y} style={{ position: "relative", height: "100%", minHeight: 0, overflowX: "hidden", overflowY:"auto", touchAction: "none", cursor: "grab" }} onMouseLeave={() => setHover(undefined)} onScroll={e=>{
      const top = e.currentTarget.scrollTop;
      e.currentTarget.scrollLeft=0;
      // Native scroll events are queued and may coalesce. Keep the last
      // programmed position until a user scroll differs, ignoring its echoes.
      if (programmaticTop.current === top) return;
      programmaticTop.current = null;
      setTransform(old => old.y === -top ? old : { ...old, x: 0, y: -top });
    }}
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
            <span style={{ display: "block", width: n.width / graph.scale, height: graph.compact ? 24 : 44, boxSizing: "border-box", padding: graph.compact?"2px 6px":"3px 6px", transform: `scale(${graph.scale})`, transformOrigin: "0 0" }}>
            {graph.compact ? <span data-service-text style={{display:"flex",alignItems:"center",gap:4,fontSize:label.metricSize,lineHeight:"18px",whiteSpace:"nowrap",minWidth:0}}>
              <span aria-hidden="true" style={{color,flex:"none"}}>{healthGlyph[n.health] ?? "○"}</span><span data-service-name data-text-width={label.nameWidth} style={{minWidth:0,maxWidth:label.nameWidth,fontWeight:600,fontSize:label.nameSize,whiteSpace:"nowrap"}}>{label.name}</span>
              <span data-service-metric data-text-width={label.metricWidth} style={{display:label.metric?"inline":"none",marginLeft:"auto",flex:"none",fontSize:label.metricSize,color:theme.muted,whiteSpace:"nowrap"}}>{label.metric}</span>
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
    {below>0&&<span data-service-below-hint data-count={below} style={{position:"absolute",bottom:4,right:12,fontSize:12,color:theme.muted,background:theme.surface,padding:"0 4px",pointerEvents:"none"}}>+{below} below</span>}
    </div>
    <Text c="dimmed" fz={12} mt={4} style={{ flexShrink: 0 }}>{model.nodes.length} services · {model.edges.length} {model.edges.length === 1 ? "route" : "routes"}</Text>
  </div>;
}
