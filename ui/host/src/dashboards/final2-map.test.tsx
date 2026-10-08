import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import type { Frame } from "../../../panels/types";
const calls = vi.hoisted(() => ({ layout: vi.fn() }));
vi.mock("@dagrejs/dagre", async importOriginal => {
  const actual = await importOriginal<typeof import("@dagrejs/dagre")>();
  return { ...actual, layout: (...args: Parameters<typeof actual.layout>) => { calls.layout(); return actual.layout(...args); } };
});
import { serviceMapModel } from "../../../panels/rollups";
import { fitServiceMap, layoutServiceMapRaw, serviceMapStructure } from "./viz/service-map-layout";
import * as mapLayout from "./viz/service-map-layout";
import { ServiceMapViz } from "./viz/service-map";

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
it("I1 reuses positions across two moving-window metric refreshes and resize", async () => {
  const { viewport, render, resize } = await mount();
  const count = calls.layout.mock.calls.length;
  expect(count).toBeGreaterThan(0);
  const positions = [...viewport.querySelectorAll<HTMLElement>("button")].map(n => [n.style.left, n.style.top]);
  await render(frame(4, 110), 3630000); await render(frame(4, 120), 3660000);
  expect(calls.layout).toHaveBeenCalledTimes(count);
  expect([...viewport.querySelectorAll<HTMLElement>("button")].map(n => [n.style.left, n.style.top])).toEqual(positions);
  await resize(); expect(calls.layout).toHaveBeenCalledTimes(count);
});
it("I1 uses a module worker above 60 nodes and cancels it on unmount", async () => {
  const postMessage = vi.fn(), terminate = vi.fn(), worker = vi.fn(function() { return { postMessage, terminate, addEventListener: vi.fn(), removeEventListener: vi.fn() }; });
  vi.stubGlobal("Worker", worker);
  const { host } = await mount(61);
  expect(worker).toHaveBeenCalledOnce(); expect(postMessage).toHaveBeenCalledOnce(); expect(calls.layout).not.toHaveBeenCalled();
  expect(host.textContent).toContain("Laying out services");
  await act(async () => cleanups.splice(0).forEach(fn => fn())); expect(terminate).toHaveBeenCalledOnce();
});
it.each([100,200,400])("I1 keeps main-thread layout work below 50ms per refresh for %s services", async n => {
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
    console.log(`final2 main-thread layout refresh n=${n}: ${elapsed.toFixed(2)}ms`);
    expect(fitted.nodes).toHaveLength(n); expect(elapsed).toBeLessThan(50);
    await render(frame(n,metric),3630000);
  }
  await resize(); expect(calls.layout).not.toHaveBeenCalled(); expect(worker).not.toHaveBeenCalled();
});
it.each([false,true])("I4 leaves ordinary wheel scrolling native at overflow edge=%s", async bottom => {
  const { viewport } = await mount(bottom ? 40 : 4);
  if (bottom) viewport.scrollTop = 100000;
  const wheel = new WheelEvent("wheel", {deltaY:40,bubbles:true,cancelable:true});
  await act(async () => viewport.dispatchEvent(wheel)); expect(wheel.defaultPrevented).toBe(false);
  expect(viewport.style.touchAction).not.toBe("none");
});
it("M3 preserves a user's scroll through refresh and resize", async () => {
  const { viewport, render, resize } = await mount(40);
  await act(async () => { viewport.scrollTop = 100; viewport.dispatchEvent(new Event("scroll", {bubbles:true})); });
  await render(frame(40,110),3630000); expect(viewport.scrollTop).toBe(100);
  const layouts=calls.layout.mock.calls.length;
  await render(frame(40,1e9),3660000); expect(viewport.scrollTop).toBe(100);
  expect(calls.layout).toHaveBeenCalledTimes(layouts);
  await resize(); expect(viewport.scrollTop).toBe(100);
});

it("I1 accepts worker geometry, retains it during topology changes and ignores cancelled results",async()=>{
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
it("I1 invalidates measured geometry when card widths change",()=>{
  const model=serviceMapModel(frame(4),{from_ms:0,to_ms:3600000});
  expect(serviceMapStructure(model,t=>t.length*6).key).not.toBe(serviceMapStructure(model,t=>t.length*9).key);
});
it("I1 changes card mode on a narrow resize and then fits further resizes without Dagre",async()=>{
  const {viewport,resize}=await mount(4);
  expect(viewport.dataset.cardMode).toBe("full");
  await resize(270,280);
  expect(viewport.dataset.cardMode).toBe("compact");
  expect(Number(viewport.dataset.layoutScale)).toBeGreaterThanOrEqual(.75);
  const layouts=calls.layout.mock.calls.length;
  await resize(260,270); expect(calls.layout).toHaveBeenCalledTimes(layouts);
});
it("I1 reports worker failures while leaving page scrolling available",async()=>{
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
it.each([20, 30, 40, 60])("I-B synchronous compact metric refresh performs no layout for %s services", async n => {
  const raw = vi.spyOn(mapLayout, "layoutServiceMapRaw");
  const { host, viewport, render } = await mount(n, fanout(n));
  expect(viewport.dataset.cardMode).toBe("compact");
  expect([...host.querySelectorAll<HTMLElement>("[data-service-metric]")].some(el => el.style.display !== "none" && el.textContent)).toBe(true);
  expect(raw).toHaveBeenCalledOnce();
  expect(calls.layout).toHaveBeenCalledOnce();
  const passes = calls.layout.mock.calls.length;
  const positions = [...viewport.querySelectorAll<HTMLElement>("button")].map(el => [el.style.left, el.style.top, el.style.width]);
  const labels = host.textContent;
  for (const [metric, error, health] of [[360000, 12.3, "degraded"], [360000000, 0, "healthy"]] as const) {
    const next = serviceMapModel(fanout(n, metric, error, health), {from_ms:30000,to_ms:3630000});
    const base = serviceMapModel(fanout(n), {from_ms:0,to_ms:3600000});
    expect(serviceMapStructure(next).key).toBe(serviceMapStructure(base).key);
    const start = performance.now();
    await render(fanout(n, metric, error, health), metric);
    console.log(`final3 synchronous refresh n=${n}: ${(performance.now()-start).toFixed(2)}ms; Dagre passes=${calls.layout.mock.calls.length-passes}`);
    expect(raw).toHaveBeenCalledOnce();
    expect(calls.layout).toHaveBeenCalledTimes(passes);
    expect([...viewport.querySelectorAll<HTMLElement>("button")].map(el => [el.style.left, el.style.top, el.style.width])).toEqual(positions);
    expect(host.textContent).not.toBe(labels);
  }
});
it("I-B measures the fixed metric slot once per font and measurement context", () => {
  const measure = vi.fn((text: string) => text.length * 6);
  const model = serviceMapModel(fanout(30), {from_ms:0,to_ms:3600000});
  serviceMapStructure(model, measure);
  const slots = () => measure.mock.calls.filter(([text]) => text.endsWith("/s") || text.endsWith("% err"));
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
it.each([[30, 21], [12, 9]])("final4 keeps realistic-name metrics on %s-service fan-out above baseline %s", async (n, minimum) => {
  const {host, viewport, render, resize} = await mount(n, namedFanout(n));
  await resize(1440, 300);
  expect(viewport.dataset.cardMode).toBe("compact");
  const shown = () => [...host.querySelectorAll<HTMLElement>("[data-service-metric]")].filter(el => el.style.display !== "none" && el.textContent).length;
  const count = calls.layout.mock.calls.length;
  const positions = [...viewport.querySelectorAll<HTMLElement>("button")].map(el => [el.style.left, el.style.top, el.style.width]);
  for (const [metric, error] of [[3600, 0], [360000, 12.3], [360000000, .0004], [3600000000000, 100]] as const) {
    await render(namedFanout(n, metric, error));
    console.log(`final4 realistic fan-out n=${n}: ${shown()}/${n} metrics`);
    expect(shown()).toBeGreaterThanOrEqual(minimum);
    expect(calls.layout).toHaveBeenCalledTimes(count);
    expect([...viewport.querySelectorAll<HTMLElement>("button")].map(el => [el.style.left, el.style.top, el.style.width])).toEqual(positions);
  }
});
it.each([
  [0, 0, "0/s"], [-1, 0, "0/s"], [1e-300, 0, "<0.01/s"], [999999, 0, "999K/s"], [1e300, 0, "999K/s"],
  [1, .0004, "0.1% err"], [1, 99.9, "99.9% err"], [1, 100, "100% err"], [1, 1e300, "100% err"],
])("final4 bounds card formats rate=%s error=%s", (rate, error, expected) => {
  const node = serviceMapModel(fanout(2), {from_ms:0,to_ms:3600000}).nodes[0];
  const labels = mapLayout.serviceCardLabels({...node, request_rate:rate, error_rate:error}, {width:300,scale:1,compact:true});
  expect(labels.metric).toBe(expected);
  expect(labels.metric.length).toBeLessThanOrEqual(9);
});

it("final4 preserves zero error in the full-card metric pair", () => {
  const node = serviceMapModel(fanout(2), {from_ms:0,to_ms:3600000}).nodes[0];
  const labels = mapLayout.serviceCardLabels({...node, error_rate:0}, {width:400,scale:1,compact:false});
  expect(labels.metric).toContain("0% err");
});
