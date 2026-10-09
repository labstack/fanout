import type { Frame } from "../../../panels/types";
import * as mapLayout from "./viz/service-map-layout";
import * as serviceMapExports from "./viz/service-map";
import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { serviceMapModel } from "../../../panels/rollups";
import { chartThemeFor } from "../../../panels/compile";
import { typeScale } from "../../../tokens";
import { ServiceMapViz } from "./viz/service-map";
import { fitServiceMap, layoutServiceMapRaw, serviceCardLabels, serviceMapStructure } from "./viz/service-map-layout";
import { PanelCard } from "./panel-card";
import { makeDrill } from "./drill-state";
import { demoFrame } from "../../tests/service-map-demo";
import { assertServiceMapDOM } from "../../tests/service-map-collector";
export { demoFrame } from "../../tests/service-map-demo";
vi.mock("./echart-canvas", () => ({ EChartCanvas: ({ label }: { label: string }) => <div role="img" aria-label={label} /> }));
const cleanups: (() => void)[] = [];
beforeEach(() => vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true));
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(fn => fn())); vi.restoreAllMocks(); vi.unstubAllGlobals(); document.body.innerHTML = ""; });
const model = () => serviceMapModel(demoFrame, { from_ms: 0, to_ms: 3600000 });
const layoutServiceMap = (m: ReturnType<typeof model>, size: { width: number; height: number }) => fitServiceMap(layoutServiceMapRaw(m, size), m, size);

it.each([180,190,220,230,280,396,460,480])("contains nodes, labels and routed edges at dashboard body height %s", height => {
  const graph = layoutServiceMap(model(), { width: 1100, height });
  expect(graph.nodes).toHaveLength(20); expect(graph.edges).toHaveLength(23);
  for (const node of graph.nodes) { expect(node.x).toBeGreaterThanOrEqual(8); expect(node.y).toBeGreaterThanOrEqual(8); expect(node.x + node.width).toBeLessThanOrEqual(1092); expect(node.y + node.height).toBeLessThanOrEqual(height - 8); }
  for (const edge of graph.edges) for (const p of edge.points) { expect(p.x).toBeGreaterThanOrEqual(8); expect(p.y).toBeGreaterThanOrEqual(8); expect(p.x).toBeLessThanOrEqual(1092); expect(p.y).toBeLessThanOrEqual(height - 8); }
  expect(graph.uncalledLabel!.y).toBeGreaterThanOrEqual(8);
});

for (const [width,height] of [[780,460],[1100,220],[1440,480]]) for (const dark of [false,true]) it(`renders fitted labels and safe titles at ${width}×${height}, dark=${dark}`, async () => {
  const original = HTMLElement.prototype.getBoundingClientRect;
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function(this: HTMLElement) { return this.hasAttribute("data-service-viewport") ? DOMRect.fromRect({width,height}) : original.call(this); });
  const host = document.createElement("div"); document.body.append(host); const root = createRoot(host); cleanups.push(() => root.unmount());
  await act(async () => root.render(<MantineProvider forceColorScheme={dark ? "dark" : "light"}><ServiceMapViz panel={{id:"m",title:"Map",viz:"service_map"}} result={{id:"m",status:"ok",elapsed_ms:1,frame:demoFrame}} dark={dark} height={height+24}/></MantineProvider>));
  const cards = [...host.querySelectorAll<HTMLElement>('[data-service-node]')];
  expect(cards).toHaveLength(20); expect(host.textContent).not.toMatch(/\+\d+ below/);
  for (const card of cards) {
    const text = card.querySelector<HTMLElement>('[data-service-text]')!;
    const scale=Number(host.querySelector<HTMLElement>('[data-service-viewport]')!.dataset.layoutScale);
    expect(parseFloat(text.style.fontSize)+.01).toBeGreaterThanOrEqual(typeScale.micro*Math.min(1,scale/.65));
    expect(text.style.visibility).not.toBe("hidden");
    expect(card.querySelector('[data-service-name]')!.textContent).toBe(card.dataset.serviceNode);
    expect(card.title).toContain("p95"); expect(card.title).toContain("err");
    expect(card.querySelector<HTMLElement>('[data-service-name]')!.style.textOverflow).not.toBe("ellipsis");
  }
});

it.each([{width:780,height:460},{width:1100,height:220}])("runs the shared browser collector against fitted DOM geometry at $width × $height", async size => {
  const original = HTMLElement.prototype.getBoundingClientRect;
  vi.spyOn(HTMLElement.prototype,"getBoundingClientRect").mockImplementation(function(this:HTMLElement) { return this.hasAttribute("data-service-viewport") ? DOMRect.fromRect(size) : original.call(this); });
  const host = document.createElement("div"); document.body.append(host); const root = createRoot(host); cleanups.push(()=>root.unmount());
  await act(async()=>root.render(<MantineProvider><ServiceMapViz panel={{id:"m",title:"Map",viz:"service_map"}} result={{id:"m",status:"ok",elapsed_ms:1,frame:demoFrame}} dark={false} height={size.height+24}/></MantineProvider>));
  const viewport = host.querySelector<HTMLElement>('[data-service-viewport]')!;
  for (const node of viewport.querySelectorAll<HTMLElement>('[data-service-node]')) node.getBoundingClientRect = () => DOMRect.fromRect({x:parseFloat(node.style.left),y:parseFloat(node.style.top),width:parseFloat(node.style.width),height:parseFloat(node.style.height)});
  for (const edge of viewport.querySelectorAll<SVGPathElement>('svg > g > path')) {
    const points = edge.getAttribute("d")!.match(/-?[\d.]+/g)!.map(Number), xs = points.filter((_,i)=>i%2===0), ys = points.filter((_,i)=>i%2===1);
    edge.getBoundingClientRect = () => DOMRect.fromRect({x:Math.min(...xs),y:Math.min(...ys),width:Math.max(...xs)-Math.min(...xs),height:Math.max(...ys)-Math.min(...ys)});
  }
  const label = viewport.querySelector<HTMLElement>('[data-service-uncalled-label]')!;
  label.getBoundingClientRect = () => DOMRect.fromRect({x:(size.width-180)/2,y:parseFloat(label.style.top),width:180,height:11});
  expect(assertServiceMapDOM(viewport)).toMatchObject({nodes:20,edges:23,body:size});
  const clipped = viewport.querySelector<HTMLButtonElement>('button[title]')!;
  clipped.getBoundingClientRect = () => DOMRect.fromRect({x:0,y:-10,width:20,height:20});
  expect(()=>assertServiceMapDOM(viewport)).toThrow(/clipped/);
});

it("keeps queued pan positions stable, suppresses a drag click and Fit resets zoom/pan", async () => {
  const host = document.createElement("div"); document.body.append(host); const root = createRoot(host); cleanups.push(() => root.unmount());
  const point = vi.fn(), mapView = vi.fn();
  await act(async () => root.render(<MantineProvider><ServiceMapViz panel={{id:"m",title:"Map",viz:"service_map"}} result={{id:"m",status:"ok",elapsed_ms:1,frame:demoFrame}} dark={false} height={300} onPoint={point} onMapView={mapView}/></MantineProvider>));
  const viewport = host.querySelector<HTMLElement>('[data-service-viewport]')!, content = host.querySelector<HTMLElement>('[data-service-content]')!;
  await act(async () => {
    viewport.dispatchEvent(new PointerEvent("pointerdown", { button:0, clientX:100, clientY:100, bubbles:true }));
    for (const [x,y] of [[120,130],[140,160]]) viewport.dispatchEvent(new PointerEvent("pointermove", {clientX:x, clientY:y, bubbles:true}));
    viewport.dispatchEvent(new PointerEvent("pointerup", {bubbles:true}));
  });
  expect(content.style.transform).toBe("translate(40px, 60px) scale(1)");
  await act(async () => host.querySelector<HTMLButtonElement>('[data-service-node]')!.click()); expect(point).not.toHaveBeenCalled();
  await act(async () => mapView.mock.lastCall![0].fit());
  expect(content.style.transform).toBe("translate(0px, 0px) scale(1)"); expect(mapView.mock.lastCall![0].canFit).toBe(false);
});

it("measures usable client bounds and refits on resize without changing the LR layout", async () => {
  let resize = () => {}, width = 1085, height = 215;
  vi.stubGlobal("ResizeObserver", class { constructor(callback: () => void) { resize = callback; } observe() {} disconnect() {} });
  vi.spyOn(HTMLElement.prototype,"clientWidth","get").mockImplementation(function(this:HTMLElement){return this.hasAttribute("data-service-viewport") ? width : 0;});
  vi.spyOn(HTMLElement.prototype,"clientHeight","get").mockImplementation(function(this:HTMLElement){return this.hasAttribute("data-service-viewport") ? height : 0;});
  const host = document.createElement("div"); document.body.append(host); const root = createRoot(host); cleanups.push(() => root.unmount());
  await act(async () => root.render(<MantineProvider><ServiceMapViz panel={{id:"m",title:"Map",viz:"service_map"}} result={{id:"m",status:"ok",elapsed_ms:1,frame:demoFrame}} dark={false} height={254}/></MantineProvider>));
  width = 744; height = 460; await act(async () => resize());
  const content=host.querySelector<HTMLElement>('[data-service-content]')!;
  expect(parseFloat(content.style.width)).toBe(width);expect(parseFloat(content.style.height)).toBe(height);expect(content.style.transform).toBe("translate(0px, 0px) scale(1)");
  for (const node of host.querySelectorAll<HTMLElement>('[data-service-node]')) { expect(parseFloat(node.style.left)+parseFloat(node.style.width)).toBeLessThanOrEqual(parseFloat(content.style.width)-8); expect(parseFloat(node.style.top)+parseFloat(node.style.height)).toBeLessThanOrEqual(parseFloat(content.style.height)-8); }
});

it("drops optional metrics before names and bounds labels without changing the full tooltip", () => {
  const node = {...model().nodes[0],id:"recommendation",request_rate:1.2,error_rate:3.4};
  expect(serviceCardLabels(node,{width:140,scale:.85,compact:true}).metric).toBe("");
  expect(serviceCardLabels({...node,id:"cart"},{width:300,scale:1}).metric).toContain("3.4% err");
});

describe("shared map interactions", () => {
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
    const viewport = host.querySelector<HTMLElement>('[data-service-viewport]')!;
    const content = viewport.querySelector<HTMLElement>('[data-service-content]')!;
    await act(async () => viewport.dispatchEvent(new WheelEvent("wheel", { deltaY: -200, clientX: 300, clientY: 100, bubbles: true, cancelable: true })));
    expect(content.style.transform).not.toBe("translate(0px, 0px) scale(1)");
    await act(async () => {
      viewport.dispatchEvent(new PointerEvent("pointerdown", { button: 0, clientX: 100, clientY: 80, bubbles: true }));
      viewport.dispatchEvent(new PointerEvent("pointermove", { clientX: 140, clientY: 110, bubbles: true }));
      viewport.dispatchEvent(new PointerEvent("pointerup", { bubbles: true }));
    });
    const fit = host.querySelector<HTMLButtonElement>('[aria-label="Fit Map graph"]')!;
    expect(fit).not.toBeNull(); await act(async () => fit.click());
    expect(content.style.transform).toBe("translate(0px, 0px) scale(1)");
    expect(host.textContent).toContain("No traced calls in this window");
    expect(host.textContent).toContain("20 services · 23 routes");
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

const calls = vi.hoisted(() => ({ layout: vi.fn() }));
vi.mock("@dagrejs/dagre", async importOriginal => {
  const actual = await importOriginal<typeof import("@dagrejs/dagre")>();
  return { ...actual, layout: (...args: Parameters<typeof actual.layout>) => { calls.layout(); return actual.layout(...args); } };
});

describe("service map worker and refresh lifecycle",()=>{
 beforeEach(()=>calls.layout.mockClear());
function frame(n: number, metric = 100): Frame {
  const names = ["kind", "service", "caller", "callee", "edge_type", "calls", "average_ms", "error_rate", "health", "p95_ms", "spans"];
  const rows = [...Array.from({ length: n }, (_, i) => ["node", `svc-${i}`, "", "", "", null, null, 0, "healthy", 30, metric]),
    ...Array.from({ length: n - 1 }, (_, i) => ["edge", "", `svc-${i}`, `svc-${i+1}`, "call", metric, 20, 0, "", null, null])];
  return { rows: rows.length, columns: names.map(name => ({ name, type: "string", role: "dimension" })), values: names.map((_, i) => rows.map(r => r[i] as string | number | null)) };
}
const cleanups: (() => void)[] = [];
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(fn => fn())); vi.restoreAllMocks(); vi.unstubAllGlobals(); calls.layout.mockClear(); });
async function mount(n = 4, initial = frame(n)) {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  let resize = () => {}, width = 1100, height = 300;
  vi.stubGlobal("ResizeObserver", class { constructor(cb: () => void) { resize = cb; } observe() {} disconnect() {} });
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(() => DOMRect.fromRect({ width, height }));
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host); cleanups.push(() => { root.unmount(); host.remove(); });
  const render = async (f: Frame, end = 3600000) => act(async () => root.render(<MantineProvider><ServiceMapViz panel={{id:"m",title:"Map",viz:"service_map"}} result={{id:"m",status:"ok",elapsed_ms:1,frame:f,from_ms:end-3600000,to_ms:end}} dark={false} height={324}/></MantineProvider>));
  await render(initial);
  const viewport = host.querySelector<HTMLElement>("[data-service-viewport]")!;
  return { host, viewport, render, resize: async (w = 1050, h = 280) => act(async () => { width = w; height = h; resize(); }) };
}
it("reuses positions across two moving-window metric refreshes and resize", async () => {
  const { viewport, render, resize } = await mount();
  const count = calls.layout.mock.calls.length;
  expect(count).toBeGreaterThan(0);
  const positions = [...viewport.querySelectorAll<HTMLElement>("button")].map(n => [n.style.left, n.style.top]);
  await render(frame(4, 110), 3630000); await render(frame(4, 120), 3660000);
  expect(calls.layout).toHaveBeenCalledTimes(count);
  expect([...viewport.querySelectorAll<HTMLElement>("button")].map(n => [n.style.left, n.style.top])).toEqual(positions);
  await resize(); expect(calls.layout).toHaveBeenCalledTimes(count);
});
it("uses a module worker above 60 nodes and cancels it on unmount", async () => {
  const postMessage = vi.fn(), terminate = vi.fn(), worker = vi.fn(function() { return { postMessage, terminate, addEventListener: vi.fn(), removeEventListener: vi.fn() }; });
  vi.stubGlobal("Worker", worker);
  const { host } = await mount(61);
  expect(worker).toHaveBeenCalledOnce(); expect(postMessage).toHaveBeenCalledOnce(); expect(calls.layout).not.toHaveBeenCalled();
  expect(host.textContent).toContain("Laying out services");
  await act(async () => cleanups.splice(0).forEach(fn => fn())); expect(terminate).toHaveBeenCalledOnce();
});
it.each([100,200,400])("keeps main-thread layout work below 50ms per refresh for %s services", async n => {
  const worker = vi.fn(function() { return { postMessage: vi.fn(), terminate: vi.fn() }; }); vi.stubGlobal("Worker", worker);
  const size={width:1100,height:300};
  const base=serviceMapModel(frame(n),{from_ms:0,to_ms:3600000});
  const raw=layoutServiceMapRaw(base,size); // Represents the worker's completed first layout.
  const { render, resize } = await mount(n);
  calls.layout.mockClear(); worker.mockClear();
  for (const metric of [110,120]) {
    const next=serviceMapModel(frame(n,metric),{from_ms:30000,to_ms:3630000});
    const start=performance.now();
    serviceMapStructure(next);
    const fitted=fitServiceMap(raw,next,size);
    const elapsed=performance.now()-start;
    console.log(`main-thread layout refresh n=${n}: ${elapsed.toFixed(2)}ms`);
    expect(fitted.nodes).toHaveLength(n); expect(elapsed).toBeLessThan(50);
    await render(frame(n,metric),3630000);
  }
  await resize(); expect(calls.layout).not.toHaveBeenCalled(); expect(worker).not.toHaveBeenCalled();
});
it("zooms with the wheel and preserves the user's zoom/pan through metric refresh and resize", async () => {
  const { viewport, render, resize } = await mount(4);
  const wheel = new WheelEvent("wheel", {deltaY:-100,clientX:200,clientY:100,bubbles:true,cancelable:true});
  await act(async () => viewport.dispatchEvent(wheel)); expect(wheel.defaultPrevented).toBe(true);
  const content = viewport.querySelector<HTMLElement>('[data-service-content]')!;
  const transform = content.style.transform; expect(transform).toContain("scale(1.22");
  await render(frame(4,110),3630000); await resize(); expect(content.style.transform).toBe(transform);
});

it("reveals floor-sized labels that fit their boxes when zooming a dense fitted graph", async () => {
  const { viewport } = await mount(60, fanout(60));
  const card = viewport.querySelector<HTMLElement>('[data-service-node]')!;
  const text = card.querySelector<HTMLElement>('[data-service-text]')!;
  expect(text.style.visibility).not.toBe("hidden");
  expect(card.querySelector('[data-service-name]')!.textContent).toBe(card.dataset.serviceNode);
  await act(async () => viewport.dispatchEvent(new WheelEvent("wheel", {deltaY:-5000,clientX:200,clientY:100,bubbles:true,cancelable:true})));
  const content = viewport.querySelector<HTMLElement>('[data-service-content]')!;
  const zoom = Number(content.style.transform.match(/scale\(([^)]+)\)/)![1]);
  expect(text.style.visibility).not.toBe("hidden");
  expect(parseFloat(text.style.fontSize)*zoom).toBeGreaterThanOrEqual(11);
  expect(parseFloat(text.style.fontSize)*zoom+2).toBeLessThanOrEqual(parseFloat(card.style.height)*zoom);
});


it("accepts worker geometry, retains it during topology changes and ignores cancelled results",async()=>{
  const workers: {onmessage?: (event:{data:ReturnType<typeof layoutServiceMapRaw>})=>void;postMessage:ReturnType<typeof vi.fn>;terminate:ReturnType<typeof vi.fn>}[]=[];
  vi.stubGlobal("Worker",class {onmessage?: (event:{data:ReturnType<typeof layoutServiceMapRaw>})=>void;postMessage=vi.fn();terminate=vi.fn();constructor(){workers.push(this);}});
  const {host,render}=await mount(61);
  const request=workers[0].postMessage.mock.calls[0][0];
  const geometry=layoutServiceMapRaw(request.model,request.size,request.widths);
  await act(async()=>workers[0].onmessage!({data:geometry}));
  expect(host.querySelectorAll("[data-service-node]")).toHaveLength(61);
  await render(frame(62));
  expect(workers[0].terminate).toHaveBeenCalledOnce();
  expect(host.querySelectorAll("[data-service-node]")).toHaveLength(61);
  await act(async()=>workers[0].onmessage!({data:geometry}));
  expect(host.querySelectorAll("[data-service-node]")).toHaveLength(61);
  const next=workers[1].postMessage.mock.calls[0][0];
  await act(async()=>workers[1].onmessage!({data:layoutServiceMapRaw(next.model,next.size,next.widths)}));
  expect(host.querySelectorAll("[data-service-node]")).toHaveLength(62);
});
it("invalidates measured geometry when card widths change",()=>{
  const model=serviceMapModel(frame(4),{from_ms:0,to_ms:3600000});
  expect(serviceMapStructure(model,t=>t.length*6).key).not.toBe(serviceMapStructure(model,t=>t.length*9).key);
});
it("contains a narrow resize and fits further resizes without Dagre",async()=>{
  const {viewport,resize}=await mount(4);
  await resize(270,280);
  for (const node of viewport.querySelectorAll<HTMLElement>("button")) expect(parseFloat(node.style.left)+parseFloat(node.style.width)).toBeLessThanOrEqual(270);
  const layouts=calls.layout.mock.calls.length;
  await resize(260,270); expect(calls.layout).toHaveBeenCalledTimes(layouts);
});
it("reports worker failures while leaving page scrolling available",async()=>{
  let failed: ((event:Event)=>void) | undefined;
  vi.stubGlobal("Worker",class {set onerror(callback:(event:Event)=>void){failed=callback;} postMessage(){} terminate(){} });
  const {host,viewport}=await mount(61);
  await act(async()=>failed!(new Event("error")));
  expect(host.textContent).toContain("Service layout unavailable");
  const wheel=new WheelEvent("wheel",{deltaY:10,cancelable:true}); viewport.dispatchEvent(wheel);
  expect(wheel.defaultPrevented).toBe(false);
});

function fanout(n: number, metric = 3600, error = 0, health = "healthy"): Frame {
  const f = frame(n, metric);
  const caller = f.columns.findIndex(c => c.name === "caller");
  f.values[caller] = f.values[caller].map((value, i) => i < n ? value : "svc-0");
  f.values[f.columns.findIndex(c => c.name === "error_rate")] = f.values[0].map(() => error);
  f.values[f.columns.findIndex(c => c.name === "health")] = f.values[0].map(() => health);
  return f;
}
it.each([20, 30, 40, 60])("synchronous metric refresh performs no layout for %s services", async n => {
  const raw = vi.spyOn(mapLayout, "layoutServiceMapRaw");
  const { host, viewport, render } = await mount(n, fanout(n));
  expect([...host.querySelectorAll<HTMLElement>("[data-service-node]")].every(el => el.title.includes("p95"))).toBe(true);
  expect(raw).toHaveBeenCalledOnce();
  expect(calls.layout).toHaveBeenCalledTimes(2); // Cached full and compact LR card sizes.
  const passes = calls.layout.mock.calls.length;
  const positions = [...viewport.querySelectorAll<HTMLElement>("button")].map(el => [el.style.left, el.style.top, el.style.width]);
  const labels = [...host.querySelectorAll<HTMLButtonElement>("[data-service-node]")].map(el=>el.title).join();
  for (const [metric, error, health] of [[360000, 12.3, "degraded"], [360000000, 0, "healthy"]] as const) {
    const next = serviceMapModel(fanout(n, metric, error, health), {from_ms:30000,to_ms:3630000});
    const base = serviceMapModel(fanout(n), {from_ms:0,to_ms:3600000});
    expect(serviceMapStructure(next).key).toBe(serviceMapStructure(base).key);
    const start = performance.now();
    await render(fanout(n, metric, error, health), metric);
    console.log(`synchronous refresh n=${n}: ${(performance.now()-start).toFixed(2)}ms; Dagre passes=${calls.layout.mock.calls.length-passes}`);
    expect(raw).toHaveBeenCalledOnce();
    expect(calls.layout).toHaveBeenCalledTimes(passes);
    expect([...viewport.querySelectorAll<HTMLElement>("button")].map(el => [el.style.left, el.style.top, el.style.width])).toEqual(positions);
    expect([...host.querySelectorAll<HTMLButtonElement>("[data-service-node]")].map(el=>el.title).join()).not.toBe(labels);
  }
});
it("measures the fixed health slot once per card font and measurement context", () => {
  const measure = vi.fn((text: string) => text.length * 6);
  const model = serviceMapModel(fanout(30), {from_ms:0,to_ms:3600000});
  serviceMapStructure(model, measure);
  const slots = () => measure.mock.calls.filter(([text]) => ["●","■","◆","○"].includes(text));
  expect(slots().length).toBeGreaterThan(0);
  const count = slots().length;
  serviceMapStructure(serviceMapModel(fanout(30, 360000), {from_ms:0,to_ms:3600000}), measure);
  expect(slots()).toHaveLength(count);
});

it("removes service-map collector instrumentation in production", async () => {
  vi.stubEnv("DEV", false);
  try {
    const { host } = await mount(30, fanout(30));
    const region = host.querySelector('[role="region"]')!;
    expect(region.querySelectorAll("button")).toHaveLength(30);
    for (const node of region.querySelectorAll("*")) {
      expect([...node.attributes].map(attr => attr.name).filter(name => name.startsWith("data-") && !name.startsWith("data-mantine-"))).toEqual([]);
    }
  } finally { vi.unstubAllEnvs(); }
});

const demoNames = ["frontend-proxy", "frontend", "load-generator", "cart", "checkout", "payment", "shipping", "quote", "currency", "product-catalog", "recommendation", "ad", "email", "accounting", "fraud-detection", "kafka", "cart-cache", "checkout-db", "image-provider", "otelcol-contrib"];
function namedFanout(n: number, metric = 3600, error = 0): Frame {
  const f = fanout(n, metric, error);
  const names = Array.from({length:n}, (_, i) => i === 0 ? "frontend-proxy" : `${demoNames[i % demoNames.length]}-${i}`);
  for (const column of ["service", "caller", "callee"]) {
    const index = f.columns.findIndex(c => c.name === column);
    f.values[index] = f.values[index].map(value => typeof value === "string" && value.startsWith("svc-") ? names[Number(value.slice(4))] : value);
  }
  return f;
}
it.each([[30, 21], [12, 9]])("keeps realistic-name metrics in titles and stable geometry for %s-service fan-out (former metric baseline %s)", async (n, minimum) => {
  const {host, viewport, render, resize} = await mount(n, namedFanout(n));
  await resize(1440, 300);
  const shown = () => [...host.querySelectorAll<HTMLElement>("[data-service-metric]")].filter(el => el.style.display !== "none" && el.textContent).length;
  const count = calls.layout.mock.calls.length;
  const positions = [...viewport.querySelectorAll<HTMLElement>("button")].map(el => [el.style.left, el.style.top, el.style.width]);
  for (const [metric, error] of [[3600, 0], [360000, 12.3], [360000000, .0004], [3600000000000, 100]] as const) {
    await render(namedFanout(n, metric, error));
    console.log(`realistic fan-out n=${n}: ${shown()}/${n} metrics`);
    expect([...host.querySelectorAll<HTMLButtonElement>("[data-service-node]")].every(el=>el.title.includes("p95") && el.title.includes("err"))).toBe(true);
    expect(calls.layout).toHaveBeenCalledTimes(count);
    expect([...viewport.querySelectorAll<HTMLElement>("button")].map(el => [el.style.left, el.style.top, el.style.width])).toEqual(positions);
  }
});
it.each([
  [0, 0, "0/s"], [-1, 0, "0/s"], [1e-300, 0, "<0.01/s"], [999999, 0, "999K/s"], [1e300, 0, "999K/s"],
  [1, .0004, "0.1% err"], [1, 99.9, "99.9% err"], [1, 100, "100% err"], [1, 1e300, "100% err"],
])("bounds card formats rate=%s error=%s", (rate, error, expected) => {
  const node = serviceMapModel(fanout(2), {from_ms:0,to_ms:3600000}).nodes[0];
  const labels = mapLayout.serviceCardLabels({...node, request_rate:rate, error_rate:error}, {width:82,scale:1});
  expect(labels.metric).toBe(expected);
  expect(labels.metric.length).toBeLessThanOrEqual(9);
});

it("preserves zero error in the full-card metric pair", () => {
  const node = serviceMapModel(fanout(2), {from_ms:0,to_ms:3600000}).nodes[0];
  const labels = mapLayout.serviceCardLabels({...node, error_rate:0}, {width:400,scale:1});
  expect(labels.metric).toContain("0% err");
});

});

describe("service map production instrumentation", () => {
  it("exposes the renderer and active layout API without a parallel layout boundary", () => {
    expect(Object.keys(serviceMapExports)).toEqual(["ServiceMapViz"]);
    expect(Object.keys(mapLayout)).not.toContain("layoutServiceMap");
  });
  it("renders map geometry without test-only attributes", async () => {
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host); cleanups.push(() => root.unmount());
    await act(async () => root.render(<MantineProvider><ServiceMapViz panel={{ id: "map", title: "Map", viz: "service_map" }} result={{ id: "map", status: "ok", elapsed_ms: 1, frame: demoFrame }} dark={false} height={300} /></MantineProvider>));
    expect(host.querySelectorAll("[data-service-node]").length).toBeGreaterThan(0);
    expect(host.querySelector("[data-pan-y], [data-text-width]")).toBeNull();
  });
});
