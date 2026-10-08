import { MantineProvider } from "@mantine/core";
import { act, Profiler } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { serviceMapModel } from "../../../panels/rollups";
import type { Frame } from "../../../panels/types";
import { ServiceMapViz } from "./viz/service-map";
import { fitServiceMap, layoutServiceMapRaw, serviceCardLabels } from "./viz/service-map-layout";
import { PanelCard } from "./panel-card";
import { chartThemeFor } from "../../../panels/compile";
import { makeDrill } from "./drill-state";
vi.mock("./echart-canvas", () => ({ EChartCanvas: ({ label }: { label: string }) => <div role="img" aria-label={label} /> }));

// OpenTelemetry demo topology: twenty services, twenty-three directed routes.
const names = ["frontend-proxy", "frontend", "load-generator", "cart", "checkout", "payment", "shipping", "quote", "currency", "product-catalog", "recommendation", "ad", "email", "accounting", "fraud-detection", "kafka", "cart-cache", "checkout-db", "image-provider", "otelcol-contrib"];
const routes = [[2,0],[0,1],[1,3],[1,4],[1,8],[1,9],[1,10],[1,11],[4,3],[4,5],[4,6],[4,8],[4,9],[4,12],[4,17],[6,7],[10,9],[3,16],[5,15],[4,15],[15,13],[15,14],[2,9]];
const columns = ["kind", "service", "caller", "callee", "edge_type", "calls", "average_ms", "error_rate", "health", "p95_ms", "spans"];
const rows = [...names.map((service, i) => ["node", service, "", "", "", null, null, i === 4 ? 7 : 0, i === 4 ? "unhealthy" : "healthy", 30, 1000]), ...routes.map(([a,b], i) => ["edge", "", names[a], names[b], "call", (i+1)*100, 20, i === 0 ? 5 : i === 1 ? 1 : 0, "", null, null])];
export const demoFrame: Frame = { rows: rows.length, columns: columns.map(name => ({ name, type: "string", role: "dimension" })), values: columns.map((_, i) => rows.map(row => row[i] as string | number | null)) };
// Exercise the same raw-layout and fit boundaries used by the component/worker.
const layoutServiceMap = (model: Parameters<typeof layoutServiceMapRaw>[0], size: Parameters<typeof layoutServiceMapRaw>[1]) => fitServiceMap(layoutServiceMapRaw(model, size), model, size);
const cleanups: (() => void)[] = [];
beforeEach(() => vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true));
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(fn => fn())); vi.restoreAllMocks(); document.body.innerHTML = ""; });
const model = () => serviceMapModel(demoFrame, { from_ms: 0, to_ms: 3600000 });
it("G4 fits all twenty demo services and the isolated heading in a 1100×190 body",()=>{
 const graph=layoutServiceMap(model(),{width:1100,height:190});
 expect(graph.compact).toBe(true);expect(graph.scale).toBeGreaterThanOrEqual(.75);expect(graph.contentHeight).toBe(190);
 expect(graph.nodes).toHaveLength(20);
 for(const n of graph.nodes){expect(n.height/graph.scale).toBe(20);expect(n.x).toBeGreaterThanOrEqual(0);expect(n.y).toBeGreaterThanOrEqual(0);expect(n.x+n.width).toBeLessThanOrEqual(1100);expect(n.y+n.height).toBeLessThanOrEqual(190);}
 expect(new Set(graph.nodes.filter(n=>n.uncalled).map(n=>n.y)).size).toBe(1);
 expect(graph.uncalledLabel!.y-12*graph.scale).toBeGreaterThanOrEqual(0);
});
it("G4 compact card content and isolated heading stay within their 20px/17px lanes",async()=>{
 const original=HTMLElement.prototype.getBoundingClientRect;
 vi.spyOn(HTMLElement.prototype,"getBoundingClientRect").mockImplementation(function(this:HTMLElement){return this.hasAttribute("data-service-viewport")?DOMRect.fromRect({width:1100,height:190}):original.call(this);});
 const host=document.createElement("div");document.body.append(host);const root=createRoot(host);cleanups.push(()=>root.unmount());
 await act(async()=>root.render(<MantineProvider><ServiceMapViz panel={{id:"m",title:"Map",viz:"service_map"}} result={{id:"m",status:"ok",elapsed_ms:1,frame:demoFrame}} dark={false} height={214}/></MantineProvider>));
 const card=host.querySelector<HTMLElement>('[data-service-node] > span')!;
 expect(card.style.height).toBe("20px");expect(card.style.paddingTop).toBe("1px");
 expect([...host.querySelectorAll<HTMLElement>('span')].find(el=>el.textContent==='No traced calls in this window')!.style.lineHeight).toBe("1");
});
describe("Part 9 C1 scroll event lifetime and feedback", () => {
  const tallFrame: Frame = {
    ...demoFrame,
    rows: demoFrame.rows + 40,
    values: demoFrame.values.map((values, column) => [...values, ...Array.from({ length: 40 }, (_, i) =>
      ["node", `isolated-${i}`, "", "", "", null, null, 0, "healthy", 30, 1000][column] as string | number | null)]),
  };
  async function renderTall() {
    const original = HTMLElement.prototype.getBoundingClientRect;
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function(this: HTMLElement) {
      return this.hasAttribute("data-service-viewport") ? DOMRect.fromRect({ width: 1100, height: 180 }) : original.call(this);
    });
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host); cleanups.push(() => root.unmount());
    const commits = vi.fn(), mapView = vi.fn();
    await act(async () => root.render(<MantineProvider><Profiler id="map" onRender={commits}><ServiceMapViz
      panel={{ id: "m", title: "Map", viz: "service_map" }}
      result={{ id: "m", status: "ok", elapsed_ms: 1, frame: tallFrame }} dark={false} height={204} onMapView={mapView}
    /></Profiler></MantineProvider>));
    const viewport = host.querySelector<HTMLElement>("[data-service-viewport]")!;
    expect(Number(viewport.dataset.contentHeight)).toBeGreaterThan(180);
    commits.mockClear();
    return { viewport, commits, fit: mapView.mock.lastCall![0].fit as () => void };
  }
  it("snapshots user scroll positions before queued updaters outlive the event", async () => {
    const { viewport } = await renderTall();
    await act(async () => {
      // Two events before React flushes force the second updater to run after
      // React clears SyntheticEvent.currentTarget, rather than only eagerly.
      for (const top of [45, 90]) {
        viewport.scrollTop = top;
        viewport.dispatchEvent(new Event("scroll", { bubbles: true }));
      }
    });
    expect(viewport.scrollTop).toBe(90);
    expect(viewport.scrollLeft).toBe(0);
  });
  it("ignores programmatic scroll echoes and repeated equal positions", async () => {
    const { viewport, commits, fit } = await renderTall();
    await act(async () => fit());
    await act(async () => viewport.dispatchEvent(new Event("scroll", { bubbles: true })));
    expect(commits.mock.calls.length).toBeLessThanOrEqual(1);
    commits.mockClear();
    await act(async () => {
      viewport.dispatchEvent(new Event("scroll", { bubbles: true }));
      viewport.dispatchEvent(new WheelEvent("wheel", { deltaY: 0, bubbles: true, cancelable: true }));
    });
    expect(commits).not.toHaveBeenCalled();
  });
  it("keeps a queued native scroll followed by Fit at the fitted position", async () => {
    const { viewport, fit } = await renderTall();
    const initial = viewport.scrollTop;
    await act(async () => {
      viewport.scrollTop = 45;
      viewport.dispatchEvent(new Event("scroll", { bubbles: true }));
      fit();
    });
    expect(viewport.scrollTop).toBe(initial);
  });
});
describe("preview V10", () => {
  it("G4 keeps the default demo readable and fits the saved short body", () => {
    const normal = layoutServiceMap(model(), {width:1100,height:480});
    expect(normal.scale).toBeGreaterThanOrEqual(.85);
    expect(Math.max(...normal.nodes.map(n=>n.x+n.width))-Math.min(...normal.nodes.map(n=>n.x))).toBeGreaterThanOrEqual(1050);
    for(const n of normal.nodes) expect(n.y+n.height).toBeLessThanOrEqual(468.01);
    const short = layoutServiceMap(model(), {width:1100,height:180});
    expect(short.scale).toBeGreaterThanOrEqual(.75);
    expect(short.contentHeight).toBeLessThanOrEqual(190);
    const defaultBody=layoutServiceMap(model(),{width:1100,height:396});
    expect(defaultBody.contentHeight).toBe(396);
  });
  it.each([false,true])("P3a keeps rendered text at 11px and scrolls vertically (%s)", async dark => {
    const host=document.createElement("div");document.body.append(host);const root=createRoot(host);cleanups.push(()=>root.unmount());
    await act(async()=>root.render(<MantineProvider forceColorScheme={dark?"dark":"light"}><PanelCard panel={{id:"map",title:"Map",viz:"service_map"}} title="Map" result={{id:"map",status:"ok",elapsed_ms:1,frame:demoFrame}} height={300} group="g" editing={false} agentAvailable={false} loading={false} onView={vi.fn()} onCopyLink={vi.fn()} onExplain={vi.fn()}/></MantineProvider>));
    const viewport=host.querySelector<HTMLElement>("[data-service-viewport]")!;
    expect(viewport.style.overflowY).toBe("auto");expect(viewport.style.overflowX).toBe("hidden");
    const scale=Number(viewport.dataset.layoutScale);
    const texts=host.querySelectorAll<HTMLElement>("[data-service-text]");expect(texts.length).toBe(viewport.dataset.cardMode === "compact" ? 20 : 40);
    for(const text of texts) expect(parseFloat(text.style.fontSize)*scale).toBeGreaterThanOrEqual(11);
    await act(async()=>{ viewport.scrollTop=50; viewport.dispatchEvent(new Event("scroll",{bubbles:true})); });
    expect(viewport.scrollTop).toBeGreaterThan(0);
    expect(host.querySelector("[data-service-overflow-fade]")).not.toBeNull();
    const fit=host.querySelector<HTMLButtonElement>('[aria-label="Fit Map graph"]')!;expect(fit).not.toBeNull();
    await act(async()=>fit.click());expect(viewport.scrollTop).toBe(Number(viewport.dataset.initialScrollY));
  });
  it.each([{ width: 1100, height: 280 }, { width: 780, height: 220 }, { width: 270, height: 180 }])("keeps twenty readable node boxes in scrollable bounds without overlap %o", size => {
    const graph = model(); const got = layoutServiceMap(graph, size);
    expect(got.nodes).toHaveLength(20); expect(got.edges).toHaveLength(23);
    expect(layoutServiceMap({ nodes: [...graph.nodes].reverse(), edges: [...graph.edges].reverse() }, size)).toEqual(got);
    for (const n of got.nodes) {
      expect(n.x).toBeGreaterThanOrEqual(12); expect(n.y).toBeGreaterThanOrEqual(12);
      expect(n.x+n.width).toBeLessThanOrEqual(got.contentWidth-12+.01); expect(n.y+n.height).toBeLessThanOrEqual(got.contentHeight-12+.01);
      for (const b of got.nodes.filter(b => b.id !== n.id)) expect(n.x < b.x+b.width && n.x+n.width > b.x && n.y < b.y+b.height && n.y+n.height > b.y).toBe(false);
    }
    const entry = graph.nodes.filter(n => graph.edges.some(e => e.caller === n.id) && !graph.edges.some(e => e.callee === n.id));
    for (const n of entry) for (const e of graph.edges.filter(e => e.caller === n.id)) {const source=got.nodes.find(x=>x.id===n.id)!,target=got.nodes.find(x=>x.id===e.callee)!;expect(got.folded?source.y<target.y:source.x<target.x).toBe(true);}
    for (const edge of got.edges) expect(edge.path).toMatch(/^M.*C/);
    expect(got.nodes.filter(n => n.uncalled).map(n => n.id)).toEqual(["image-provider", "otelcol-contrib"]);
    expect(got.uncalledLabel).toBeDefined();
    expect(graph.edges.find(e => e.error_rate === 5)?.status).toBe("bad"); expect(graph.edges.find(e => e.error_rate === 1)?.status).toBe("warn");
    expect(graph.nodes[0].request_rate).toBeCloseTo(1000/3600);
  });
  it.each([false, true])("dims non-neighbours, supports keyboard/filter and bounded vertical pan/fit (%s)", async dark => {
    const host = document.createElement("div"); document.body.append(host); const root = createRoot(host); cleanups.push(() => root.unmount());
    const select = vi.fn(), point = vi.fn();
    await act(async () => root.render(<MantineProvider forceColorScheme={dark ? "dark" : "light"}><PanelCard panel={{ id: "map", title: "Map", viz: "service_map", click: { set_variable: "service" } }} title="Map" result={{ id: "map", elapsed_ms: 1, status: "ok", from_ms: 0, to_ms: 3600000, frame: demoFrame }} height={300} group="g" editing={false} agentAvailable={false} loading={false} onView={vi.fn()} onCopyLink={vi.fn()} onExplain={vi.fn()} onSelect={select} onPoint={point} /></MantineProvider>));
    const button = (name: string) => host.querySelector<HTMLElement>(`[data-service-node="${name}"]`)!;
    await act(async () => button("frontend-proxy").dispatchEvent(new MouseEvent("mouseover", { bubbles: true })));
    expect(button("frontend-proxy").style.opacity).toBe("1"); expect(button("frontend").style.opacity).toBe("1"); expect(button("payment").style.opacity).toBe("0.25");
    expect(host.querySelector('svg > g > path')?.getAttribute("marker-end")).toBeTruthy();
    const theme = chartThemeFor(dark);
    expect(button("frontend").style.border).toContain(theme.border);
    expect(button("checkout").style.border).toContain(theme.status.bad);
    for (const edge of host.querySelectorAll('svg > g > path')) {
      expect(Number(edge.getAttribute("stroke-width"))).toBeGreaterThanOrEqual(1);
      expect(Number(edge.getAttribute("stroke-width"))).toBeLessThanOrEqual(4);
      expect(edge.querySelector("title")?.textContent).toContain("calls");
      expect(edge.querySelector("text")).toBeNull();
    }
    await act(async () => button("frontend").focus()); expect(document.activeElement).toBe(button("frontend"));
    expect(button("frontend").style.outline).toContain("2px");
    await act(async () => button("frontend").dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));
    expect(select).toHaveBeenCalledWith("frontend"); expect(point).not.toHaveBeenCalled();
    const viewport = host.querySelector('[data-service-viewport]')!;
    await act(async () => {
      viewport.dispatchEvent(new PointerEvent("pointerdown", { button: 0, clientX: 100, clientY: 80, bubbles: true }));
      viewport.dispatchEvent(new PointerEvent("pointermove", { clientX: -10000, clientY: -10000, bubbles: true }));
      viewport.dispatchEvent(new PointerEvent("pointerup", { bubbles: true }));
    });
    expect((viewport as HTMLElement).scrollLeft).toBe(0);
    expect((viewport as HTMLElement).scrollTop).toBeCloseTo(parseFloat(viewport.querySelector<HTMLElement>("[data-service-content]")!.style.height)-188);
    const fit = host.querySelector<HTMLButtonElement>('[aria-label="Fit Map graph"]')!; expect(fit).not.toBeNull(); expect(viewport.contains(fit)).toBe(false);
    await act(async () => fit.click());
    expect(host.textContent).toContain("No traced calls in this window"); expect(host.textContent).toContain("20 services · 23 routes");
  });
  it("drills without a variable and renders untrusted service names as text", async () => {
    const host = document.createElement("div"); document.body.append(host); const root = createRoot(host); cleanups.push(() => root.unmount()); const point = vi.fn(); const attack = '<img src=x onerror=alert(1)>';
    const frame = { ...demoFrame, values: demoFrame.values.map(values => values.map(v => v === "frontend" ? attack : v)) };
    await act(async () => root.render(<MantineProvider><ServiceMapViz panel={{ id: "m", title: "Map", viz: "service_map" }} result={{ id: "m", status: "ok", elapsed_ms: 1, frame }} dark={false} height={200} onPoint={point} /></MantineProvider>));
    const button = [...host.querySelectorAll<HTMLButtonElement>('[data-service-node]')].find(n => n.dataset.serviceNode === attack)!;
    expect(host.querySelector("img")).toBeNull(); expect(button.title).toContain(attack);
    await act(async () => button.click()); expect(point).toHaveBeenCalledWith({ dimensions: { service: attack } });
    expect(makeDrill({ id: "m", title: "Map", viz: "service_map" }, { id: "m", status: "ok", elapsed_ms: 1, from_ms: 0, to_ms: 3600000 }, point.mock.lastCall![0])).toMatchObject({ kind: "traces", dimensions: { service: attack } });
  });
});

describe("Part 5 R1",()=>{
 it("fits the twenty-service demo in compact cards at 1100×230 without pan",()=>{
  const graph=layoutServiceMap(model(),{width:1100,height:230});
  expect(graph).toMatchObject({compact:true,contentHeight:230,contentWidth:1100,initialScrollY:0});
  expect(graph.nodes).toHaveLength(20);expect(graph.edges).toHaveLength(23);
  for(const node of graph.nodes){expect(node.height).toBeLessThanOrEqual(20);expect(node.height).toBeGreaterThanOrEqual(20*.75);expect(node.y+node.height).toBeLessThanOrEqual(230);}
 });
 it.each([180,181,200,230,280,396,480])("keeps entry services initially visible at body height %s",height=>{
  const graph=layoutServiceMap(model(),{width:1100,height});
  const entries=model().nodes.filter(n=>model().edges.some(e=>e.caller===n.id)&&!model().edges.some(e=>e.callee===n.id));
  for(const entry of entries){const n=graph.nodes.find(n=>n.id===entry.id)!;expect(n.y-graph.initialScrollY).toBeGreaterThanOrEqual(0);expect(n.y+n.height-graph.initialScrollY).toBeLessThanOrEqual(height);}
 });
 it("centres an overflowing fanout on the entry and main callee instead of the top",()=>{
  const graph=model();
  for(let i=0;i<35;i++){const id=`dependency-${i}`;graph.nodes.push({...graph.nodes[0],id});graph.edges.push({id,caller:"frontend",callee:id,edge_type:"call",calls:1,request_rate:1,error_rate:0,average_ms:1,status:null});}
  const layout=layoutServiceMap(graph,{width:1100,height:180});
  expect(layout.compact).toBe(true);expect(layout.scale).toBe(.75);expect(layout.contentHeight).toBeGreaterThan(180);expect(layout.initialScrollY).toBeGreaterThan(0);
  const entry=layout.nodes.find(n=>n.id==="load-generator")!;
  expect(entry.y-layout.initialScrollY).toBeGreaterThanOrEqual(0);expect(entry.y+entry.height-layout.initialScrollY).toBeLessThanOrEqual(180);
  expect(Math.abs(entry.y+entry.height/2-layout.initialScrollY-90)).toBeLessThanOrEqual(90-entry.height/2);
 });
 it.each([false,true])("compact DOM cards expose full metrics and Fit restores the entry-centred view (%s)",async dark=>{
  const host=document.createElement("div");document.body.append(host);const root=createRoot(host);cleanups.push(()=>root.unmount());
  const select=vi.fn();
  await act(async()=>root.render(<MantineProvider forceColorScheme={dark?"dark":"light"}><PanelCard panel={{id:"map",title:"Map",viz:"service_map",click:{set_variable:"service"}}} title="Map" result={{id:"map",status:"ok",elapsed_ms:1,frame:demoFrame,from_ms:0,to_ms:3600000}} height={300} group="g" editing={false} agentAvailable={false} loading={false} onView={vi.fn()} onCopyLink={vi.fn()} onExplain={vi.fn()} onSelect={select}/></MantineProvider>));
  const viewport=host.querySelector<HTMLElement>("[data-service-viewport]")!;
  expect(viewport.dataset.cardMode).toBe("compact");
  const entry=host.querySelector<HTMLButtonElement>('[data-service-node="load-generator"]')!;
  expect(entry.dataset.serviceEntry).toBe("true");expect(entry.title).toContain("p95");expect(entry.title).toContain("err");
  expect(entry.querySelectorAll('[data-service-text]')).toHaveLength(1);
  expect(host.querySelector<HTMLButtonElement>('[data-service-node="checkout"]')!.title).toContain("err");
  expect(entry.querySelector('[data-service-name]')?.textContent).toBe("load-generator");
  const initial=viewport.scrollTop;
  await act(async()=>viewport.dispatchEvent(new WheelEvent("wheel",{deltaY:1000,bubbles:true,cancelable:true})));
  await act(async()=>host.querySelector<HTMLButtonElement>('[aria-label="Fit Map graph"]')!.click());
  expect(viewport.scrollTop).toBe(initial);
  await act(async()=>entry.dispatchEvent(new KeyboardEvent("keydown",{key:"Enter",bubbles:true})));
  expect(select).toHaveBeenCalledWith("load-generator");
 });
});

it("R1 keeps multiple widely separated entry services initially visible",()=>{
 const graph={nodes:[],edges:[]} as ReturnType<typeof model>;
 for(let root=0;root<2;root++){
  const id=`entry-${root}`;graph.nodes.push({...model().nodes[0],id});
  for(let i=0;i<20;i++){const child=`${id}-callee-${i}`;graph.nodes.push({...model().nodes[0],id:child});graph.edges.push({id:child,caller:id,callee:child,edge_type:"call",calls:20-i,request_rate:20-i,error_rate:0,average_ms:1,status:null});}
 }
 const got=layoutServiceMap(graph,{width:1100,height:180});
 for(const n of got.nodes.filter(n=>n.entry)){expect(n.y-got.initialScrollY).toBeGreaterThanOrEqual(0);expect(n.y+n.height-got.initialScrollY).toBeLessThanOrEqual(180);}
 for(const n of got.nodes)for(const other of got.nodes.filter(o=>o.id!==n.id))expect(n.x<other.x+other.width&&n.x+n.width>other.x&&n.y<other.y+other.height&&n.y+n.height>other.y).toBe(false);
});

describe("Part 5 R2",()=>{
 const measure=(text:string,font:string)=>text.length*parseFloat(font.match(/[\d.]+px/)![0])*.62;
 it("bounds compact numbers and includes p95 only when measured text fits",()=>{
  const n={...model().nodes[0],id:"cart",request_rate:.201,error_rate:.701,p95_ms:48};
  const short=serviceCardLabels(n,{width:168,scale:1,compact:false,measureText:measure});
  expect(short.metric).toBe("0.2/s · 0.7% err");
  expect(serviceCardLabels(n,{width:300,scale:1,compact:false,measureText:measure}).metric).toBe("0.2/s · 0.7% err · p95 48ms");
  expect(serviceCardLabels({...n,error_rate:.715},{width:168,scale:1,compact:false,measureText:measure}).metric).toContain("0.7% err");
  expect(serviceCardLabels(n,{width:140,scale:.85,compact:true,measureText:measure}).metric).toBe("0.7% err");
  expect(serviceCardLabels({...n,error_rate:0},{width:140,scale:.85,compact:true,measureText:measure}).metric).toBe("0.2/s");
 });
 it.each([1100,1440])("uses wider full cards when panel width %s allows",width=>{
  const graph=layoutServiceMap(model(),{width,height:480});expect(graph.contentWidth).toBe(width);
  if(width===1440){expect(graph.compact).toBe(false);for(const n of graph.nodes)expect(n.width/graph.scale).toBeCloseTo(168);}else expect(graph.compact).toBe(true);
 });
 for(const width of [1100,1440])for(const height of [230,480])for(const dark of [false,true]){
  it(`measures every DOM card's name and metric budget at ${width}×${height}, dark=${dark}`,async()=>{
   const original=HTMLElement.prototype.getBoundingClientRect;
   vi.spyOn(HTMLElement.prototype,"getBoundingClientRect").mockImplementation(function(this:HTMLElement){return this.hasAttribute("data-service-viewport")?DOMRect.fromRect({width,height}):original.call(this);});
   const calls:{text:string;font:string}[]=[];
   const context={font:"",measureText(text:string){calls.push({text,font:this.font});return {width:measure(text,this.font)};}};
   vi.spyOn(HTMLCanvasElement.prototype,"getContext").mockReturnValue(context as unknown as CanvasRenderingContext2D);
   const host=document.createElement("div");document.body.append(host);const root=createRoot(host);cleanups.push(()=>root.unmount());
   await act(async()=>root.render(<MantineProvider forceColorScheme={dark?"dark":"light"}><ServiceMapViz panel={{id:"m",title:"Map",viz:"service_map"}} result={{id:"m",status:"ok",elapsed_ms:1,frame:demoFrame,from_ms:0,to_ms:3600000}} dark={dark} height={height+24}/></MantineProvider>));
   expect(calls.length).toBeGreaterThan(0);
   const viewport=host.querySelector<HTMLElement>("[data-service-viewport]")!,scale=Number(viewport.dataset.layoutScale);
   for(const card of host.querySelectorAll<HTMLElement>("[data-service-node]")){
    const name=card.querySelector<HTMLElement>("[data-service-name]")!,metric=card.querySelector<HTMLElement>("[data-service-metric]")!;
    expect(name).not.toBeNull();expect(metric).not.toBeNull();
    const nameWidth=measure(name.textContent!,`600 ${name.style.fontSize} monospace`),metricWidth=measure(metric.textContent!,`${metric.style.fontSize} monospace`);
    const contentWidth=parseFloat((card.firstElementChild as HTMLElement).style.width);
    const metricBudget=contentWidth-(viewport.dataset.cardMode === "compact" ? parseFloat(name.style.maxWidth)+22 : 14);
    expect(nameWidth).toBeLessThanOrEqual(parseFloat(name.style.maxWidth)+.01);expect(metricWidth).toBeLessThanOrEqual(metricBudget+.01);
    expect(nameWidth*scale).toBeLessThan(parseFloat(card.style.width));expect(metricWidth*scale).toBeLessThan(parseFloat(card.style.width));
    expect(metric.style.textOverflow).not.toBe("ellipsis");expect(card.title).toContain("p95");
    if(viewport.dataset.cardMode==="compact"){expect(card.querySelectorAll("[data-service-text]")).toHaveLength(1);expect(metric.textContent).not.toContain(" · ");}
   }
  });
 }
});

it("R1 measures the usable viewport after classic scrollbars",async()=>{
 const original=HTMLElement.prototype.getBoundingClientRect;
 vi.spyOn(HTMLElement.prototype,"getBoundingClientRect").mockImplementation(function(this:HTMLElement){return this.hasAttribute("data-service-viewport")?DOMRect.fromRect({width:1100,height:230}):original.call(this);});
 vi.spyOn(HTMLElement.prototype,"clientWidth","get").mockImplementation(function(this:HTMLElement){return this.hasAttribute("data-service-viewport")?1085:0;});
 vi.spyOn(HTMLElement.prototype,"clientHeight","get").mockImplementation(function(this:HTMLElement){return this.hasAttribute("data-service-viewport")?215:0;});
 const host=document.createElement("div");document.body.append(host);const root=createRoot(host);cleanups.push(()=>root.unmount());
 await act(async()=>root.render(<MantineProvider><ServiceMapViz panel={{id:"m",title:"Map",viz:"service_map"}} result={{id:"m",status:"ok",elapsed_ms:1,frame:demoFrame}} dark={false} height={254}/></MantineProvider>));
 const viewport=host.querySelector<HTMLElement>("[data-service-viewport]")!;
 expect(Number(viewport.dataset.contentWidth)).toBeLessThanOrEqual(1085);
 for(const entry of host.querySelectorAll<HTMLElement>('[data-service-entry="true"]')){expect(parseFloat(entry.style.top)-viewport.scrollTop).toBeGreaterThanOrEqual(0);expect(parseFloat(entry.style.top)+parseFloat(entry.style.height)-viewport.scrollTop).toBeLessThanOrEqual(215);}
});

it("R1 keeps an entry in a distant Dagre rank inside the initial viewport",()=>{
 const base=model().nodes[0],graph={nodes:[],edges:[]} as ReturnType<typeof model>;
 for(let i=0;i<12;i++)graph.nodes.push({...base,id:`chain-${i}`});
 graph.nodes.push({...base,id:"late-entry"});
 for(let i=1;i<12;i++)graph.edges.push({id:`hop-${i}`,caller:`chain-${i-1}`,callee:`chain-${i}`,edge_type:"call",calls:1,request_rate:1,error_rate:0,average_ms:1,status:null});
 graph.edges.push({...graph.edges[0],id:"late-hop",caller:"late-entry",callee:"chain-11"});
 const got=layoutServiceMap(graph,{width:1100,height:180});
 for(const n of got.nodes.filter(n=>n.entry)){expect(n.x).toBeGreaterThanOrEqual(0);expect(n.x+n.width).toBeLessThanOrEqual(1100);expect(n.y-got.initialScrollY).toBeGreaterThanOrEqual(0);expect(n.y+n.height-got.initialScrollY).toBeLessThanOrEqual(180);}
 for(const n of got.nodes)for(const other of got.nodes.filter(o=>o.id!==n.id))expect(n.x<other.x+other.width&&n.x+n.width>other.x&&n.y<other.y+other.height&&n.y+n.height>other.y).toBe(false);
});

it("R1 viewport measurement retains horizontal fit when scrollbars appear",async()=>{
 let resize=()=>{},usableWidth=1100,usableHeight=230;
 const originalObserver=globalThis.ResizeObserver;cleanups.push(()=>vi.stubGlobal("ResizeObserver",originalObserver));
 vi.stubGlobal("ResizeObserver",class {constructor(callback:()=>void){resize=callback;}observe(){}disconnect(){}});
 const original=HTMLElement.prototype.getBoundingClientRect;
 vi.spyOn(HTMLElement.prototype,"getBoundingClientRect").mockImplementation(function(this:HTMLElement){return this.hasAttribute("data-service-viewport")?DOMRect.fromRect({width:1100,height:230}):original.call(this);});
 vi.spyOn(HTMLElement.prototype,"clientWidth","get").mockImplementation(function(this:HTMLElement){return this.hasAttribute("data-service-viewport")?usableWidth:0;});
 vi.spyOn(HTMLElement.prototype,"clientHeight","get").mockImplementation(function(this:HTMLElement){return this.hasAttribute("data-service-viewport")?usableHeight:0;});
 const host=document.createElement("div");document.body.append(host);const root=createRoot(host);cleanups.push(()=>root.unmount());
 await act(async()=>root.render(<MantineProvider><ServiceMapViz panel={{id:"m",title:"Map",viz:"service_map"}} result={{id:"m",status:"ok",elapsed_ms:1,frame:demoFrame}} dark={false} height={254}/></MantineProvider>));
 const viewport=host.querySelector<HTMLElement>("[data-service-viewport]")!;
 usableWidth=1085;usableHeight=215;await act(async()=>resize());
 expect(Number(viewport.dataset.contentWidth)).toBeLessThanOrEqual(1085);
});

describe("Part 7 M7a",()=>{
 it("drops the compact metric before a protected service name",()=>{
  const n={...model().nodes[0],id:"recommendation",request_rate:1.2,error_rate:3.4};
  const label=serviceCardLabels(n,{width:140,scale:.85,compact:true});
  expect(label.name).toBe(n.id);expect(label.metric).toBe("");
 });
 for(const width of [744,1100,1440])for(const dark of [false,true])it(`renders every full demo service name at ${width}px, dark=${dark}`,async()=>{
  const original=HTMLElement.prototype.getBoundingClientRect;
  vi.spyOn(HTMLElement.prototype,"getBoundingClientRect").mockImplementation(function(this:HTMLElement){return this.hasAttribute("data-service-viewport")?DOMRect.fromRect({width,height:230}):original.call(this);});
  const context={font:"",measureText(text:string){return {width:text.length*parseFloat(this.font.match(/[\d.]+px/)![0])*.62};}};
  vi.spyOn(HTMLCanvasElement.prototype,"getContext").mockReturnValue(context as unknown as CanvasRenderingContext2D);
  const host=document.createElement("div");document.body.append(host);const root=createRoot(host);cleanups.push(()=>root.unmount());
  await act(async()=>root.render(<MantineProvider forceColorScheme={dark?"dark":"light"}><ServiceMapViz panel={{id:"m",title:"Map",viz:"service_map"}} result={{id:"m",status:"ok",elapsed_ms:1,frame:demoFrame,from_ms:0,to_ms:3600000}} dark={dark} height={254}/></MantineProvider>));
  const widths=new Set<string>();
  for(const card of host.querySelectorAll<HTMLElement>("[data-service-node]")){
   widths.add(card.style.width);expect(card.querySelector("[data-service-name]")!.textContent).toBe(card.dataset.serviceNode);
   expect(card.querySelector("[data-service-name]")!.textContent).not.toContain("…");
  }
  expect(widths.size).toBeGreaterThan(1);
 });
 it("protects all 24 characters using per-node measurements",()=>{
  const node={...model().nodes[0],id:"abcdefghijklmnopqrstuvwx"};
  const got=layoutServiceMap({nodes:[node],edges:[]},{width:1100,height:180,measureText:(text,font)=>text.length*parseFloat(font.match(/[\d.]+px/)![0])*.62});
  const n=got.nodes[0],label=serviceCardLabels(n,{width:n.width/got.scale,scale:got.scale,compact:got.compact,measureText:(text,font)=>text.length*parseFloat(font.match(/[\d.]+px/)![0])*.62});
  expect(label.name).toBe("abcdefghijklmnopqrstuvwx");expect(Number(label.nameWidth)).toBeGreaterThanOrEqual(label.name.length*label.nameSize*.62);
 });
});

describe("Part 7 M7b/M7c",()=>{
 it.each([270,744,1100,1440])("fits node and routed edge bounds horizontally at width %s",width=>{
  const graph=layoutServiceMap(model(),{width,height:230});expect(graph.contentWidth).toBe(width);expect(graph.scale).toBeGreaterThanOrEqual(graph.compact?.75:.85);
  for(const n of graph.nodes){expect(n.x).toBeGreaterThanOrEqual(0);expect(n.x+n.width).toBeLessThanOrEqual(width);}
  for(const edge of graph.edges){const numbers=edge.path.match(/-?[\d.]+/g)!.map(Number);for(let i=0;i<numbers.length;i+=2){expect(numbers[i]).toBeGreaterThanOrEqual(0);expect(numbers[i]).toBeLessThanOrEqual(width);}}
 });
 it("fits the 20px compact demo at 1100×230 with no clipping or pan",()=>{
  const graph=layoutServiceMap(model(),{width:1100,height:230});expect(graph).toMatchObject({compact:true,contentWidth:1100,contentHeight:230,initialScrollY:0});
  for(const n of graph.nodes){expect(n.height/graph.scale).toBeCloseTo(20);expect(n.y).toBeGreaterThanOrEqual(0);expect(n.y+n.height).toBeLessThanOrEqual(230);}
 });
 it("uses compact nodesep of 4 logical pixels",()=>{
  const base=model().nodes[0],nodes=[{...base,id:"entry"},...Array.from({length:8},(_,i)=>({...base,id:`callee-${i}`}))];
  const edges=nodes.slice(1).map(n=>({...model().edges[0],id:n.id,caller:"entry",callee:n.id}));
  const graph=layoutServiceMap({nodes,edges},{width:1100,height:230}),rank=graph.nodes.filter(n=>n.id!=="entry").sort((a,b)=>a.y-b.y);
  expect(graph.compact).toBe(true);for(let i=1;i<rank.length;i++){const gap=(rank[i].y-rank[i-1].y-rank[i-1].height)/graph.scale;expect(gap).toBeCloseTo(4);}
 });
 it("reports the number below the centred initial viewport and updates it on pan",async()=>{
  const host=document.createElement("div");document.body.append(host);const root=createRoot(host);cleanups.push(()=>root.unmount());
  await act(async()=>root.render(<MantineProvider><ServiceMapViz panel={{id:"m",title:"Map",viz:"service_map"}} result={{id:"m",status:"ok",elapsed_ms:1,frame:demoFrame}} dark={true} height={180}/></MantineProvider>));
  const viewport=host.querySelector<HTMLElement>("[data-service-viewport]")!;
  const below=()=>[...host.querySelectorAll<HTMLElement>("[data-service-node]")].filter(n=>parseFloat(n.style.top)+parseFloat(n.style.height)>156+viewport.scrollTop+.5).length;
  expect(below()).toBeGreaterThan(0);expect(host.querySelector("[data-service-below-hint]")!.textContent).toBe(`+${below()} below`);
  await act(async()=>viewport.dispatchEvent(new WheelEvent("wheel",{deltaY:20,bubbles:true,cancelable:true})));
  expect(host.querySelector("[data-service-below-hint]")?.textContent??"").toBe(below()?`+${below()} below`:"");
 });
 it("never pans or enlarges the map horizontally through native wheel or drag gestures",async()=>{
  const host=document.createElement("div");document.body.append(host);const root=createRoot(host);cleanups.push(()=>root.unmount());
  await act(async()=>root.render(<MantineProvider><ServiceMapViz panel={{id:"m",title:"Map",viz:"service_map"}} result={{id:"m",status:"ok",elapsed_ms:1,frame:demoFrame}} dark={false} height={254}/></MantineProvider>));
  const viewport=host.querySelector<HTMLElement>("[data-service-viewport]")!;
  const wheel=new WheelEvent("wheel",{deltaX:999,deltaY:-100,bubbles:true,cancelable:true});
  await act(async()=>{viewport.dispatchEvent(wheel);viewport.dispatchEvent(new PointerEvent("pointerdown",{button:0,clientX:100,clientY:100,bubbles:true}));viewport.dispatchEvent(new PointerEvent("pointermove",{clientX:-999,clientY:80,bubbles:true}));viewport.dispatchEvent(new PointerEvent("pointerup",{bubbles:true}));});
  expect(viewport.scrollLeft).toBe(0);expect(viewport.style.overflowX).toBe("hidden");
 });
});
