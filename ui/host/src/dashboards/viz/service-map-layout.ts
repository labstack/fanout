import { graphlib, layout } from "@dagrejs/dagre";
import type { ChartSize } from "../../../../panels/compile";
import { healthGlyph, type ServiceGraph, type ServiceNode } from "../../../../panels/rollups";
import { formatValue } from "../../../../panels/units";
import { fonts, typeScale } from "../../../../tokens";
const order = (a: string, b: string) => a < b ? -1 : a > b ? 1 : 0;
const clamp = (n: number, min: number, max: number) => Math.min(max, Math.max(min, n));
const compactHeight = 20, fullHeight = 44;
// A compact card must hold micro text plus its two one-pixel borders.
const compactReadableScale = (typeScale.micro + 2) / compactHeight;
export type Point = { x: number; y: number };
export type CardWidths = Record<string, {full:number;compact:number}>;
export type Box = ServiceNode & Point & { width: number; height: number; entry: boolean; uncalled: boolean };
type CardLayout = { nodes: Box[]; routes: Point[][]; width: number; height: number; label?: Point; labelWidth: number };
export type MapLayout = CardLayout & { full: CardLayout };
export function serviceMapStructure(model: ServiceGraph, measureText?: ChartSize["measureText"]) {
  const widths: CardWidths = Object.create(null);
  for (const node of model.nodes) widths[node.id] = {full:serviceCardWidth(node,false,measureText),compact:serviceCardWidth(node,true,measureText)};
  const ordered = Object.entries(widths).sort(([a], [b]) => order(a, b));
  const topology = JSON.stringify([ordered.map(([id]) => id), model.edges.map(e => [e.id, e.caller, e.callee]).sort((a,b) => order(a[0],b[0]))]);
  return { key: JSON.stringify([topology, ordered]), topology, widths };
}

/** One deterministic LR layout, independent of viewport size. Dagre owns all routes. */
export function layoutServiceMapRaw(model: ServiceGraph, _size: ChartSize, cardWidths?: CardWidths): MapLayout {
  const widths=cardWidths??serviceMapStructure(model,_size.measureText).widths;
  return {...layoutCards(model,widths,true),full:layoutCards(model,widths,false)};
}
function layoutCards(model:ServiceGraph,cardWidths:CardWidths,compact:boolean):CardLayout {
  const nodes = [...model.nodes].sort((a,b) => order(a.id,b.id)), edges = [...model.edges].sort((a,b) => order(a.id,b.id));
  const connected = new Set(edges.flatMap(e => [e.caller,e.callee]));
  const incoming = new Set(edges.map(e => e.callee)), outgoing = new Set(edges.map(e => e.caller));
  const graph = new graphlib.Graph({ directed: true, multigraph: true });
  graph.setDefaultEdgeLabel(() => ({}));
  const cardHeight=compact?compactHeight:fullHeight;
  graph.setGraph({ rankdir: "LR", ranker: "network-simplex", acyclicer: "greedy", ranksep: compact?16:32, nodesep: compact?4:8, edgesep: compact?2:8, marginx: 0, marginy: 0 });
  const widths = new Map(nodes.map(n => [n.id, cardWidths[n.id][compact?"compact":"full"]]));
  for (const node of nodes) if (connected.has(node.id)) graph.setNode(node.id, { width: widths.get(node.id), height: cardHeight });
  for (const edge of edges) graph.setEdge(edge.caller, edge.callee, { weight: 1, minlen: 1 }, edge.id);
  if (graph.nodeCount()) layout(graph);
  const boxes: Box[] = nodes.filter(n => connected.has(n.id)).map(n => ({ ...n, x: graph.node(n.id).x - widths.get(n.id)! / 2, y: graph.node(n.id).y - cardHeight/2, width: widths.get(n.id)!, height: cardHeight, entry: outgoing.has(n.id) && !incoming.has(n.id), uncalled: false }));
  const routes = edges.map(e => graph.edge(e.caller, e.callee, e.id).points as Point[]);
  const uncalled = nodes.filter(n => !connected.has(n.id));
  const rowWidth = uncalled.reduce((sum,n) => sum + widths.get(n.id)! + 12, 0) - (uncalled.length ? 12 : 0);
  const labelWidth = uncalled.length ? textMeasure("No traced calls in this window", `12px ${fonts.display}`) : 0;
  const width = Math.max(1, graph.graph().width ?? 0, rowWidth, labelWidth);
  let rowX = (width - rowWidth) / 2;
  const graphHeight = Math.max(0, graph.graph().height ?? 0, ...routes.flat().map(p => p.y), ...boxes.map(n => n.y + n.height));
  // The isolated heading has a dedicated lane between routes and isolated cards.
  const rowY = graphHeight + (graphHeight ? 40 : 24);
  for (const node of uncalled) { boxes.push({ ...node, x: rowX, y: rowY, width: widths.get(node.id)!, height: cardHeight, entry: false, uncalled: true }); rowX += widths.get(node.id)! + 12; }
  // Centre connected graph and its separate uncalled row within the same bounds.
  const shift = (width - (graph.graph().width ?? 0)) / 2;
  for (const node of boxes) if (!node.uncalled) node.x += shift;
  for (const route of routes) for (const point of route) point.x += shift;
  const byID = new Map(boxes.map(node => [node.id, node]));
  // Dagre's diagonal attachment to the top/bottom of a variable-width card can
  // cross another card in the target rank. Keep its intermediate routing lanes
  // and approach through the rank gap, attaching to the facing sides instead.
  for (const [index, edge] of edges.entries()) {
    const source = byID.get(edge.caller)!, target = byID.get(edge.callee)!;
    const middle = routes[index].slice(1, -1);
    if (!middle.length || source === target) continue;
    const forward = source.x < target.x;
    const start = { x: source.x + (forward ? source.width : 0), y: source.y + source.height / 2 };
    const end = { x: target.x + (forward ? 0 : target.width), y: target.y + target.height / 2 };
    routes[index] = [start, { x: middle[0].x, y: start.y }, ...middle, { x: middle.at(-1)!.x, y: end.y }, end];
  }
  return { nodes: boxes, routes, width, height: Math.max(1, graphHeight, uncalled.length ? rowY + cardHeight : 0), label: uncalled.length ? { x: (width - labelWidth) / 2, y: rowY - 24 } : undefined, labelWidth };
}

/** Contain the complete graph initially; zoom restores natural text size. */
export function fitServiceMap(layout: MapLayout, model: ServiceGraph, size: ChartSize) {
  const bounds=(raw:CardLayout)=>{
    const points = raw.routes.flat();
    const minX = Math.min(0, ...points.map(p => p.x)), minY = Math.min(0, ...points.map(p => p.y));
    const width = Math.max(raw.width, ...points.map(p => p.x)) - minX;
    const height = Math.max(raw.height, ...points.map(p => p.y)) - minY;
    const graphBottom = Math.max(0, ...raw.routes.flat().map(p => p.y), ...raw.nodes.filter(n=>!n.uncalled).map(n=>n.y+n.height));
    const rowY = raw.nodes.find(n=>n.uncalled)?.y;
    const gap = rowY === undefined ? 0 : rowY - graphBottom;
    const lane = rowY === undefined ? 0 : 32;
    const scale = Math.min(Math.max(1, size.width - 24) / width, Math.max(1, size.height - 24 - lane) / Math.max(1,height-gap));
    return {minX,minY,width,height,scale,graphBottom,rowY,gap,lane};
  };
  const compact=bounds(layout.full).scale<1;
  const raw=compact?layout:layout.full;
  const {minX,minY,width,height,scale:containScale,graphBottom,rowY,gap,lane}=bounds(raw);
  const scale=Math.min(containScale,1);
  const fittedHeight=(height-gap)*scale+lane;
  const contentWidth=size.width,contentHeight=size.height;
  const offsetX = (contentWidth - width * scale) / 2, offsetY = (contentHeight - fittedHeight) / 2;
  const fit = (p: Point) => ({ x: offsetX + (p.x - minX) * scale, y: offsetY + (p.y - minY) * scale + (rowY !== undefined && p.y>=rowY ? lane-gap*scale : 0) });
  const nodesByID = new Map(model.nodes.map(n => [n.id, n]));
  const nodes = raw.nodes.map(n => ({ ...n, ...nodesByID.get(n.id), ...fit(n), width: n.width * scale, height: n.height * scale }));
  const edges = [...model.edges].sort((a,b) => order(a.id,b.id)).map((e,index) => {
    const points = raw.routes[index].map(fit);
    // Straight Dagre segments stay inside their routing lanes; cubic smoothing
    // could cut a corner across a neighbouring box.
    const path = points.map((p,i) => `${i ? "L" : "M"}${p.x},${p.y}`).join(" ");
    return { ...e, points, path };
  });
  // Once contained, the entry and every reachable/isolated node share one view.
  const initialView={x:0,y:0};
  return { nodes, edges, scale, compact, initialView, contentWidth, contentHeight, uncalledLabel: raw.label ? {x:(size.width-raw.labelWidth)/2,y:offsetY+(graphBottom-minY)*scale+8} : undefined };
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

/** Identity owns its line. Compact cards keep all metrics in the tooltip/Data view. */
export function serviceCardLabels(n: ServiceNode, { width, scale, compact=false, measureText }: { width: number; scale: number; compact?:boolean; measureText?: ChartSize["measureText"] }) {
  const nameSize = Math.max(12, typeScale.micro / scale), metricSize = typeScale.micro / scale;
  const measure = measureText ?? textMeasure;
  const available = Math.max(0, width - 12 / scale), glyph = measure(healthGlyph[n.health] ?? "○", `${nameSize}px ${fonts.display}`);
  const name = protectedName(n.id);
  const candidate = n.error_rate !== null && n.error_rate > 0 ? shortError(n.error_rate) : `${shortNumber(n.request_rate)}/s`;
  const pair = `${shortNumber(n.request_rate)}/s · ${shortError(n.error_rate)}`;
  const metric = compact ? "" : [`${pair} · p95 ${shortDuration(n.p95_ms)}`, pair, candidate].find(text => measure(text, `${metricSize}px ${fonts.display}`) <= available) ?? "";
  return { name, metric, nameSize, metricSize, nameWidth: Math.max(0, available - glyph - 4 / scale) };
}
// Widths depend on names and fonts, never on live health/rate values.
const slotWidths = new WeakMap<NonNullable<ChartSize["measureText"]>, Map<string,number>>();
function serviceCardWidth(n: ServiceNode, compact:boolean, measureText?: ChartSize["measureText"]) {
  const measure = measureText ?? textMeasure;
  let slots = slotWidths.get(measure);
  if (!slots) {slots=new Map();slotWidths.set(measure,slots);}
  // Reserve each name at the smallest scale that can contain micro text.
  // Full cards put metrics on a second line, so they never compete with names.
  const nameSize=compact?typeScale.micro/compactReadableScale:12,font=`600 ${nameSize}px ${fonts.display}`;
  let glyph=slots.get(font);
  if(glyph===undefined){glyph=Math.max(...Object.values(healthGlyph).map(g=>measure(g,font)));slots.set(font,glyph);}
  const padding=compact?Math.ceil(16/compactReadableScale)+1:20;
  return Math.ceil(Math.max(compact?0:168,measure(protectedName(n.id),font)+glyph+padding));
}
