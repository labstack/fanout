import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";

const mocks = vi.hoisted(() => ({ init: vi.fn(), registered: [] as unknown[], connect: vi.fn(), disconnect: vi.fn(), instance: null as null | Record<string, unknown> }));

vi.mock("echarts/core", () => ({ init: mocks.init, use: (components: unknown[]) => { mocks.registered = components; }, connect: mocks.connect, disconnect: mocks.disconnect }));
vi.mock("echarts/charts", () => ({ BarChart: {}, LineChart: {}, CustomChart: {}, HeatmapChart: {}, ScatterChart: {} }));
vi.mock("echarts/components", () => ({ AriaComponent: {}, BrushComponent: {}, DataZoomComponent: {}, GraphicComponent: { id: "graphic" }, GridComponent: {}, LegendComponent: {}, MarkAreaComponent: {}, MarkLineComponent: {}, ToolboxComponent: {}, TooltipComponent: {}, VisualMapComponent: {} }));
vi.mock("echarts/renderers", () => ({ CanvasRenderer: {} }));

import { EChartCanvas } from "./echart-canvas";

function fresh() {
  const instance = { setOption: vi.fn(), dispatchAction: vi.fn(), dispose: vi.fn(), on: vi.fn(), resize: vi.fn(), group: "" };
  mocks.init.mockReturnValueOnce(instance);
  return instance;
}

describe("EChartCanvas", () => {
  afterEach(() => { document.body.innerHTML = ""; vi.clearAllMocks(); });

  it("I2 has no production audit hook, large data attributes or per-frame audit", async () => {
    vi.stubEnv("DEV", false);
    const instance=fresh(), getDisplayList=vi.fn(() => []);
    Object.assign(instance,{getZr:()=>({storage:{getDisplayList}})});
    const container=document.createElement("div"),root=createRoot(container); document.body.append(container);
    try {
      await act(async()=>root.render(<EChartCanvas option={{series:[]}} height={100} label="Production"/>));
      expect(instance.on.mock.calls.some(([event])=>event==="finished")).toBe(false);
      expect(getDisplayList).not.toHaveBeenCalled();
      expect(container.querySelector("[data-chart-audit],[data-chart-labels],[data-chart-legend],[data-chart-plot]")).toBeNull();
      expect((window as any).__fanoutAudit).toBeUndefined();
      expect(container.querySelector("[role=img]")!.hasAttribute("id")).toBe(false);
      for(const node of container.querySelectorAll("*")) for(const attr of node.attributes) if(attr.name.startsWith("data-")) expect(attr.value.length).toBeLessThanOrEqual(1024);
    } finally { await act(async()=>root.unmount()); vi.unstubAllEnvs(); }
  });

  it("inspects native text only on demand in development",async()=>{
    vi.stubEnv("DEV",true); window.history.replaceState({},"","/?__fanout_audit=1");
    const instance=fresh();
    const rect={x:10,y:10,width:40,height:12,clone(){return this;},applyTransform(){}};
    Object.assign(instance,{getZr:()=>({storage:{getDisplayList:()=>[{type:"tspan",style:{text:"300 ms",font:"11px monospace",fill:"#6b7280"},getBoundingRect:()=>rect,getComputedTransform:()=>null}]}})});
    const container=document.createElement("div");document.body.append(container);const root=createRoot(container);
    await act(async()=>root.render(<EChartCanvas option={{tooltip:{backgroundColor:"#fcfcfc"},xAxis:{axisLabel:{fontSize:11}},series:[]}} height={100} label="Native audit"/>));
    const audit=window.__fanoutAudit!(container.querySelector<HTMLElement>("[role=img]")!.id)!.audit;
    expect(audit.texts).toContainEqual(expect.objectContaining({text:"300 ms",size:11,color:"#6b7280",surface:"#fcfcfc",family:"11px monospace"}));
    await act(async()=>root.unmount()); window.history.replaceState({},"","/"); vi.unstubAllEnvs();
  });

  it("activates the brush cursor on initial render and option update", async () => {
    expect(mocks.registered).toContainEqual({ id: "graphic" });
    const instance = fresh();
    const container = document.createElement("div"); document.body.append(container); const root = createRoot(container); const zoom = vi.fn(); const option = { series: [] };
    await act(async () => root.render(<EChartCanvas option={option} height={100} label="P" onZoom={zoom} />));
    const cursorCalls = () => instance.dispatchAction.mock.calls.filter(([a]) => a.type === "takeGlobalCursor");
    expect(cursorCalls()).toHaveLength(1);
    expect(instance.setOption.mock.lastCall?.[0].toolbox?.show).toBe(false);
    expect(instance.setOption.mock.lastCall?.[0].brush.toolbox).toBeUndefined();
    expect(instance.dispatchAction).toHaveBeenCalledWith({ type: "takeGlobalCursor", key: "brush", brushOption: { brushType: "lineX", brushMode: "single" } });
    const nextOption = { series: [{ type: "line", data: [[1, 2]] }] };
    await act(async () => root.render(<EChartCanvas option={nextOption} height={100} label="P" onZoom={zoom} />));
    expect(instance.setOption).toHaveBeenCalledTimes(2); expect(cursorCalls()).toHaveLength(2);
    await act(async () => root.unmount()); container.remove();
  });

  it("commits only brushEnd, keeps one chart on callback changes, and clears the brush", async () => {
    const instance = fresh();
    const container = document.createElement("div"); document.body.append(container); const root = createRoot(container); const zoom = vi.fn(); const option = { series: [] };
    await act(async () => root.render(<EChartCanvas option={option} height={100} label="P" onZoom={zoom} />));
    const end = instance.on.mock.calls.find(([name]) => name === "brushEnd")?.[1] as ((payload: unknown) => void);
    expect(end).toBeTypeOf("function"); expect(instance.on.mock.calls.some(([name]) => name === "brushselected")).toBe(false);
    end({ areas: [{ coordRange: [1000, 5000] }] }); expect(zoom).toHaveBeenCalledOnce(); expect(zoom).toHaveBeenCalledWith(1000, 5000);
    expect(instance.dispatchAction).toHaveBeenCalledWith({ type: "brush", areas: [] }, { silent: true });
    expect(instance.dispatchAction.mock.calls.filter(([a]) => a.type === "takeGlobalCursor")).toHaveLength(2);
    const calls = mocks.init.mock.calls.length;
    const nextZoom = vi.fn();
    await act(async () => root.render(<EChartCanvas option={option} height={100} label="P" onZoom={nextZoom} />));
    expect(mocks.init.mock.calls.length).toBe(calls);
    expect(instance.setOption).toHaveBeenCalledOnce();
    end({ areas: [{ coordRange: [2000, 6000] }] });
    expect(nextZoom).toHaveBeenCalledWith(2000, 6000);
    expect(zoom).toHaveBeenCalledOnce();
    for (const areas of [[], [{ coordRange: [2, 2] }], [{ coordRange: [NaN, 3] }]]) end({ areas });
    expect(nextZoom).toHaveBeenCalledOnce();
    await act(async () => root.unmount());
  });

  it("updates zoom availability without replacing the chart or adding point affordances", async () => {
    const instance = fresh();
    const container = document.createElement("div"); document.body.append(container); const root = createRoot(container); const option = { series: [] }; const zoom = vi.fn();
    await act(async () => root.render(<EChartCanvas option={option} height={100} label="P" />));
    expect(container.querySelector("div")!.style.cursor).toBe("");
    expect(container.querySelector("div")!.hasAttribute("tabindex")).toBe(false);
    expect(instance.dispatchAction).not.toHaveBeenCalled();
    await act(async () => root.render(<EChartCanvas option={option} height={100} label="P" onZoom={zoom} />));
    expect(instance.setOption.mock.lastCall?.[0].brush).toMatchObject({ xAxisIndex: 0, brushMode: "single" });
    expect(container.querySelector("div")!.getAttribute("aria-label")).toContain("Brush across the chart to zoom to that range.");
    await act(async () => root.render(<EChartCanvas option={option} height={100} label="P" />));
    expect(instance.setOption.mock.lastCall?.[0].brush).toBeUndefined();
    expect(instance.setOption).toHaveBeenCalledTimes(3);
    expect(instance.dispatchAction).toHaveBeenCalledOnce();
    expect(instance.dispose).not.toHaveBeenCalled();
    await act(async () => root.unmount()); container.remove();
  });

  it("applies the option with the label as the aria description and disposes on unmount", async () => {
    const instance = fresh();
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const option = { series: [], aria: { label: { enabled: true } } };
    await act(async () => root.render(<EChartCanvas option={option} height={100} label="Latency" />));
    const [applied, opts] = instance.setOption.mock.calls[0];
    expect(applied.series).toEqual([]);
    expect(applied.aria).toEqual({ label: { enabled: true }, enabled: true, description: "Latency" });
    expect(opts).toEqual({ notMerge: true });
    expect(instance.dispose).not.toHaveBeenCalled();
    await act(async () => root.unmount());
    expect(instance.dispose).toHaveBeenCalledTimes(1);
  });

  it("forwards datum selections to the latest click handler without replacing the chart", async () => {
    const instance = fresh();
    const container = document.createElement("div"); document.body.append(container);
    const root = createRoot(container);
    const option = { series: [] };
    const first = vi.fn(); const next = vi.fn();
    await act(async () => root.render(<EChartCanvas option={option} height={100} label="Points" onClick={first} />));
    const click = instance.on.mock.calls.find(([name]) => name === "click")![1];
    const event = { data: { selection: { dimensions: { service: "cart" } } }, dataType: "node" };
    click(event);
    expect(first).toHaveBeenCalledWith(event);
    await act(async () => root.render(<EChartCanvas option={option} height={100} label="Points" onClick={next} />));
    click(event);
    expect(next).toHaveBeenCalledWith(event);
    expect(first).toHaveBeenCalledOnce();
    expect(instance.setOption).toHaveBeenCalledOnce();
    await act(async () => root.unmount());
  });

  it("disconnects a group only when its last chart leaves", async () => {
    const a = fresh();
    const b = fresh();
    const ca = document.createElement("div");
    const cb = document.createElement("div");
    document.body.append(ca, cb);
    const ra = createRoot(ca);
    const rb = createRoot(cb);
    await act(async () => ra.render(<EChartCanvas option={{}} height={1} label="a" group="g" />));
    await act(async () => rb.render(<EChartCanvas option={{}} height={1} label="b" group="g" />));
    expect(a.group).toBe("g");
    expect(b.group).toBe("g");
    await act(async () => ra.unmount());
    expect(a.group).toBe("");
    expect(mocks.disconnect).not.toHaveBeenCalled();
    await act(async () => rb.unmount());
    expect(mocks.disconnect).toHaveBeenCalledWith("g");
  });
});

it("W12 inspects rendered legend text bounds on demand", async () => {
 vi.stubEnv("DEV",true); window.history.replaceState({},"","/?__fanout_audit=1");
 const instance = fresh();
 Object.assign(instance, { getZr: () => ({ storage: { getDisplayList: () => [{ style: { text: "load-generator" }, getBoundingRect: () => ({ clone: () => ({ x: 10, y: 0, width: 100, height: 16, applyTransform: () => {} }) }), getComputedTransform: () => null }] } }) });
 const container = document.createElement("div"); document.body.append(container); const root = createRoot(container);
 await act(async () => root.render(<EChartCanvas option={{ legend: { type: "plain", show: true, data: ["load-generator"] }, grid: { top: 30 } }} height={100} label="Legend" />));
 const chart = container.querySelector<HTMLElement>("[role=img]")!;
 expect(window.__fanoutAudit!(chart.id)!.legend).toMatchObject({ type: "plain", names: ["load-generator"], entries: [{ name: "load-generator", text: "load-generator", left: 10, top: 0, right: 110, bottom: 16 }] });
 expect(window.__fanoutAudit!(chart.id)!.legendBottom).toBe(30);
 await act(async () => root.unmount()); container.remove(); window.history.replaceState({},"","/"); vi.unstubAllEnvs();
});
