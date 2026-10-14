import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { setPlatformAPI, use, type EChartsCoreOption, type EChartsType } from "echarts/core";
import { SVGRenderer } from "echarts/renderers";
import { chartThemeFor, timeseriesOption } from "../../../panels/compile";
import { nativeAudit } from "./chart-measurements";
import { EChartCanvas } from "./echart-canvas";

const native = vi.hoisted(() => ({ chart: null as EChartsType | null, height: 210, resize: () => {} }));
// Exercise the real engine and component events without a native canvas library.
vi.mock("echarts/core", async importOriginal => {
  const core = await importOriginal<typeof import("echarts/core")>();
  return { ...core, init: (element: HTMLElement) => {
    native.chart = core.init(element, undefined, { renderer: "svg", width: 320, height: native.height });
    return native.chart;
  } };
});
use([SVGRenderer]);
setPlatformAPI({ measureText: text => ({ width: String(text ?? "").length * 7 }) });

beforeEach(() => {
  native.height = 210;
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("ResizeObserver", class {
    constructor(callback: () => void) { native.resize = callback; }
    observe() {}
    disconnect() {}
  });
  vi.spyOn(HTMLCanvasElement.prototype, "getContext").mockReturnValue(null);
});
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); document.body.innerHTML = ""; native.chart = null; });

function option(dark = false, values = [[0, 1000], [1000, 2000], [1, 10000]]): EChartsCoreOption {
  return { ...timeseriesOption({
    id: "p", title: "Latency", viz: "timeseries", unit: "ms", options: { scale: "log" },
    thresholds: [100, 150, 200].map(value => ({ value, status: "bad", label: "Budget" })),
  }, {
    id: "p", status: "ok", elapsed_ms: 0, from_ms: 0, to_ms: 1000,
    frame: {
      columns: [{ name: "time", type: "time", role: "time" }, ...["A", "B"].map(name => ({ name, type: "number" as const, role: "measure" as const, unit: "ms" }))],
      values, rows: 2,
    },
  }, chartThemeFor(dark), { width: 320, height: 210 }), animation: false };
}

function assertLabels(chart: EChartsType, compiled: EChartsCoreOption) {
  const rect = (chart as unknown as { getModel(): { getComponent(name: string): { coordinateSystem: { getRect(): { x: number; y: number; width: number; height: number } } } } }).getModel().getComponent("grid").coordinateSystem.getRect();
  const texts = nativeAudit(chart, compiled, { width: chart.getWidth(), height: chart.getHeight() }, rect).texts;
  const thresholds = texts.filter(text => text.text.startsWith("Budget "));
  expect(thresholds).toHaveLength(3);
  for (const [index, text] of thresholds.entries()) {
    expect(text.left).toBeGreaterThanOrEqual(rect.x);
    expect(text.right).toBeLessThanOrEqual(rect.x + rect.width);
    expect(text.top).toBeGreaterThanOrEqual(rect.y);
    expect(text.bottom).toBeLessThanOrEqual(rect.y + rect.height);
    for (const other of thresholds.slice(index + 1)) {
      expect(text.top < other.bottom && text.bottom > other.top, JSON.stringify([text, other])).toBe(false);
    }
  }
  // Direct end labels occupy the separate lane to the right of the plot.
  const ends = texts.filter(text => ["A", "B"].includes(text.text) && text.left >= rect.x + rect.width);
  expect(ends.length).toBeGreaterThan(0);
  for (const text of ends) {
    expect(text.top).toBeGreaterThanOrEqual(rect.y);
    expect(text.bottom).toBeLessThanOrEqual(rect.y + rect.height);
  }
}

it("keeps compact native log-chart labels contained and separate with default animations", async () => {
  native.height = 120;
  const container = document.createElement("div"); document.body.append(container); const root = createRoot(container);
  const names = ["A", "B", "C", "D"];
  const compiled = timeseriesOption({
    id: "p", title: "Latency", viz: "timeseries", unit: "ms", options: { scale: "log" },
    thresholds: [{ value: 100, status: "bad", label: "Budget" }],
  }, {
    id: "p", status: "ok", elapsed_ms: 0, from_ms: 0, to_ms: 1000,
    frame: {
      columns: [{ name: "time", type: "time", role: "time" }, ...names.map(name => ({ name, type: "number" as const, role: "measure" as const, unit: "ms" }))],
      values: [[0, 1000], [1, 100], [1, 100], [1, 100], [1, 10000]], rows: 2,
    },
  }, chartThemeFor(false), { width: 320, height: 120 });
  try {
    await act(async () => root.render(<EChartCanvas option={compiled} height={120} label="Latency" />));
    const chart = native.chart!;
    // Inspect the settled native renderer after its default line animations.
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 1200)); });
    const rect = (chart as unknown as { getModel(): { getComponent(name: string): { coordinateSystem: { getRect(): { x: number; y: number; width: number; height: number } } } } }).getModel().getComponent("grid").coordinateSystem.getRect();
    const texts = nativeAudit(chart, compiled, { width: chart.getWidth(), height: chart.getHeight() }, rect).texts;
    const ends = texts.filter(text => names.includes(text.text) && text.left >= rect.x + rect.width);
    const thresholds = texts.filter(text => text.text.startsWith("Budget "));
    expect(ends.map(text => text.text)).toContain("C");
    expect(thresholds).toHaveLength(1);
    const labels = [...ends, ...thresholds];
    for (const [index, text] of labels.entries()) {
      expect(text.top, text.text).toBeGreaterThanOrEqual(rect.y);
      expect(text.bottom, text.text).toBeLessThanOrEqual(rect.y + rect.height);
      for (const other of labels.slice(index + 1)) {
        expect(text.left < other.right && text.right > other.left && text.top < other.bottom && text.bottom > other.top, JSON.stringify([text, other])).toBe(false);
      }
    }
    for (const text of thresholds) {
      expect(text.left).toBeGreaterThanOrEqual(rect.x);
      expect(text.right).toBeLessThanOrEqual(rect.x + rect.width);
    }
  } finally { await act(async () => root.unmount()); }
});

it.each([false, true])("keeps native threshold and end labels inside the plot after legend toggles (dark=%s)", async dark => {
  const container = document.createElement("div"); document.body.append(container); const root = createRoot(container);
  const compiled = option(dark);
  try {
    await act(async () => root.render(<EChartCanvas option={compiled} height={210} label="Latency" />));
    const chart = native.chart!;
    assertLabels(chart, compiled);
    const before = chart.convertToPixel({ seriesIndex: 0 }, [0, 100]);
    await act(async () => chart.dispatchAction({ type: "legendToggleSelect", name: "B" }));
    expect(chart.convertToPixel({ seriesIndex: 0 }, [0, 100])).not.toEqual(before);
    expect((chart.getOption().legend as { selected: Record<string, boolean> }[])[0].selected.B).toBe(false);
    assertLabels(chart, compiled);
    await act(async () => chart.dispatchAction({ type: "legendToggleSelect", name: "B" }));
    assertLabels(chart, compiled);
    expect(native.chart).toBe(chart);
  } finally { await act(async () => root.unmount()); }
});

it("keeps native labels inside the plot after dataZoom and its reset", async () => {
  const container = document.createElement("div"); document.body.append(container); const root = createRoot(container);
  const compiled = { ...option(false, [[0, 1000], [1000, 100], [1, 200]]), dataZoom: [{ type: "inside", xAxisIndex: 0, filterMode: "filter" }] };
  try {
    await act(async () => root.render(<EChartCanvas option={compiled} height={210} label="Latency" />));
    const chart = native.chart!;
    const before = chart.convertToPixel({ seriesIndex: 0 }, [0, 100]);
    await act(async () => chart.dispatchAction({ type: "dataZoom", start: 90, end: 100 }));
    expect(chart.convertToPixel({ seriesIndex: 0 }, [0, 100])).not.toEqual(before);
    expect((chart.getOption().dataZoom as { start: number }[])[0].start).toBe(90);
    assertLabels(chart, compiled);
    await act(async () => chart.dispatchAction({ type: "dataZoom", start: 0, end: 100 }));
    assertLabels(chart, compiled);
  } finally { await act(async () => root.unmount()); }
});

it.each([false, true])("re-lays out native labels after resize (responsive=%s)", async responsive => {
  const container = document.createElement("div"); document.body.append(container); const root = createRoot(container);
  const compiled = option();
  let height = 210;
  vi.spyOn(HTMLElement.prototype, "clientWidth", "get").mockReturnValue(320);
  vi.spyOn(HTMLElement.prototype, "clientHeight", "get").mockImplementation(() => height);
  try {
    await act(async () => root.render(<EChartCanvas option={compiled} optionForSize={responsive ? () => compiled : undefined} height={210} label="Latency" />));
    const chart = native.chart!;
    // Let ECharts' resize path read the same dimensions as the component.
    const resize = chart.resize.bind(chart);
    vi.spyOn(chart, "resize").mockImplementation(() => { resize({ width: 320, height }); });
    height = 150;
    await act(async () => native.resize());
    assertLabels(chart, compiled);
    height = 210;
    await act(async () => native.resize());
    assertLabels(chart, compiled);
  } finally { await act(async () => root.unmount()); }
});
