import { within } from "@testing-library/dom";
import { statusInk } from "../../../panels/style";
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PanelGrid } from "./grid";
import { warn } from "../../../tokens";
import type { DashboardSpec, PanelResult } from "../../../panels/types";

const charts = vi.hoisted(() => ({ calls: [] as { label: string; option: Record<string, unknown>; height: number; group?: string; onClick?: (p: { name: string; seriesName: string }) => void; onZoom?: (from: number, to: number) => void }[] }));
vi.mock("./echart-canvas", () => ({ EChartCanvas: ({ label, option, height, group, onClick, onZoom }: { label: string; option: Record<string, unknown>; height: number; group?: string; onClick?: (p: { name: string; seriesName: string }) => void; onZoom?: (from: number, to: number) => void }) => {
  charts.calls.push({ label, option, height, group, onClick, onZoom });
  return <button data-chart={label} onClick={() => onClick?.({ name: "cart", seriesName: "cart" })}>{label}</button>;
} }));


const spec: DashboardSpec = {
  version: 1, name: "Shop", time: { range: "1h" },
  variables: [{ name: "service", kind: "query", from: "spans", field: "service" }],
  panels: [
    { id: "requests", title: "Requests", viz: "stat", better: "lower", unit: "count", thresholds: [{ value: 100, status: "warn" }], query: { from: "spans", measures: ["count()"] }, grid: { x: 0, y: 0, w: 3, h: 3 } },
    { id: "by_service", title: "By service", viz: "bar", click: { set_variable: "service" }, query: { from: "spans", measures: ["count()"], by: ["service"] }, grid: { x: 3, y: 0, w: 6, h: 6 } },
    { id: "slow", title: "Slow", viz: "table", better: "lower", query: { from: "spans", measures: ["p95(duration_ms)"], by: ["http_route"] }, grid: { x: 0, y: 6, w: 12, h: 6 } },
    { id: "broken", title: "Broken", viz: "timeseries", query: { from: "spans", measures: ["count()"], bucket: "auto" }, grid: { x: 9, y: 0, w: 3, h: 6 } },
    { id: "notes", title: "Notes", viz: "text", content: "**Checkout** is the money path.", grid: { x: 0, y: 3, w: 3, h: 3 } },
  ],
};

const results = new Map<string, PanelResult>([
  ["requests", { id: "requests", status: "ok", frame: { columns: [{ name: "time", type: "time", role: "time" }, { name: "count", type: "number", role: "measure", unit: "count" }], values: [[1, 2], [60, 80]], rows: 2, totals: [null, 140] }, previous: { columns: [{ name: "time", type: "time", role: "time" }, { name: "count", type: "number", role: "measure", unit: "count" }], values: [[1], [70]], rows: 1, totals: [null, 70] }, elapsed_ms: 4, sql: "SELECT 1" }],
  ["by_service", { id: "by_service", status: "ok", frame: { columns: [{ name: "service", type: "string", role: "dimension" }, { name: "count", type: "number", role: "measure", unit: "count" }], values: [["checkout", "cart"], [90, 50]], rows: 2 }, elapsed_ms: 3 }],
  ["slow", { id: "slow", status: "ok", frame: { columns: [{ name: "http_route", type: "string", role: "dimension" }, { name: "p95", type: "number", role: "measure", unit: "ms" }], values: [["/cart", "/quote"], [900, 40]], rows: 2 }, elapsed_ms: 3 }],
  ["broken", { id: "broken", status: "error", error: "Conversion Error: Could not convert string 'checkout' to INT32", elapsed_ms: 1 }],
]);

const cleanups: (() => void)[] = [];

beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  charts.calls = [];
  vi.stubGlobal("ResizeObserver", class {
    constructor(private callback: ResizeObserverCallback) {}
    observe(target: Element) { this.callback([{ target, contentRect: { width: 1200 } } as ResizeObserverEntry], this as unknown as ResizeObserver); }
    unobserve() {}
    disconnect() {}
  });
  vi.stubGlobal("IntersectionObserver", class { observe() {} unobserve() {} disconnect() {} });
});
afterEach(async () => { await act(async () => { cleanups.splice(0).forEach((cleanup) => cleanup()); }); vi.unstubAllGlobals(); document.body.innerHTML = ""; });

async function render(overrides: Partial<Parameters<typeof PanelGrid>[0]> = {}) {
  const host = document.createElement("div");
  document.body.append(host);
  const props = { dashboardId: "d1", version: 2, spec, vars: { service: "checkout" }, results, fetching: false, editing: false, agentAvailable: true, canManage: true, onOpenChat: vi.fn(), onVariable: vi.fn(), onView: vi.fn(), onVisible: vi.fn(), ...overrides };
  const root = createRoot(host);
  const client = new QueryClient();
  cleanups.push(() => { root.unmount(); client.clear(); });
  await act(async () => {
    root.render(<MantineProvider theme={{ colors: { warn: [...warn] } }}><QueryClientProvider client={client}><PanelGrid {...props} /></QueryClientProvider></MantineProvider>);
  });
  const rerender = async (overrides: Partial<Parameters<typeof PanelGrid>[0]>) => {
    await act(async () => { root.render(<MantineProvider theme={{ colors: { warn: [...warn] } }}><QueryClientProvider client={client}><PanelGrid {...props} {...overrides} /></QueryClientProvider></MantineProvider>); });
  };
  return { host, props, rerender };
}

describe("PanelGrid", () => {
  it("renders every visualization and state", async () => {
    const { host } = await render();
    expect(host.textContent).toContain("140");
    expect(host.textContent).toContain("Degraded");
    expect(host.textContent).not.toContain("+100%"); // Comparison is off.
    expect(host.textContent).toContain("/cart");
    expect(host.textContent).toContain("900ms");
    expect(host.textContent).toContain("Could not convert");
    expect(host.querySelector("strong")?.textContent).toBe("Checkout");
  });

  it("sets the variable when a bar is clicked", async () => {
    const { host, props } = await render();
    await act(async () => { (host.querySelector('[data-chart^="By service"]') as HTMLButtonElement).click(); });
    expect(props.onVariable).toHaveBeenCalledWith("service", "cart");
  });

  it("links and zooms only time charts, and wires point actions only for actionable panels", async () => {
    const onPoint = vi.fn(); const onZoom = vi.fn();
    const panels: DashboardSpec["panels"] = ["timeseries", "heatmap", "state_timeline", "histogram", "scatter", "bar"].map(viz => ({ id: viz, title: viz, viz: viz as DashboardSpec["panels"][number]["viz"], query: { from: "spans", measures: ["count()"], by: ["service"] } }));
    panels.push({ ...panels[5], id: "click", title: "Click", click: { set_variable: "service" }, time: { range: "15m", shift: "1h" } });
    panels.push({ ...panels[5], id: "drill", title: "Drill", drill: "traces" });
    const r = results.get("requests")!;
    const data = new Map(panels.map(p => [p.id, { ...r, id: p.id }]));
    const { host } = await render({ spec: { ...spec, panels }, results: data, onPoint, onZoom });
    for (const call of charts.calls) {
      const time = ["timeseries", "heatmap", "state_timeline"].some(viz => call.label.startsWith(`${viz}:`));
      expect(call.group).toBe(time ? "dashboard-d1" : undefined);
      expect(call.onZoom).toBe(time ? onZoom : undefined);
      expect(Boolean(call.onClick)).toBe(call.label.startsWith("Click:") || call.label.startsWith("Drill:"));
    }
    expect(host.querySelector('[data-panel="click"] [role="status"]')?.textContent).toBe("Range 15m · Shifted 1h");
    charts.calls.find(call => call.label.startsWith("Click:"))!.onClick!({ name: "cart", seriesName: "cart" });
    expect(onPoint).toHaveBeenCalledWith(panels[6], { time: undefined, dimensions: { service: "cart" } });
  });

  it("renders gauge and successful time series with stable chart options and the server shift", async () => {
    const time = results.get("requests")!;
    const extraSpec: DashboardSpec = { ...spec, panels: [
      ...spec.panels,
      { ...spec.panels[0], id: "gauge", title: "Gauge", viz: "gauge" },
      { ...spec.panels[3], id: "series", title: "Series", click: { set_variable: "service" } },
    ] };
    const extraResults = new Map(results);
    extraResults.set("gauge", { ...time, id: "gauge" });
    extraResults.set("series", { ...time, previous: { ...time.previous!, columns: [...time.previous!.columns, { name: "service", type: "string", role: "dimension" }], values: [...time.previous!.values, ["cart", "cart"]] }, frame: { ...time.frame!, columns: [...time.frame!.columns, { name: "service", type: "string", role: "dimension" }], values: [...time.frame!.values, ["cart", "cart"]] }, id: "series", shift_ms: 1234 });
    const { host, props, rerender } = await render({ spec: extraSpec, results: extraResults });
    expect(host.querySelector('[data-chart="Gauge: gauge"]')).not.toBeNull();
    expect(host.querySelector('[data-chart="Series: time series"]')).not.toBeNull();
    const options = new Map(charts.calls.map((call) => [call.label, call.option]));
    const series = options.get("Series: time series")!.series as { name: string; data: number[][] }[];
    expect(series.find((line) => line.name.endsWith(" · previous"))!.data).toEqual([[1235, 70]]);
    expect(charts.calls.find((call) => call.label === "Series: time series")!.group).toBe("dashboard-d1");
    charts.calls = [];
    await rerender({ fetching: true });
    for (const call of charts.calls) expect(call.option).toBe(options.get(call.label));
    await act(async () => { host.querySelector<HTMLButtonElement>('[data-chart="Series: time series"]')!.click(); });
    expect(props.onVariable).toHaveBeenCalledWith("service", "cart");
  });

  it("sorts table rows, formats units, colours thresholds and selects the first dimension", async () => {
    const table: DashboardSpec = { ...spec, panels: [{ ...spec.panels[2], thresholds: [{ value: 100, status: "warn" }], click: { set_variable: "service" } }] };
    const { host, props } = await render({ spec: table });
    const values = () => [...host.querySelectorAll("tbody tr")].map((row) => row.textContent);
    expect(values()).toEqual(["/cart900ms", "/quote40.0ms"]);
    expect(host.querySelector("tbody tr:first-child td:nth-child(2) p")!.getAttribute("style")).toContain(statusInk("warn",false));
    await act(async () => { host.querySelector<HTMLButtonElement>("thead th:nth-child(2) button")!.click(); });
    await act(async () => { host.querySelector<HTMLButtonElement>("thead th:nth-child(2) button")!.click(); });
    expect(values()).toEqual(["/quote40.0ms", "/cart900ms"]);
    await act(async () => { host.querySelector<HTMLTableRowElement>("tbody tr")!.click(); });
    expect(props.onVariable).toHaveBeenCalledWith("service", "/quote");
  });

  it("opens Data and Spec within the card with query and timing", async () => {
    const { host } = await render(); const card = host.querySelector('[data-panel="requests"]')!;
    await act(async () => card.querySelector<HTMLButtonElement>('[data-panel-view="Data"]')!.click());
    for (const value of ["1970-01-01T00:00:00.001Z", "SELECT 1", "Ran in 4 ms, 2 rows."]) expect(card.textContent).toContain(value);
    await act(async () => card.querySelector<HTMLButtonElement>('[data-panel-view="Spec"]')!.click());
    expect(card.querySelector('pre')!.textContent).toContain('"id": "requests"');
  });

  it("opens and closes the full-screen panel through URL view callbacks", async () => {
    const { host, props, rerender } = await render();
    await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Requests menu"]')!.click(); });
    await act(async () => { [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find((el) => el.textContent === "View")!.click(); });
    expect(props.onView).toHaveBeenCalledWith("requests");
    await rerender({ view: "requests" });
    const dialog = document.querySelector('[role="dialog"]')!;
    expect(dialog.textContent).toContain("140");
    await act(async () => { dialog.querySelector<HTMLButtonElement>('.mantine-Modal-close')!.click(); });
    expect(props.onView).toHaveBeenLastCalledWith(undefined);
  });

  it("keeps empty diagnoses and fetching loaders local to each panel", async () => {
    const extraResults = new Map(results);
    extraResults.set("requests", { id: "requests", status: "empty", diagnosis: "No requests here.", elapsed_ms: 0 });
    extraResults.delete("by_service");
    const { host } = await render({ results: extraResults, fetching: true, agentAvailable: false });
    expect(host.textContent).toContain("No requests here.");
    expect(host.querySelector('.mantine-Loader-root')).not.toBeNull();
    expect(host.textContent).not.toContain("Ask Fanout to fix it");
    expect(host.textContent).toContain("Could not convert");
    expect(host.querySelector("strong")!.textContent).toBe("Checkout");
  });

});


async function menu(host: HTMLElement, title: string, item: string) {
  await act(async () => { within(host).getByRole("button", {name: name => name === title + " menu" || name === title + " menu, refresh failed"}).click(); });
  await act(async () => { [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find((el) => el.textContent === item)!.click(); });
}

it("pins dashboard Explain to the executed panel window and keeps fix a separate edit", async () => {
  const observed = new Map(results);
  observed.set("broken", { ...results.get("broken")!, from_ms: 0, to_ms: 3600000, diagnosis: "Observed diagnosis", previous: { columns: [], values: [], rows: 0, truncated: true }, frame: { columns: [], values: [], rows: 0, truncated: false, note: "Partial result" } });
  const { host, props } = await render({ results: observed, time: { from: "2026-10-08T00:00:00.123456789Z", to: "2026-10-08T01:00:00.987654321Z" }, vars: { service: "$__all" }, staleAt: new Map([["broken", 42]]) });
  await menu(host, "Broken", "Explain in chat");
  const [prompt, options] = vi.mocked(props.onOpenChat).mock.calls[0];
  for (const value of ["dashboard id d1", "version 2", "panel id broken", "1970-01-01T00:00:00.000Z", "1970-01-01T01:00:00.000Z", '"service":"$__all"', "2026-10-08T00:00:00.123456789Z", "2026-10-08T01:00:00.987654321Z", "Observed diagnosis", '"stale_since":42', '"truncated":true', "Partial result", "Do not create, edit, replace or restore"]) expect(prompt).toContain(value);
  expect(options).toEqual({ answer_only: true });
  expect(prompt).not.toContain("Please fix");
  const fix = within(host).getByRole("button", {name: "Ask Fanout to fix it"});
  await act(async () => fix.click());
  const [edit, editOptions] = vi.mocked(props.onOpenChat).mock.calls[1];
  expect(edit).toContain("Please fix");
  expect(edit).toContain("version 2");
  expect(edit).toContain("Could not convert");
  expect(editOptions?.answer_only).not.toBe(true);
});

it("hides fix without manage permission while keeping Explain answer-only", async () => {
  const { host, props } = await render({ canManage: false });
  expect(host.textContent).not.toContain("Ask Fanout to fix it");
  await menu(host, "Broken", "Explain in chat");
  expect(vi.mocked(props.onOpenChat).mock.calls[0][1]).toEqual({ answer_only: true });
});

it("copies a view link without edit mode and reports success", async () => {
  const writeText = vi.fn().mockResolvedValue(undefined);
  vi.stubGlobal("navigator", { clipboard: { writeText } });
  window.history.replaceState({}, "", "/dashboards/d1?edit=1&range=6h");
  const { host } = await render();
  await menu(host, "Requests", "Copy link");
  expect(host.textContent).toContain("Link copied");
  const url = new URL(writeText.mock.calls[0][0]);
  expect(url.searchParams.get("edit")).toBeNull();
  expect(url.searchParams.get("range")).toBe("6h");
  expect(url.searchParams.get("view")).toBe("requests");
});

it("shows a copyable URL when clipboard permission is denied", async () => {
  vi.stubGlobal("navigator", { clipboard: { writeText: vi.fn().mockRejectedValue(new Error("Denied")) } });
  window.history.replaceState({}, "", "/dashboards/d1?edit=1");
  const { host } = await render();
  await menu(host, "Requests", "Copy link");
  expect(host.textContent).toContain("Copy this URL:");
  expect(host.textContent).toContain("view=requests");
  expect(host.querySelector('[role="status"]')!.textContent).not.toContain("edit=1");
});

it("resizes the full-screen chart, renders its title once and names close buttons", async () => {
  const { host, rerender } = await render({ view: "by_service" });
  const dialog = document.querySelector('[role="dialog"]')!;
  expect(dialog.querySelector('.mantine-Modal-title')).toBeNull();
  expect(dialog.querySelectorAll('[data-panel-title]')).toHaveLength(1);
  expect(dialog.querySelector('[aria-label="Close panel view"]')).not.toBeNull();
  const height = charts.calls.at(-1)!.height;
  vi.stubGlobal("innerHeight", window.innerHeight + 200);
  await act(async () => { window.dispatchEvent(new Event("resize")); });
  expect(charts.calls.at(-1)!.height).toBe(height + 200);
  await rerender({ view: undefined });
  await act(async () => host.querySelector<HTMLButtonElement>('[data-panel="requests"] [data-panel-view="Data"]')!.click());
  expect(host.querySelector('[data-panel="requests"] table')).not.toBeNull();
});


it("announces a truncated frame in the panel status line", async()=>{
 const {host}=await render({results:new Map([["requests",{...results.get("requests")!,frame:{...results.get("requests")!.frame!,truncated:true}}]])});
 expect(host.querySelector('[data-panel="requests"] [role="status"]')!.textContent).toContain("Truncated: showing limited data");
});
