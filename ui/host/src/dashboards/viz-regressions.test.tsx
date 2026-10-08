import { statusInk } from "../../../panels/style";
import { MantineProvider } from "@mantine/core";
import { act, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Frame, Panel, PanelResult } from "../../../panels/types";
import type { AnnotationsResponse } from "../../../panels/annotations";
import { warn, bad, ok } from "../../../tokens";

const mocks = vi.hoisted(() => ({ init: vi.fn() }));
vi.mock("echarts/core", () => ({ init: mocks.init, use: vi.fn(), connect: vi.fn(), disconnect: vi.fn() }));
vi.mock("echarts/charts", () => ({ BarChart: {}, GaugeChart: {}, GraphChart: {}, LineChart: {}, CustomChart: {}, HeatmapChart: {}, ScatterChart: {} }));
vi.mock("echarts/components", () => ({ AriaComponent: {}, BrushComponent: {}, DataZoomComponent: {}, GraphicComponent: {}, GridComponent: {}, LegendComponent: {}, MarkAreaComponent: {}, MarkLineComponent: {}, ToolboxComponent: {}, TooltipComponent: {}, VisualMapComponent: {} }));
vi.mock("echarts/renderers", () => ({ CanvasRenderer: {} }));
import { BarViz } from "./viz/bar";
import { TimeseriesViz } from "./viz/timeseries";
import { GaugeViz } from "./viz/gauge";
import { StatViz } from "./viz/stat";
import { TextViz } from "./viz/text";
import { TableViz } from "./viz/table";
import { PanelCard } from "./panel-card";
import { EChartCanvas } from "./echart-canvas";
import { Viz } from "./viz";
import { ApiError } from "./api";
import { retryQuery } from "./query-policy";
import { PanelData } from "./inspect";

const frame: Frame = { columns: [{ name: "time", type: "time", role: "time" }, { name: "count", type: "number", role: "measure" }], values: [[1, 2], [20, 40]], rows: 2, totals: [null, 120] };
const panel: Panel = { id: "p", title: "For $service", viz: "stat", reduce: "window" };
const result: PanelResult = { id: "p", status: "ok", frame, elapsed_ms: 1 };
const cleanups: (() => void)[] = [];
let instance: { setOption: ReturnType<typeof vi.fn>; on: ReturnType<typeof vi.fn>; dispose: ReturnType<typeof vi.fn>; resize: ReturnType<typeof vi.fn> };
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  instance = { setOption: vi.fn(), on: vi.fn(), dispose: vi.fn(), resize: vi.fn() };
  mocks.init.mockReturnValue(instance);
});
afterEach(async () => {
  await act(async () => { cleanups.splice(0).forEach((fn) => fn()); });
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
});
async function render(node: ReactNode) {
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host); cleanups.push(() => root.unmount());
  const rerender = async (next: ReactNode) => { await act(async () => { root.render(<MantineProvider theme={{ colors: { warn: [...warn], bad: [...bad], ok: [...ok] } }}>{next}</MantineProvider>); }); };
  await rerender(node);
  return { host, rerender };
}

describe("visualization regressions", () => {
  it.each(["stat", "gauge"] as const)("switches folded %s views through a checked menu radio group at 300 px", async viz => {
    const measure = vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockReturnValue({ x: 0, y: 0, left: 0, right: 300, top: 0, bottom: 200, width: 300, height: 200, toJSON() {} });
    try {
      const { host } = await render(<PanelCard panel={{ ...panel, viz }} title="Calls" result={result} loading={false} height={200} group="d" editing={false} agentAvailable onView={vi.fn()} onCopyLink={vi.fn()} onExplain={vi.fn()} />);
      const open = async () => {
        await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Calls menu"]')!.click());
        await vi.waitFor(() => expect(document.querySelector('[role="menu"]')).not.toBeNull());
      };
      await open();
      const choices = () => [...document.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]')];
      expect(choices().map(item => item.textContent)).toEqual(["Chart", "Data", "Spec"]);
      expect(choices().map(item => item.getAttribute("aria-checked"))).toEqual(["true", "false", "false"]);
      expect(document.querySelector('[role="group"][aria-label="Calls view"]')?.contains(choices()[0])).toBe(true);
      const items = [...document.querySelectorAll('[data-menu-item]')].map(item => item.textContent);
      expect(items).toEqual(["Chart", "Data", "Spec", "View", "Explain in chat", "Copy link"]);
      await act(async () => choices()[1].click());
      expect(host.querySelector('[data-panel-data]')).not.toBeNull();
      await open();
      expect(choices().map(item => item.getAttribute("aria-checked"))).toEqual(["false", "true", "false"]);
      await act(async () => choices()[2].click());
      expect(host.querySelector('[data-panel-spec]')).not.toBeNull();
      await open();
      await act(async () => choices()[0].click());
      expect(host.querySelector('[data-panel-data]')).toBeNull();
      expect(host.querySelector('[data-panel-spec]')).toBeNull();
    } finally { measure.mockRestore(); }
  });

  it("offers only Spec in the text panel view menu", async () => {
    const { host } = await render(<PanelCard panel={{ ...panel, viz: "text", content: "Text content" }} title="Note" loading={false} height={200} group="d" editing={false} agentAvailable={false} onView={vi.fn()} onCopyLink={vi.fn()} onExplain={vi.fn()} />);
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Note menu"]')!.click());
    await vi.waitFor(() => expect(document.querySelector('[role="menu"]')).not.toBeNull());
    const choices = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitemradio"]')];
    expect(choices.map(item => item.textContent)).toEqual(["Spec"]);
    await act(async () => choices[0].click());
    expect(host.querySelector('[data-panel-spec]')).not.toBeNull();
  });

  it.each(["bar", "timeseries", "gauge", "heatmap", "histogram", "scatter", "state_timeline", "service_map"] as const)("fits %s into the flex body including the split note", async (viz) => {
    const { host } = await render(<PanelCard panel={{ ...panel, viz }} title="Chart" result={{ ...result, frame: { ...frame, note: "Split at 2026-10-06T19:30:58.554969Z · cart 2.3.0" } }} loading={false} height={300} group="d" editing={false} agentAvailable={false} onView={vi.fn()} onCopyLink={vi.fn()} onExplain={vi.fn()} />);
    const canvas = host.querySelector<HTMLElement>(viz === 'service_map' ? '[data-service-viewport]' : '[role="img"]')!;
    const body = host.querySelector<HTMLElement>('[data-panel="p"]')!.children[1] as HTMLElement;
    expect(body.style.overflow).toBe("hidden");
    expect(body.style.display).toBe("flex");
    expect(parseFloat(body.style.minHeight)).toBe(0);
    expect((viz === "service_map" ? canvas.parentElement! : canvas).style.flex).toBe("1 1 auto");
    expect(parseFloat(canvas.style.minHeight)).toBe(0);
    if (viz === "service_map") expect(parseFloat(canvas.parentElement!.style.minHeight)).toBe(0);
  });

  it("formats the deploy split in the viewer locale and retains the exact timestamp in its title", async () => {
    const NativeFormatter = Intl.DateTimeFormat;
    vi.spyOn(Intl, "DateTimeFormat").mockImplementation(function (_locale, options) {
      return new NativeFormatter("en-US", { ...options, timeZone: "America/Los_Angeles" });
    });
    try {
      const note = "Split at 2026-10-06T19:30:58.554969Z · cart 2.3.0";
      const { host } = await render(<PanelCard panel={{ ...panel, viz: "bar" }} title="Split" result={{ ...result, frame: { ...frame, note } }} loading={false} height={300} group="d" editing={false} agentAvailable={false} onView={vi.fn()} onCopyLink={vi.fn()} onExplain={vi.fn()} />);
      const status = host.querySelector('[role="status"]')!;
      expect(status.textContent).toBe("Split at Oct 6, 12:30 PM · cart 2.3.0");
      expect(status.getAttribute("title")).toBe(note);
    } finally { vi.restoreAllMocks(); }
  });

  it("shows Error logs warn at 3 and ok at 0", async () => {
    const p: Panel = { ...panel, id: "error_logs", title: "Error logs", better: "lower", thresholds: [{ value: 1, status: "warn" }] };
    const r = (count: number): PanelResult => ({ ...result, frame: { ...frame, totals: [null, count] } });
    const { host, rerender } = await render(<StatViz panel={p} result={r(3)} />);
    expect(host.textContent).toContain("Degraded");
    await rerender(<StatViz panel={p} result={r(0)} />);
    expect(host.textContent).toContain("Healthy");
  });
  it("colours SQL stat thresholds without a direction and honors result then spec", async () => {
    const p: Panel = { ...panel, sql: "SELECT count(*) FROM logs", thresholds: [{ value: 50, status: "warn" }, { value: 100, status: "bad" }] };
    const { host, rerender } = await render(<StatViz panel={p} result={result} />);
    expect(host.textContent).toContain("Unhealthy");
    await rerender(<StatViz panel={p} result={{ ...result, better: "higher" }} />);
    expect(host.textContent).toContain("Healthy");
    await rerender(<StatViz panel={{ ...p, better: "lower" }} result={{ ...result, better: "higher" }} />);
    expect(host.textContent).toContain("Unhealthy");
  });
  it("uses threshold-derived gauge bands and result direction", async () => {
    const p: Panel = { ...panel, viz: "gauge", min: 0, max: 200, thresholds: [{ value: 50, status: "warn" }, { value: 100, status: "bad" }] };
    const { rerender } = await render(<GaugeViz panel={p} result={result} dark={false} height={200} />);
    expect(instance.setOption.mock.lastCall?.[0].series[0].axisLine.lineStyle.color).toHaveLength(3);
    await rerender(<GaugeViz panel={p} result={{ ...result, better: "higher" }} dark={false} height={200} />);
    expect(instance.setOption.mock.lastCall?.[0].series[0].axisLine.lineStyle.color).toHaveLength(2);
  });
  it("keeps dimension cells nowrap with full titles and only the panel scrollbar", async () => {
    const service = "checkout-service-with-a-long-unbroken-name";
    const tableFrame: Frame = { columns: [{ name: "service", type: "string", role: "dimension" }, frame.columns[1]], values: [[service], [1]], rows: 1 };
    const { host } = await render(<PanelCard panel={{ ...panel, viz: "table" }} title="Services" result={{ ...result, frame: tableFrame }} loading={false} height={200} group="d" editing={false} agentAvailable={false} onView={vi.fn()} onCopyLink={vi.fn()} onExplain={vi.fn()} />);
    const cell = host.querySelector("tbody td p")!;
    expect(cell.classList.contains("dashboard-dimension-nowrap")).toBe(true);
    expect(cell.getAttribute("title")).toBe(service);
    const table = host.querySelector("table")!;
    expect(table.classList.contains("dashboard-table")).toBe(true);
    let scrollContainers = 0;
    for (let element: HTMLElement | null = table.parentElement; element && element !== host; element = element.parentElement) {
      if ([element.style.overflow, element.style.overflowY].some((v) => v === "auto" || v === "scroll")) scrollContainers += 1;
    }
    expect(scrollContainers).toBe(1);
  });
  it("formats count columns without ms in tables, stats, gauges and inspect", async () => {
    const countFrame: Frame = { columns: [{ name: "count_distinct", type: "number", role: "measure", unit: "count" }], values: [[1]], rows: 1, totals: [1] };
    const r = { ...result, frame: countFrame };
    const p = { ...panel, unit: "ms" as const };
    const { host } = await render(<><StatViz panel={p} result={r} /><TableViz panel={{ ...p, viz: "table" }} result={r} height={200} /><GaugeViz panel={{ ...p, viz: "gauge" }} result={r} dark={false} height={200} /><PanelData panel={p} result={r} /></>);
    expect(host.querySelector("tbody td p")!.textContent).toBe("1");
    expect(host.querySelector("p")!.textContent).toBe("1");
    expect(instance.setOption.mock.lastCall?.[0].series[0].detail.formatter()).toBe("1");
    expect(document.querySelector('[data-panel-data] tbody td')!.textContent).toBe("1");
  });
  it.each([undefined, { ...frame, totals: undefined }])("hides window delta without previous totals (%s)", async (previous) => {
    const { host } = await render(<StatViz panel={panel} result={{ ...result, previous }} />);
    expect(host.textContent).toContain("120");
    expect(host.textContent).not.toContain("vs previous period");
    expect(host.textContent).not.toContain("%");
  });
  it("uses the previous window total for the correct delta", async () => {
    const { host } = await render(<StatViz panel={panel} result={{ ...result, previous: { ...frame, totals: [null, 60] } }} />);
    expect(host.textContent).toContain("+100%");
  });
  it.each(["bar", "timeseries", "gauge", "heatmap", "histogram", "scatter", "state_timeline", "service_map"] as const)("does not setOption again for an equal frame and new elapsed_ms (%s)", async (viz) => {
    const p = { ...panel, viz };
    const node = (r: PanelResult) => viz === "bar" ? <BarViz panel={p} result={r} dark={false} height={200} /> : viz === "gauge" ? <GaugeViz panel={p} result={r} dark={false} height={200} /> : <Viz panel={p} result={r} dark={false} height={200} group="d" />;
    const { rerender } = await render(node(result));
    expect(instance.setOption).toHaveBeenCalledTimes(viz === "service_map" ? 0 : 1);
    await rerender(node({ ...result, elapsed_ms: 99 }));
    expect(instance.setOption).toHaveBeenCalledTimes(viz === "service_map" ? 0 : 1);
  });
  it("memoizes the health trend across unrelated result metadata changes", async () => {
    const healthFrame: Frame = { ...frame, health: { health: "healthy", counts: { healthy: 1, degraded: 0, unhealthy: 0 }, total_spans: 10, error_rate: 0, service_count: 1, error_trend: [0, 1] } };
    const p: Panel = { ...panel, viz: "health" };
    const r = { ...result, frame: healthFrame };
    const { rerender } = await render(<Viz panel={p} result={r} dark={false} height={200} group="d" />);
    expect(instance.setOption).toHaveBeenCalledOnce();
    await rerender(<Viz panel={p} result={{ ...r, elapsed_ms: 99 }} dark={false} height={200} group="d" />);
    expect(instance.setOption).toHaveBeenCalledOnce();
  });
  it.each(["timeseries", "heatmap", "state_timeline"] as const)("threads annotations through the card and memoizes options on %s", async viz => {
    const p: Panel = { ...panel, viz };
    const r: PanelResult = { ...result, from_ms: 0, to_ms: 10000, frame: { ...frame, note: "Deploy split unavailable." }, annotation_scope: { services: [{ namespace: "shop", service: "checkout" }], limited: true } };
    const annotations: AnnotationsResponse = {
      deploys: [{ namespace: "shop", service: "checkout", version: "v2", at: new Date(1000).toISOString() }, { namespace: "shop", service: "frontend", version: "v3", at: new Date(2000).toISOString() }],
      anomalies: [{ namespace: "shop", service: "checkout", kind: "latency", from: new Date(3000).toISOString(), to: new Date(5000).toISOString(), title: "Slow", severity: "bad" }],
    };
    const vars = { service: "frontend" };
    const node = (next: PanelResult, history = annotations, values = vars) => <PanelCard panel={p} title="Time" result={next} annotations={history} vars={values} loading={false} height={200} group="d" editing={false} agentAvailable={false} onView={vi.fn()} onCopyLink={vi.fn()} onExplain={vi.fn()} />;
    const { host, rerender } = await render(node(r));
    const option = () => instance.setOption.mock.lastCall![0];
    expect(option().series[0].markLine.data).toHaveLength(1); expect(option().series[0].markArea.data).toHaveLength(1);
    expect([...host.querySelectorAll('[role="status"]')].map(element => element.textContent)).toEqual(["Deploy split unavailable.", "Annotation service scope is limited."]);
    await rerender(node({ ...r, elapsed_ms: 99 })); expect(instance.setOption).toHaveBeenCalledOnce();
    await rerender(node(r, annotations, { service: "checkout" })); expect(instance.setOption).toHaveBeenCalledOnce();
    await rerender(node(r, { ...annotations, deploys: [] })); expect(option().series[0].markLine.data).toHaveLength(0);
    await rerender(node({ ...r, annotation_scope: undefined })); expect(option().series[0].markLine.data).toHaveLength(2);
    await rerender(node({ ...r, from_ms: 2000 })); expect(option().series[0].markLine.data).toHaveLength(0);
    await rerender(node({ ...r, to_ms: 1000 })); expect(option().series[0].markLine.data).toHaveLength(0); expect(option().series[0].markArea.data).toHaveLength(0);
    await rerender(node({ ...r, annotation_error: "Scope unavailable" }));
    expect(option().series[0].markLine).toBeUndefined();
    expect([...host.querySelectorAll('[role="status"]')].map(element => element.textContent)).toContain("Scope unavailable");
  });
  it("forwards point and dimension selections from the chart datum", async () => {
    const onPoint = vi.fn();
    const onSelect = vi.fn();
    await render(<Viz panel={{ ...panel, viz: "scatter" }} result={result} dark={false} height={200} group="d" onPoint={onPoint} onSelect={onSelect} />);
    const selection = { dimensions: { service: "checkout" }, time: 1000, bucket: { lower: 2, upper: 10 } };
    instance.on.mock.calls.find(([name]) => name === "click")![1]({ data: { selection } });
    expect(onPoint).toHaveBeenCalledWith(selection);
    expect(onSelect).toHaveBeenCalledWith("checkout");
  });
  it("retains legend selections across data changes only for surviving series", async () => {
    const { rerender } = await render(<EChartCanvas label="Services" height={100} option={{ legend: {}, series: [{ name: "cart" }, { name: "gone" }] }} />);
    const handler = instance.on.mock.calls.find(([name]) => name === "legendselectchanged")?.[1];
    expect(handler).toBeTypeOf("function");
    handler({ selected: { cart: false, gone: false } });
    await rerender(<EChartCanvas label="Services" height={100} option={{ legend: {}, series: [{ name: "cart", data: [1] }, { name: "new", data: [2] }] }} />);
    expect(instance.setOption.mock.lastCall).toEqual([expect.objectContaining({ legend: { selected: { cart: false } } }), { notMerge: true }]);
    await rerender(<EChartCanvas label="Services" height={100} option={{ legend: {}, series: [{ name: "gone" }] }} />);
    expect(instance.setOption.mock.lastCall?.[0].legend.selected).toEqual({});
  });
  it("keeps raw HTML inert, unsafe links sanitized, images absent and external links isolated", async () => {
    const content = '<script>alert(1)</script>\n\n<img src="https://remote.test/leak">\n\n![leak](https://remote.test/leak)\n\n[unsafe](javascript:alert%281%29) [data](data:text/html,test) [external](https://example.com)';
    const { host } = await render(<TextViz panel={{ ...panel, viz: "text", content }} />);
    expect(host.querySelector("script, img")).toBeNull();
    expect(host.textContent).not.toContain("alert(1)");
    const links = [...host.querySelectorAll("a")];
    expect(links).toHaveLength(3);
    expect(links[0].getAttribute("href")).toBe("");
    expect(links[1].getAttribute("href")).toBe("");
    expect(links[2].getAttribute("href")).toBe("https://example.com");
    expect(links[2].target).toBe("_blank");
    expect(links[2].rel).toBe("noopener noreferrer");
  });
  it("does not select a measure name on an ungrouped timeseries click", async () => {
    const select = vi.fn();
    await render(<TimeseriesViz panel={{ ...panel, viz: "timeseries" }} result={result} dark={false} height={200} group="d" onSelect={select} />);
    instance.on.mock.calls.find(([name]) => name === "click")![1]({ seriesName: "count" });
    expect(select).not.toHaveBeenCalled();
  });
  it("colours only unhealthy first measures and prefers column units", async () => {
    const tableFrame: Frame = { columns: [{ name: "service", type: "string", role: "dimension" }, { name: "latency", type: "number", role: "measure", unit: "ms" }, { name: "size", type: "number", role: "measure", unit: "bytes" }], values: [["cart", "checkout"], [900, 40], [2048, 1024]], rows: 2 };
    const { host } = await render(<TableViz panel={{ ...panel, viz: "table", better: "lower", unit: "count", thresholds: [{ value: 100, status: "warn" }] }} result={{ ...result, frame: tableFrame }} height={200} />);
    const cells = host.querySelectorAll("tbody tr:first-child td p");
    expect(cells[1].textContent).toBe("900ms");
    expect(cells[1].getAttribute("style")).toContain(statusInk("warn",false));
    expect(cells[2].textContent).toBe("2.0 KiB");
    expect(cells[2].getAttribute("style") ?? "").not.toMatch(/warn|bad|ok/);
    expect(host.querySelector("tbody tr:nth-child(2) td:nth-child(2) p")!.getAttribute("style") ?? "").not.toMatch(/warn|bad|ok/);
  });
  it("announces table sorting and supports keyboard row selection", async () => {
    const select = vi.fn();
    const grouped: Frame = { columns: [{ name: "service", type: "string", role: "dimension" }, frame.columns[1]], values: [["cart", "checkout"], [40, 20]], rows: 2 };
    const { host } = await render(<TableViz panel={{ ...panel, viz: "table" }} result={{ ...result, frame: grouped }} height={200} onSelect={select} />);
    const header = host.querySelector("th:nth-child(2)")!;
    expect(header.getAttribute("aria-sort")).toBe("none");
    await act(async () => { header.querySelector("button")!.click(); });
    expect(header.getAttribute("aria-sort")).toBe("descending");
    const row = host.querySelector("tbody tr")!;
    expect(row.getAttribute("tabindex")).toBe("0");
    for (const key of ["Enter", " "]) {
      const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true });
      await act(async () => { row.dispatchEvent(event); });
      expect(event.defaultPrevented).toBe(true);
    }
    expect(select.mock.calls).toEqual([["cart"], ["cart"]]);
  });
  it("uses interpolated chart labels, focusable descriptions, named loaders and non-scrolling chart bodies", async () => {
    const { host } = await render(<PanelCard panel={{ ...panel, viz: "gauge", description: "Details" }} title="For cart" result={result} loading height={200} group="d" editing={false} agentAvailable={false} onView={vi.fn()} onCopyLink={vi.fn()} onExplain={vi.fn()} />);
    expect(host.querySelector('[role="img"]')!.getAttribute("aria-label")).toBe("For cart: gauge");
    expect(host.querySelector('[aria-label="For cart description"]')!.tagName).toBe("BUTTON");
    expect(host.querySelector('[aria-label="Refreshing"]')).not.toBeNull();
    expect(host.querySelector('[role="img"]')!.parentElement!.style.overflow).toBe("hidden");
  });
  it("limits retries to two and never retries client ApiErrors", () => {
    for (const status of [400, 401, 403, 404, 409, 422, 499]) expect(retryQuery(0, new ApiError("bad", status))).toBe(false);
    for (const error of [new ApiError("server", 500), new Error("network")]) {
      expect(retryQuery(0, error)).toBe(true);
      expect(retryQuery(1, error)).toBe(true);
      expect(retryQuery(2, error)).toBe(false);
    }
  });
});

import { makeDrill, parseDrill } from "./drill-state";
it("grouped bar clicks carry both telemetry dimensions", async () => {
 const p: Panel = {...panel,viz:"bar",drill:"traces",query:{from:"spans",by:["service","operation"],measures:["count()"]}};
 const r: PanelResult = {...result,from_ms:100000,to_ms:500000,frame:{columns:[{name:"service",type:"string",role:"dimension"},{name:"operation",type:"string",role:"dimension"},{name:"count",type:"number",role:"measure"}],values:[["checkout"],["PlaceOrder"],[9]],rows:1}};
 const onPoint = vi.fn();
 await render(<BarViz panel={p} result={r} dark={false} height={200} onPoint={onPoint}/>);
 const data = instance.setOption.mock.lastCall![0].series[0].data[0];
 instance.on.mock.calls.find(([name])=>name==="click")![1]({name:"checkout",seriesName:"PlaceOrder",data});
 expect(onPoint).toHaveBeenCalledWith({dimensions:{service:"checkout",operation:"PlaceOrder"}});
 expect(makeDrill(p,r,onPoint.mock.lastCall![0])?.dimensions).toEqual({service:"checkout",operation:"PlaceOrder"});
});
it.each(["Before deploy","Since deploy"])("deploy bar clicks carry the %s window without a synthetic dimension",async period=>{
 const p: Panel = {...panel,viz:"bar",drill:"traces",query:{from:"spans",by:["service"],measures:["count()"]},options:{split:"deploy"}};
 const periods = JSON.parse('{"Before deploy":{"from":"1970-01-01T00:01:40Z","to":"1970-01-01T00:05:00.123456789Z"},"Since deploy":{"from":"1970-01-01T00:05:00.123456789Z","to":"1970-01-01T00:08:20Z"}}');
 const r: PanelResult = {...result,from_ms:100000,to_ms:500000,frame:{columns:[{name:"service",type:"string",role:"dimension"},{name:"period",type:"string",role:"dimension"},{name:"count",type:"number",role:"measure"}],values:[["checkout"],[period],[9]],rows:1,...{periods}}};
 const onPoint = vi.fn();
 await render(<BarViz panel={p} result={r} dark={false} height={200} onPoint={onPoint}/>);
 const data = instance.setOption.mock.lastCall![0].series[0].data[0];
 instance.on.mock.calls.find(([name])=>name==="click")![1]({name:"checkout",seriesName:period,data});
 const target = makeDrill(p,r,onPoint.mock.lastCall![0]);
 expect(target?.from).toBe(periods[period].from);
 expect(target?.to).toBe(periods[period].to);
 expect(target?.dimensions).toEqual({service:"checkout"});
 expect(parseDrill(JSON.stringify(target))).toEqual(target);
});
