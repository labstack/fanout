import { graphlib, layout } from "@dagrejs/dagre";
import type { ChartSize } from "../../../../panels/compile";
import { healthGlyph, type ServiceGraph, type ServiceNode } from "../../../../panels/rollups";
import { formatValue } from "../../../../panels/units";
import { fonts } from "../../../../tokens";
const order = (a: string, b: string) => a < b ? -1 : a > b ? 1 : 0;
const clamp = (n: number, min: number, max: number) => Math.min(max, Math.max(min, n));
type Point = { x: number; y: number };

/** Fit routing and per-node cards horizontally; excess height remains scrollable. */
export type CardWidths = Record<string, {full:number;compact:number;identity:number}>;
export function serviceMapStructure(model: ServiceGraph, measureText?: ChartSize["measureText"]) {
  const widths: CardWidths = Object.create(null);
  for (const n of model.nodes) widths[n.id] = {full:serviceCardWidth(n,false,measureText),compact:serviceCardWidth(n,true,measureText),identity:serviceCardWidth(n,true,measureText,false)};
  const orderedWidths=Object.entries(widths).sort(([a],[b])=>order(a,b));
  const topology=JSON.stringify([orderedWidths.map(([id])=>id),model.edges.map(e=>[e.id,e.caller,e.callee]).sort((a,b)=>order(a[0],b[0]))]);
  const key=JSON.stringify([topology,orderedWidths]);
  return {key,topology,widths};
}
export type Box = ServiceNode & Point & {width:number;height:number;entry:boolean;uncalled:boolean};
export type Raw = {nodes:Box[];routes:Point[][];width:number;height:number;label?:Point};
export type MapLayout = Raw & {compact:boolean;folded:boolean;metrics:boolean;centre:number;groups?:string[][]};
export function layoutServiceMapRaw(model: ServiceGraph, size: ChartSize, cardWidths?: CardWidths): MapLayout {
  const nodes = [...model.nodes].sort((a,b)=>order(a.id,b.id)), edges = [...model.edges].sort((a,b)=>order(a.id,b.id));
  const outgoing = new Map<string, typeof edges>();
  const incoming = new Set(edges.map(e=>e.callee));
  for (const e of edges) { const list = outgoing.get(e.caller) ?? []; list.push(e); outgoing.set(e.caller,list); }
  for (const list of outgoing.values()) list.sort((a,b)=>(b.request_rate??b.calls)-(a.request_rate??a.calls)||order(a.id,b.id));
  const connected = new Set(edges.flatMap(e=>[e.caller,e.callee]));
  const uncalled = nodes.filter(n=>!connected.has(n.id));
  const entries = nodes.filter(n=>outgoing.has(n.id)&&!incoming.has(n.id));
  const g = new graphlib.Graph({directed:true,multigraph:true});
  g.setDefaultEdgeLabel(()=>({}));
  for(const e of edges)g.setEdge(e.caller,e.callee,{weight:1,minlen:1},e.id);
  const innerWidth=Math.max(1,size.width-24), innerHeight=Math.max(1,size.height-24);
  // A simple fan-out has one leaf rank. If even the minimum full-card
  // separation cannot fit it vertically, full-card trials cannot succeed.
  const fanout = entries.length === 1 && uncalled.length === 0 && edges.length === nodes.length - 1
    && incoming.size === nodes.length - 1 && edges.every(e => e.caller === entries[0].id);
  const compactFirst = fanout && (nodes.length - 1) * 44 + Math.max(0, nodes.length - 2) * .1 > innerHeight;
  let compact=compactFirst, height=compactFirst?20:44, nodesep=compactFirst?4:16, ranksep=48, metrics=true;
  let widths=new Map<string,number>();
  const entryIDs = new Set(entries.map(n=>n.id)), isolatedIDs = new Set(uncalled.map(n=>n.id));
  const isolatedIndexes = new Map(uncalled.map((n,i)=>[n.id,i]));
  const large = nodes.length > 60;
  let foldedGroups: string[][] | undefined;
  const configure=()=>{
    widths=new Map(nodes.map(n=>[n.id,cardWidths?.[n.id]?.[compact ? metrics ? "compact" : "identity" : "full"] ?? serviceCardWidth(n,compact,size.measureText,metrics)]));
    for(const n of nodes.filter(n=>connected.has(n.id)))g.setNode(n.id,{width:widths.get(n.id),height});
  };
  const run=()=>{
    g.setGraph({rankdir:"LR",ranker:"network-simplex",acyclicer:"greedy",ranksep,nodesep,edgesep:compact?2:8,marginx:0,marginy:0});
    if(g.nodeCount())layout(g);
  };
  const regular=():Raw=>{
    // Isolated cards live beside the graph, sharing its height rather than
    // adding a footer rank that clips otherwise fitted connected services.
    const strip=compact&&uncalled.length>0&&uncalled.reduce((sum,n)=>sum+widths.get(n.id)!+4,0)<=innerWidth/.75;
    const lane=uncalled.length&&!strip?Math.max(252,...uncalled.map(n=>widths.get(n.id)!))+8:0;
    const graphTop=strip?17+height+4:0;
    let isolatedX=0;
    const isolatedLeft=new Map(uncalled.map(n=>{const x=isolatedX;isolatedX+=widths.get(n.id)!+4;return [n.id,x];}));
    const boxes=nodes.map(n=>{
      const i=isolatedIndexes.get(n.id) ?? -1, isolated=i>=0, width=widths.get(n.id)!;
      const raw=isolated?{x:strip?isolatedLeft.get(n.id)!:0,y:strip?17:17+i*(height+4)}:{x:lane+g.node(n.id).x-width/2,y:graphTop+g.node(n.id).y-height/2};
      return {...n,...raw,width,height,entry:entryIDs.has(n.id),uncalled:isolated};
    });
    const routes=edges.map(e=>(g.edge(e.caller,e.callee,e.id).points as Point[]).map(p=>({x:p.x+lane,y:p.y+graphTop})));
    return {nodes:boxes,routes,width:Math.max(1,lane+(g.graph().width??0),...boxes.map(n=>n.x+n.width))+16,height:Math.max(1,graphTop+(g.graph().height??0),...boxes.map(n=>n.y+n.height)),label:uncalled.length?{x:0,y:12}:undefined};
  };
  const fitRanks=()=>{
    for(const gap of compact?[24,16,8]:[48,24,8,0]){
      ranksep=gap;run();if(regular().width*(compact?.75:.85)<=innerWidth)break;
    }
  };
  if(large){compact=true;height=20;nodesep=4;metrics=false;configure();run();}
  else {
  configure();
  if(compact)fitRanks();
  else for(const gap of [16,8,.1]){nodesep=gap;fitRanks();if(regular().height<=innerHeight)break;}
  if(!compact&&(regular().height>innerHeight||regular().width*.85>innerWidth)){
    compact=true;height=20;nodesep=4;configure();fitRanks();
  }
  if(regular().width*.85>innerWidth){metrics=false;configure();fitRanks();}
  }
  let raw=regular(),folded=false;
  const busyCentre=(boxes:Box[])=>{
    const roots=entries.length?entries:[...nodes].sort((a,b)=>(b.request_rate??b.spans??0)-(a.request_rate??a.spans??0)||order(a.id,b.id)).slice(0,1);
    const boxesByID = new Map(boxes.map(n=>[n.id,n]));
    let y=0,weight=0;
    for(const root of roots){
      const traffic=Math.max(1e-9,root.request_rate??root.spans??(outgoing.get(root.id)??[]).reduce((n,e)=>n+(e.request_rate??e.calls),0)??1),seen=new Set<string>();
      let current:string|undefined=root.id;
      while(current&&!seen.has(current)){
        seen.add(current);const n=boxesByID.get(current)!;y+=(n.y+n.height/2)*traffic;weight+=traffic;
        current=outgoing.get(current)?.find(e=>!seen.has(e.callee))?.callee;
      }
    }
    return weight?y/weight:raw.height/2;
  };
  // Keep separated roots in a common leading rank. Measure the extra routing
  // lane before fitting; it must never be appended to already-fitted content.
  const roots=raw.nodes.filter(n=>n.entry);
  if(!large&&roots.length&&Math.max(...roots.map(n=>n.y+n.height))-Math.min(...roots.map(n=>n.y))>innerHeight/.85){
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
    const boxesByID=new Map(raw.nodes.map(n=>[n.id,n]));
    edges.forEach((e,i)=>{const source=boxesByID.get(e.caller)!;if(source.entry)raw.routes[i][0]={x:source.x+source.width,y:source.y+source.height/2};});
  }
  if(large||raw.width*.85>innerWidth){
    folded=true;
    // A long chain cannot fit readable full identities in a narrow LR view.
    // Fold Dagre's ordered ranks into bounded rows, retaining the dependency
    // ordering and routed edges while allowing only vertical navigation.
    compact=true;height=20;nodesep=4;metrics=false;if(!large){configure();run();}
    const groups:ServiceNode[][]=[entries];
    const ranked = new Map<number,ServiceNode[]>();
    for (const n of nodes) if(connected.has(n.id)&&!entryIDs.has(n.id)) {const x=g.node(n.id).x;const list=ranked.get(x)??[];list.push(n);ranked.set(x,list);}
    for (const [,rank] of [...ranked].sort(([a],[b])=>a-b)) groups.push(rank.sort((a,b)=>g.node(a.id).y-g.node(b.id).y||order(a.id,b.id)));
    groups.push(uncalled);
    foldedGroups = groups.map(group=>group.map(n=>n.id));
    const boxes:Box[]=[],capacity=Math.max(1,innerWidth/.85-16);
    let y=0,label:Point|undefined;
    for(const group of groups.filter(group=>group.length)){
      if(group===uncalled){label={x:0,y:y+12};y+=17;}
      let x=0;
      for(const n of group){const width=widths.get(n.id)!;if(x&&x+width>capacity){x=0;y+=height+6;}boxes.push({...n,x,y,width,height,entry:entryIDs.has(n.id),uncalled:isolatedIDs.has(n.id)});x+=width+6;}
      y+=height+6;
    }
    const boxesByID=new Map(boxes.map(n=>[n.id,n]));
    const routes=edges.map(e=>{
      const source=boxesByID.get(e.caller)!,target=boxesByID.get(e.callee)!;
      return [{x:source.x+source.width/2,y:source.y+source.height},{x:target.x+target.width/2,y:target.y}];
    });
    raw={nodes:boxes,routes,width:Math.max(1,label?252:0,...boxes.map(n=>n.x+n.width))+16,height:Math.max(1,y-6),label};
  }
  if (compact && !foldedGroups) {
    const ranks=new Map<number,Box[]>();
    for(const n of raw.nodes) if(!n.entry&&!n.uncalled){const x=n.x+n.width/2;const rank=ranks.get(x)??[];rank.push(n);ranks.set(x,rank);}
    foldedGroups=[raw.nodes.filter(n=>n.entry).map(n=>n.id),...[...ranks].sort(([a],[b])=>a-b).map(([,rank])=>rank.sort((a,b)=>a.y-b.y||order(a.id,b.id)).map(n=>n.id)),uncalled.map(n=>n.id)];
  }
  return {...raw,compact,folded,metrics,centre:busyCentre(raw.nodes),groups:foldedGroups};
}

/** Cheap scale/translation and folded-row packing; does not invoke Dagre. */
export function fitServiceMap(layout: MapLayout, model: ServiceGraph, size: ChartSize) {
  let raw: Raw = layout;
  const {compact} = layout;
  let {folded} = layout;
  const innerWidth=Math.max(1,size.width-24),innerHeight=Math.max(1,size.height-24);
  if (compact && layout.groups && (folded || raw.width*.75>innerWidth)) {
    folded=true;
    const byID = new Map(layout.nodes.map(n=>[n.id,n]));
    const nodes:Box[]=[],capacity=Math.max(1,innerWidth/.85-16);
    let y=0,label:Point|undefined;
    for(const group of layout.groups.filter(group=>group.length)) {
      if(byID.get(group[0])!.uncalled){label={x:0,y:y+12};y+=17;}
      let x=0;
      for(const id of group){const n=byID.get(id)!;if(x&&x+n.width>capacity){x=0;y+=26;}nodes.push({...n,x,y});x+=n.width+6;}
      y+=26;
    }
    const boxes=new Map(nodes.map(n=>[n.id,n]));
    const routes=[...model.edges].sort((a,b)=>order(a.id,b.id)).map(e=>{const s=boxes.get(e.caller)!,t=boxes.get(e.callee)!;return [{x:s.x+s.width/2,y:s.y+s.height},{x:t.x+t.width/2,y:t.y}];});
    raw={nodes,routes,width:Math.max(1,label?252:0,...nodes.map(n=>n.x+n.width))+16,height:Math.max(1,y-6),label};
  }
  const centre=folded ? raw.nodes.filter(n=>n.entry).reduce((sum,n,_,a)=>sum+(n.y+n.height/2)/a.length,0) : layout.centre;
  const nodesByID=new Map(model.nodes.map(n=>[n.id,n])),edges=[...model.edges].sort((a,b)=>order(a.id,b.id));
  // Include routed points and stroke/arrow room, not only card rectangles.
  const points=raw.routes.flat(), minX=Math.min(0,...points.map(p=>p.x)),minY=Math.min(0,...points.map(p=>p.y));
  const width=(points.length ? Math.max(raw.width,...points.map(p=>p.x+8)) : Math.max(1,...raw.nodes.map(n=>n.x+n.width))+8)-minX;
  const totalHeight=Math.max(raw.height,...points.map(p=>p.y))-minY;
  const scale=Math.min(innerWidth/width,Math.max(innerWidth*.78/width,Math.min(1,Math.max(compact?.75:.85,innerHeight/totalHeight))));
  const contentWidth=size.width,contentHeight=Math.max(size.height,totalHeight*scale+24);
  const offsetX=(size.width-width*scale)/2+8*scale,offsetY=(contentHeight-totalHeight*scale)/2;
  const fit=(p:Point)=>({x:offsetX+(p.x-minX)*scale,y:offsetY+(p.y-minY)*scale});
  const positioned=raw.nodes.map(n=>({...n,...nodesByID.get(n.id),...fit(n),width:n.width*scale,height:n.height*scale}));
  const entryBoxes=positioned.filter(n=>n.entry),maxScroll=Math.max(0,contentHeight-size.height);
  const lower=Math.max(0,...entryBoxes.map(n=>n.y+n.height-size.height)),upper=Math.min(maxScroll,...entryBoxes.map(n=>n.y));
  const initialScrollY=clamp(fit({x:0,y:centre}).y-size.height/2,lower,upper);
  return {nodes:positioned,edges:edges.map((e,index)=>{
    const points=raw.routes[index].map(fit);
    const path=points.slice(1).reduce((path,p,i)=>{const prev=points[i],mid=(prev.x+p.x)/2;return `${path} C${mid},${prev.y} ${mid},${p.y} ${p.x},${p.y}`;},`M${points[0].x},${points[0].y}`);
    return {...e,path};
  }),scale,compact,folded,initialScrollY,contentWidth,contentHeight,uncalledLabel:raw.label?fit(raw.label):undefined};
}

export const nodeMetrics = (n: ServiceNode) => `${formatValue("per_second", n.request_rate)} · ${formatValue("percent", n.error_rate)} err · ${formatValue("ms", n.p95_ms)} p95`;
const shortNumber = (value: number | null): string => {
  if (value === null || !Number.isFinite(value)) return "—";
  const bounded = clamp(value, 0, 999000);
  if (bounded > 0 && bounded < .01) return "<0.01";
  if (bounded >= 1000) return `${Math.min(999, Math.round(bounded / 1000))}K`;
  return String(Number(bounded.toFixed(bounded < 1 ? 2 : bounded < 10 ? 1 : 0)));
};
const shortError = (value: number | null): string => value === null || !Number.isFinite(value) ? "— err" : `${Number((value > 0 ? clamp(value, .1, 100) : 0).toFixed(1))}% err`;
const shortDuration = (value: number | null) => value === null ? "—" : Number(value.toPrecision(2)) >= 1000 ? `${shortNumber(value / 1000)}s` : `${shortNumber(value)}ms`;

const textMeasure: NonNullable<ChartSize["measureText"]> = (text,font)=>Array.from(text).length*Number(font.match(/([\d.]+)px/)?.[1]??11)*.6;
const protectedName=(name:string)=>Array.from(name).length>24?Array.from(name).slice(0,24).join("")+"…":name;

/** Keep the identity; optional metrics consume only its remaining space. */
export function serviceCardLabels(n: ServiceNode, {width,scale,compact,measureText}: {width:number;scale:number;compact:boolean;measureText?:ChartSize["measureText"]}) {
  const nameSize=Math.max(12/Math.max(1,scale),Math.ceil(1100/scale)/100),metricSize=Math.ceil(1100/scale)/100;
  const measure=measureText??textMeasure,nameFont=`600 ${nameSize}px ${fonts.display}`,metricFont=`${metricSize}px ${fonts.display}`;
  const name=protectedName(n.id),available=Math.max(0,width-14),glyph=measure(healthGlyph[n.health]??"○",nameFont);
  const rate=`${shortNumber(n.request_rate)}/s`,error=shortError(n.error_rate);
  const key=n.error_rate !== null && n.error_rate > 0 ? error : rate,pair=`${rate} · ${error}`,all=`${pair} · p95 ${shortDuration(n.p95_ms)}`;
  const metric=compact?(measure(name,nameFont)+glyph+8+measure(key,metricFont)<=available?key:""):measure(all,metricFont)<=available?all:measure(pair,metricFont)<=available?pair:key;
  const metricWidth=compact?measure(metric,metricFont):available;
  const nameWidth=Math.max(0,available-glyph-4-(compact&&metric?metricWidth+4:0));
  return {name,metric,nameSize,metricSize,nameWidth};
}

// Per measurement context (and thus font load generation), reserve the widest
// bounded card format instead of measuring a live value. The widest error
// label is nine monospace characters; rates are capped at 999K/s.
const slotWidths = new WeakMap<NonNullable<ChartSize["measureText"]>, Map<string, number>>();
function fixedSlotWidth(measure: NonNullable<ChartSize["measureText"]>, font: string, metric: boolean) {
  let cache = slotWidths.get(measure);
  if (!cache) { cache = new Map(); slotWidths.set(measure, cache); }
  const key = `${metric ? "metric" : "health"}:${font}`;
  let width = cache.get(key);
  if (width === undefined) {
    const templates = metric ? Array.from({length:10}, (_, digit) => [
      `${digit}${digit}${digit}K/s`, `<0.0${digit}/s`, `${digit}${digit}.${digit}% err`, "100% err",
    ]).flat() : Object.values(healthGlyph);
    width = Math.max(...templates.map(text => measure(text, font)));
    cache.set(key, width);
  }
  return width;
}
function serviceCardWidth(n:ServiceNode,compact:boolean,measureText?:ChartSize["measureText"],metrics=true) {
  // Size for the floor's compensated fonts, so later scale selection cannot
  // turn a protected name into clipped text. Health and metrics have fixed slots.
  const scale=compact?.75:.85, nameSize=Math.max(12/Math.max(1,scale),Math.ceil(1100/scale)/100), metricSize=Math.ceil(1100/scale)/100;
  const measure=measureText??textMeasure, font=`600 ${nameSize}px ${fonts.display}`;
  const name=measure(protectedName(n.id),font)+fixedSlotWidth(measure,font,false)+18;
  const withMetric=name+4+fixedSlotWidth(measure,`${metricSize}px ${fonts.display}`,true);
  // Allow a fourteen-character identity beside the fixed nine-character slot.
  return compact?(metrics&&withMetric<=240?Math.ceil(withMetric):Math.ceil(name)):Math.ceil(Math.max(168,name));
}
