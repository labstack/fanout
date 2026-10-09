import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { typeScale } from "../../../tokens";
import { serviceMapModel } from "../../../panels/rollups";
import { demoFrame } from "../../tests/service-map-demo";
import { ServiceMapViz } from "./viz/service-map";
import { fitServiceMap, layoutServiceMapRaw, serviceCardLabels } from "./viz/service-map-layout";

const measure = (text:string,font:string) => Array.from(text).length * Number(font.match(/([\d.]+)px/)![1]) * .62;
const model = serviceMapModel(demoFrame,{from_ms:0,to_ms:3600000});
const graphAt = (width:number,height:number) => fitServiceMap(layoutServiceMapRaw(model,{width,height,measureText:measure}),model,{width,height});
const cleanups:(()=>void)[]=[];
beforeEach(()=>vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT",true));
afterEach(async()=>{await act(async()=>cleanups.splice(0).forEach(fn=>fn()));vi.restoreAllMocks();vi.unstubAllGlobals();document.body.innerHTML="";});
function contained(graph:ReturnType<typeof graphAt>,width:number,height:number) {
  for(const n of graph.nodes) {
    expect(n.x).toBeGreaterThanOrEqual(8);expect(n.y).toBeGreaterThanOrEqual(8);
    expect(n.x+n.width).toBeLessThanOrEqual(width-8);expect(n.y+n.height).toBeLessThanOrEqual(height-8);
    for(const o of graph.nodes) if(o!==n) expect(n.x>=o.x+o.width||o.x>=n.x+n.width||n.y>=o.y+o.height||o.y>=n.y+n.height).toBe(true);
  }
  expect(Math.max((Math.max(...graph.nodes.map(n=>n.x+n.width))-Math.min(...graph.nodes.map(n=>n.x)))/width,(Math.max(...graph.nodes.map(n=>n.y+n.height))-Math.min(...graph.nodes.map(n=>n.y)))/height)).toBeGreaterThanOrEqual(.7);
  expect(graph.contentWidth).toBe(width);expect(graph.contentHeight).toBe(height);
}
it("fits the 20px compact demo at 1100×230 with no clipping or pan",()=>{
  const graph=graphAt(1100,230);expect(graph.compact).toBe(true);expect(graph.nodes).toHaveLength(20);
  contained(graph,1100,230);for(const n of graph.nodes) expect(n.height/graph.scale).toBeCloseTo(20);
});
it("fits all twenty demo services and the isolated heading in a 1100×190 body",()=>{
  const graph=graphAt(1100,190);expect(graph.compact).toBe(true);contained(graph,1100,190);
  expect(graph.uncalledLabel!.y).toBeGreaterThanOrEqual(8);expect(graph.uncalledLabel!.y+typeScale.micro).toBeLessThan(190);
});
it.each([190,220])("protects all names in a twenty-node graph with nine leaf lanes and four uncalled services at height %s",height=>{
  const names=["load-generator","frontend-proxy","frontend","checkout","recommendation","product-reviews","product-catalog","shipping","payment","fraud-detection","email","currency","cart","ad","quote","flagd","accounting","image-provider","kafka","otelcol-contrib"];
  const nodes=names.map(id=>({...model.nodes[0],id}));
  const pairs=[[0,1],[1,2],[2,3],[2,4],...Array.from({length:9},(_,i)=>[3,i+5]),[7,14],[5,15]];
  const edges=pairs.map(([a,b],i)=>({...model.edges[0],id:`e${i}`,caller:names[a],callee:names[b]}));
  const m={nodes,edges},graph=fitServiceMap(layoutServiceMapRaw(m,{width:1100,height,measureText:measure}),m,{width:1100,height});
  contained(graph,1100,height);
  for(const n of graph.nodes) {
    const labelScale=Math.min(1,graph.scale/.65),font=Math.max(typeScale.micro*labelScale,12*graph.scale);
    expect(n.height).toBeGreaterThanOrEqual(font+2*labelScale-.01);
    expect(n.width).toBeGreaterThanOrEqual(measure(n.id+"●",`${font}px monospace`)+16*labelScale);
  }
});
it("drops the compact metric before a protected service name",()=>{
  const n={...model.nodes[0],id:"recommendation",request_rate:1.2,error_rate:3.4};
  const label=serviceCardLabels(n,{width:140,scale:.85,compact:true,measureText:measure});
  expect(label.name).toBe(n.id);expect(label.metric).toBe("");
});
it("protects all 24 characters using per-node measurements",()=>{
  const n={...model.nodes[0],id:"abcdefghijklmnopqrstuvwx"},m={nodes:[n],edges:[]};
  const graph=fitServiceMap(layoutServiceMapRaw(m,{width:1100,height:180,measureText:measure}),m,{width:1100,height:180});
  const box=graph.nodes[0],label=serviceCardLabels(n,{width:box.width/graph.scale,scale:graph.scale,compact:graph.compact,measureText:measure});
  expect(label.name).toBe(n.id);expect(label.nameWidth).toBeGreaterThanOrEqual(measure(n.id,`600 ${label.nameSize}px sans-serif`));
});
it.each([180,190,220,230,280,396,480])("keeps entry services initially visible at body height %s",height=>{
  const graph=graphAt(1100,height);for(const n of graph.nodes.filter(n=>n.entry)){expect(n.y).toBeGreaterThanOrEqual(8);expect(n.y+n.height).toBeLessThanOrEqual(height-8);}
});
it("uses full name-plus-metrics cards only when they fit at scale 1 or greater",()=>{
  const small={nodes:[model.nodes[0]],edges:[]};
  const graph=fitServiceMap(layoutServiceMapRaw(small,{width:1100,height:460,measureText:measure}),small,{width:1100,height:460});
  expect(graph.compact).toBe(false);expect(graph.scale).toBeGreaterThanOrEqual(1);expect(graph.nodes[0].height/graph.scale).toBe(44);
});
it("protects short chat service names before metrics in full cards",async()=>{
  const width=780,height=460,names=["product-reviews","product-catalog","fraud-detection","otelcol-contrib"];
  const columns=["kind","service","health","spans","p95_ms","error_rate"];
  const frame={rows:names.length,columns:columns.map(name=>({name,type:"string" as const,role:"dimension" as const})),values:[names.map(()=>"node"),names,names.map(()=>"healthy"),names.map(()=>1000),names.map(()=>30),names.map(()=>0)]};
  const original=HTMLElement.prototype.getBoundingClientRect;
  vi.spyOn(HTMLElement.prototype,"getBoundingClientRect").mockImplementation(function(this:HTMLElement){return this.hasAttribute("data-service-viewport")?DOMRect.fromRect({width,height}):original.call(this);});
  const context={font:"",measureText(text:string){return {width:measure(text,this.font)};}};
  vi.spyOn(HTMLCanvasElement.prototype,"getContext").mockReturnValue(context as unknown as CanvasRenderingContext2D);
  const host=document.createElement("div");document.body.append(host);const root=createRoot(host);cleanups.push(()=>root.unmount());
  await act(async()=>root.render(<MantineProvider><ServiceMapViz panel={{id:"m",title:"Map",viz:"service_map"}} result={{id:"m",status:"ok",elapsed_ms:0,frame}} dark={false} height={height+24}/></MantineProvider>));
  expect(host.querySelector<HTMLElement>('[data-service-viewport]')!.dataset.cardMode).toBe("full");
  for(const card of host.querySelectorAll<HTMLElement>('[data-service-node]')) {
    const line=card.querySelector<HTMLElement>('[data-service-text]')!,name=card.querySelector<HTMLElement>('[data-service-name]')!;
    expect(name.textContent).toBe(card.dataset.serviceNode);expect(name.style.textOverflow).not.toBe("ellipsis");
    const font=parseFloat(line.style.fontSize);
    expect(measure(name.textContent!+"●",`${font}px monospace`)+16).toBeLessThanOrEqual(parseFloat(card.style.width));
    expect(card.querySelector('[data-service-metric]')?.parentElement).toBe(card);
  }
});
it("uses compact nodesep of 4 logical pixels",()=>{
  const nodes=[{...model.nodes[0],id:"entry"},...Array.from({length:8},(_,i)=>({...model.nodes[0],id:`callee-${i}`}))];
  const m={nodes,edges:nodes.slice(1).map(n=>({...model.edges[0],id:n.id,caller:"entry",callee:n.id}))};
  const graph=fitServiceMap(layoutServiceMapRaw(m,{width:1100,height:230}),m,{width:1100,height:230});
  const rank=graph.nodes.filter(n=>!n.entry).sort((a,b)=>a.y-b.y);expect(graph.compact).toBe(true);
  for(let i=1;i<rank.length;i++) expect((rank[i].y-rank[i-1].y-rank[i-1].height)/graph.scale).toBeCloseTo(4);
});
it("contains separated entries and all leaves even below the natural text scale",()=>{
  const nodes=[] as typeof model.nodes,edges=[] as typeof model.edges;
  for(let root=0;root<2;root++) {
    const id=`entry-${root}`;nodes.push({...model.nodes[0],id});
    for(let i=0;i<20;i++){const child=`${id}-callee-${i}`;nodes.push({...model.nodes[0],id:child});edges.push({...model.edges[0],id:child,caller:id,callee:child});}
  }
  const m={nodes,edges},graph=fitServiceMap(layoutServiceMapRaw(m,{width:1100,height:180}),m,{width:1100,height:180});
  expect(graph.scale).toBeLessThan(.65);expect(graph.contentHeight).toBe(180);
  const entries=graph.nodes.filter(n=>n.entry);expect(entries.some(n=>n.y+graph.initialView.y>=8 && n.y+n.height+graph.initialView.y<=172)).toBe(true);
  for(const n of graph.nodes) {expect(n.height).toBeCloseTo(20*graph.scale);for(const o of graph.nodes) if(o!==n) expect(n.x>=o.x+o.width||o.x>=n.x+n.width||n.y>=o.y+o.height||o.y>=n.y+n.height).toBe(true);}
});
it("keeps an entry in a distant Dagre rank inside the initial viewport",()=>{
  const nodes=Array.from({length:12},(_,i)=>({...model.nodes[0],id:`chain-${i}`}));nodes.push({...model.nodes[0],id:"late-entry"});
  const edges=nodes.slice(1,12).map((n,i)=>({...model.edges[0],id:n.id,caller:`chain-${i}`,callee:n.id}));edges.push({...model.edges[0],id:"late-hop",caller:"late-entry",callee:"chain-11"});
  const m={nodes,edges},graph=fitServiceMap(layoutServiceMapRaw(m,{width:1100,height:180}),m,{width:1100,height:180});
  contained(graph,1100,180);for(const n of graph.nodes.filter(n=>n.entry)) expect(n.x+n.width).toBeLessThanOrEqual(1092);
});
for(const [width,height] of [[1100,220],[1100,190],[1600,700],[1440,480]]) for(const dark of [false,true]) it(`keeps measured compact/full card content inside its lanes at ${width}×${height}, dark=${dark}`,async()=>{
  const original=HTMLElement.prototype.getBoundingClientRect;
  vi.spyOn(HTMLElement.prototype,"getBoundingClientRect").mockImplementation(function(this:HTMLElement){return this.hasAttribute("data-service-viewport")?DOMRect.fromRect({width,height}):original.call(this);});
  const context={font:"",measureText(text:string){return {width:measure(text,this.font)};}};
  vi.spyOn(HTMLCanvasElement.prototype,"getContext").mockReturnValue(context as unknown as CanvasRenderingContext2D);
  const host=document.createElement("div");document.body.append(host);const root=createRoot(host);cleanups.push(()=>root.unmount());
  await act(async()=>root.render(<MantineProvider forceColorScheme={dark?"dark":"light"}><ServiceMapViz panel={{id:"m",title:"Map",viz:"service_map"}} result={{id:"m",status:"ok",elapsed_ms:1,frame:demoFrame}} dark={dark} height={height+24}/></MantineProvider>));
  const viewport=host.querySelector<HTMLElement>('[data-service-viewport]')!;
  expect(host.querySelectorAll('[data-service-node]')).toHaveLength(20);
  for(const card of host.querySelectorAll<HTMLElement>('[data-service-node]')) {
    const name=card.querySelector<HTMLElement>('[data-service-name]')!,line=card.querySelector<HTMLElement>('[data-service-text]')!;
    expect(name.textContent).toBe(card.dataset.serviceNode);expect(name.textContent).not.toContain("…");
    expect(line.style.visibility).not.toBe("hidden");expect(getComputedStyle(line).visibility).not.toBe("hidden");
    const fontSize=parseFloat(getComputedStyle(name).fontSize)||parseFloat(getComputedStyle(line).fontSize);
    expect(fontSize).toBeGreaterThanOrEqual(typeScale.micro);
    const nameWidth=measure(name.textContent!,`600 ${fontSize}px sans-serif`);
    const healthWidth=measure(line.firstElementChild!.textContent!,`${fontSize}px sans-serif`);
    expect(nameWidth+healthWidth+14).toBeLessThanOrEqual(parseFloat(card.style.width)+.01);
    expect(fontSize+2).toBeLessThanOrEqual(parseFloat(card.style.height)+.01);
    expect(card.title).toContain("p95");expect(card.title).toContain("err");
    if(viewport.dataset.cardMode==="compact") expect(card.querySelector('[data-service-metric]')).toBeNull();
    else {const metric=card.querySelector<HTMLElement>('[data-service-metric]')!;expect(metric).not.toBeNull();expect(measure(metric.textContent!,`${parseFloat(metric.style.fontSize)}px sans-serif`)+10).toBeLessThanOrEqual(parseFloat(card.style.width)+.01);}
  }
});
