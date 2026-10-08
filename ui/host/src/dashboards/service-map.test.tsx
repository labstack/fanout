import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { serviceMapModel } from "../../../panels/rollups";
import { chartThemeFor } from "../../../panels/compile";
import { typeScale } from "../../../tokens";
import { ServiceMapViz } from "./viz/service-map";
import { fitServiceMap, layoutServiceMapRaw, serviceCardLabels } from "./viz/service-map-layout";
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

for (const [width,height] of [[780,460],[1100,220],[1440,480]]) for (const dark of [false,true]) it(`renders floor-sized labels and safe titles at ${width}×${height}, dark=${dark}`, async () => {
  const original = HTMLElement.prototype.getBoundingClientRect;
  vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function(this: HTMLElement) { return this.hasAttribute("data-service-viewport") ? DOMRect.fromRect({width,height}) : original.call(this); });
  const host = document.createElement("div"); document.body.append(host); const root = createRoot(host); cleanups.push(() => root.unmount());
  await act(async () => root.render(<MantineProvider forceColorScheme={dark ? "dark" : "light"}><ServiceMapViz panel={{id:"m",title:"Map",viz:"service_map"}} result={{id:"m",status:"ok",elapsed_ms:1,frame:demoFrame}} dark={dark} height={height+24}/></MantineProvider>));
  const cards = [...host.querySelectorAll<HTMLElement>('[data-service-node]')];
  expect(cards).toHaveLength(20); expect(host.textContent).not.toMatch(/\+\d+ below/);
  for (const card of cards) {
    const text = card.querySelector<HTMLElement>('[data-service-text]')!;
    expect(parseFloat(text.style.fontSize)).toBeGreaterThanOrEqual(typeScale.micro);
    expect(text.style.visibility).not.toBe("hidden");
    expect(card.querySelector('[data-service-name]')!.textContent).toBe(card.dataset.serviceNode);
    expect(card.title).toContain("p95"); expect(card.title).toContain("err");
    expect(card.querySelector<HTMLElement>('[data-service-name]')!.style.textOverflow).toBe("ellipsis");
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
  for (const node of host.querySelectorAll<HTMLElement>('[data-service-node]')) { expect(parseFloat(node.style.left)+parseFloat(node.style.width)).toBeLessThanOrEqual(width-8); expect(parseFloat(node.style.top)+parseFloat(node.style.height)).toBeLessThanOrEqual(height-8); }
});

it("drops optional metrics before names and bounds labels without changing the full tooltip", () => {
  const node = {...model().nodes[0],id:"recommendation",request_rate:1.2,error_rate:3.4};
  expect(serviceCardLabels(node,{width:140,scale:.85}).metric).toBe("");
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
