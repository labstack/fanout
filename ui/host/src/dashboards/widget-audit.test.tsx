import { MantineProvider } from "@mantine/core";
import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { chartThemeFor, gaugeOption, timeseriesOption } from "../../../panels/compile";
import * as analysis from "../../../panels/analysis";
import * as rollups from "../../../panels/rollups";
import * as units from "../../../panels/units";
import { withAnnotations } from "../../../panels/annotations";
import type { Frame, Panel, PanelResult, Selection } from "../../../panels/types";
import type { DashboardSearch } from "./search";
import { useBrushZoom } from "./use-brush-zoom";
import { makeDrill } from "./drill-state";
import { PanelCard } from "./panel-card";
import { Viz } from "./viz";

vi.mock("./echart-canvas", () => ({ EChartCanvas: ({ onZoom, label }: { onZoom?: (from: number, to: number) => void; label: string }) => <div role="img" aria-label={label} onMouseUp={() => onZoom?.(1000, 5000)} /> }));
const panel: Panel = { id: "p", title: "Requests", viz: "timeseries" };
const frame: Frame = { columns: [{ name: "time", type: "time", role: "time" }, { name: "service", type: "string", role: "dimension" }, { name: "rate", type: "number", role: "measure", unit: "percent" }], values: [[0, 60000, 120000, 180000, 300000, 360000], ["cart", "cart", "cart", "cart", "cart", "cart"], [0, 0, 2, null, 0, Infinity]], rows: 6 };
const result: PanelResult = { id: "p", status: "ok", frame, interval: "1m", elapsed_ms: 1, from_ms: 0, to_ms: 420000 };
const cleanups: (() => void)[] = [];
beforeEach(() => vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true));
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(fn => fn())); vi.restoreAllMocks(); document.body.innerHTML = ""; });
async function render(node: React.ReactNode) {
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host); cleanups.push(() => root.unmount());
  await act(async () => root.render(<MantineProvider>{node}</MantineProvider>));
  return host;
}

describe("widget audit W1–W9", () => {
  it.each([false, true])("W1 fits the detail box inside the band and labels both endpoints (dark=%s)", dark => {
    const theme = chartThemeFor(dark);
    for (const size of [{ width: 270, height: 90 }, { width: 780, height: 90 }, { width: 500, height: 248 }]) {
      const option = gaugeOption({ ...panel, viz: "gauge", unit: "percent", min: 0, max: 10, thresholds: [{ value: 1, status: "warn" }, { value: 5, status: "bad" }] }, 1.72, theme, "percent", size);
      const gauge = (option.series as { radius: number; center: number[]; axisLine: { lineStyle: { width: number } }; pointer: { show: boolean }; progress: { show: boolean }; detail: { offsetCenter: number[]; width: number; height: number } }[])[0];
      const box = gauge.detail;
      expect(Math.hypot(box.width / 2 + Math.abs(box.offsetCenter[0]), box.height / 2 + Math.abs(box.offsetCenter[1]))).toBeLessThan(gauge.radius - gauge.axisLine.lineStyle.width);
      expect(gauge.radius).toBeGreaterThan(size.height < 100 ? 50 : 90);
      expect(gauge.pointer.show).toBe(false); expect(gauge.progress.show).toBe(true);
      const graphics = option.graphic as { style: { text: string } }[];
      expect(graphics.map(g => g.style.text)).toEqual(expect.arrayContaining(["0.00%", "10.0%", "■ Warn"]));
    }
  });
  it("W2 keeps toolbox out of time chart options", () => {
    const option = timeseriesOption(panel, result, chartThemeFor(false));
    expect(option.toolbox).toBeUndefined();
    const annotated = withAnnotations(option, panel, result, { deploys: [{ service: "cart", namespace: "", version: "2.3.0", at: new Date(1000).toISOString() }], anomalies: [] }, {}, chartThemeFor(false));
    expect(annotated.toolbox).toBeUndefined();
  });
  it("W2 shows the header reset after a brush and restores the original range with current filters", async () => {
    function Harness() {
      const [search, setSearch] = useState<DashboardSearch>({ range: "24h", vars: { service: "cart" } });
      const { zoom, resetZoom, zoomed } = useBrushZoom(search, setSearch);
      return <><output>{JSON.stringify(search)}</output><button onClick={() => setSearch(s => ({ ...s, vars: { service: "payment" } }))}>Filter</button><PanelCard panel={panel} title="Requests" result={result} loading={false} height={300} group="g" editing={false} agentAvailable={false} onView={vi.fn()} onInspect={vi.fn()} onCopyLink={vi.fn()} onExplain={vi.fn()} onZoom={zoom} zoomed={zoomed} onZoomReset={resetZoom} /></>;
    }
    const host = await render(<Harness />);
    expect(host.querySelector('[aria-label="Reset Requests zoom"]')).toBeNull();
    await act(async () => host.querySelector('[role="img"]')!.dispatchEvent(new MouseEvent("mouseup", { bubbles: true })));
    expect(JSON.parse(host.querySelector("output")!.textContent!).from).toBe(new Date(1000).toISOString());
    const reset = host.querySelector<HTMLButtonElement>('[aria-label="Reset Requests zoom"]')!;
    expect(reset).not.toBeNull(); expect(host.querySelector('[data-panel-body]')!.contains(reset)).toBe(false);
    await act(async () => host.querySelector<HTMLButtonElement>("button")!.click());
    await act(async () => reset.click());
    expect(JSON.parse(host.querySelector("output")!.textContent!)).toEqual({ range: "24h", vars: { service: "payment" } });
    expect(host.querySelector('[aria-label="Reset Requests zoom"]')).toBeNull();
  });
  it.each([false, true])("W3 reserves a surface-backed lane, truncates and collapses labels (dark=%s)", dark => {
    const theme = chartThemeFor(dark);
    const deploys = [1000, 1001, 400000].map(at => ({ service: "cart", namespace: "", version: "a-very-long-version-name", at: new Date(at).toISOString() }));
    const got = withAnnotations(timeseriesOption(panel, result, theme), panel, result, { deploys, anomalies: [] }, {}, theme, { width: 270, height: 248 });
    const grid = got.grid as { top: number };
    const series = got.series as { markLine: { data: { label: { show: boolean; formatter: string; position: string; distance: number; height: number; backgroundColor: string; overflow: string } }[] } }[];
    const labels = series[0].markLine.data.filter(mark => mark.label.show).map(mark => mark.label);
    expect(labels).toHaveLength(2); expect(grid.top).toBe(40);
    for (const label of labels) { expect(label.position).toBe("end"); expect(grid.top - label.distance).toBeLessThan(grid.top); expect(label.backgroundColor).toBe(theme.surface); expect(label.overflow).toBe("truncate"); }
    expect(labels[0].formatter).toContain("+1"); expect(labels[0].formatter).toContain("…");
  });
  it("W4 merges adjacent same-state buckets, preserves gaps and unknown, and draws only rounded rects", () => {
    const got = analysis.analysisOption({ ...panel, viz: "state_timeline", thresholds: [{ value: 1, status: "warn" }] }, result, chartThemeFor(true));
    const series = (got.series as { data: { value: (number | null)[]; itemStyle: { color: string } }[]; renderItem: (params: unknown, api: unknown) => { type: string; shape: { height: number; r: number }; style: object } }[])[0];
    expect(series.data.map(d => [d.value[0], d.value[3], d.value[4]])).toEqual([[0, 120000, 1], [120000, 180000, 2], [180000, 240000, 0], [300000, 360000, 1], [360000, 420000, 0]]);
    const selection = (series.data[0] as unknown as { selection: Selection }).selection;
    expect(makeDrill({ ...panel, viz: "state_timeline", drill: "traces" }, result, selection)).toMatchObject({ from: new Date(0).toISOString(), to: new Date(120000).toISOString() });
    expect(series.data[2].itemStyle.color).not.toBe(chartThemeFor(true).muted);
    const rect = series.renderItem({}, { value: (i: number) => series.data[0].value[i], coord: (v: number[]) => v, size: () => [0, 20], style: () => ({ fill: "green" }) });
    expect(rect.type).toBe("rect"); expect(rect.shape.height).toBe(13); expect(rect.shape.r).toBeGreaterThan(0);
    expect(rect.style).toHaveProperty("stroke", undefined); expect(series).not.toHaveProperty("symbol"); expect(got.series).toHaveLength(1);
  });
  it("W4 supplies a four-state icon legend in place of the unknown note", async () => {
    const host = await render(<Viz panel={{ ...panel, viz: "state_timeline" }} result={result} dark={false} height={248} group="g" />);
    expect(host.querySelector('[aria-label="State legend"]')?.textContent).toBe("● OK■ Warn◆ Bad○ Unknown");
    expect(host.textContent).not.toContain("Gaps and gray");
  });
  it.each([false, true])("W5 uses round duration intervals and recessive grids (dark=%s)", dark => {
    const theme = chartThemeFor(dark);
    const frame: Frame = { columns: [{ name: "operation", role: "dimension", type: "string" }, { name: "calls", role: "measure", type: "number", unit: "count" }, { name: "p95", role: "measure", type: "number", unit: "ms" }], values: [["a", "b"], [0, 10000], [0, 700000]], rows: 2 };
    const got = analysis.analysisOption({ ...panel, viz: "scatter" }, { ...result, frame }, theme);
    const x = got.xAxis as { name?: string; splitLine: { lineStyle: { color: string } } };
    const y = got.yAxis as { name?: string; interval: number; splitLine: { lineStyle: { color: string } }; axisLabel: { formatter: (v: number) => string } };
    expect(x.name).toBeUndefined(); expect(y.name).toBeUndefined(); expect(y.interval).toBe(120000);
    expect(y.axisLabel.formatter(y.interval)).toBe("2m");
    expect(x.splitLine.lineStyle.color).toBe(theme.grid); expect(y.splitLine.lineStyle.color).toBe(theme.grid);
  });
  it.each([{ width: 270, height: 220 }, { width: 780, height: 220 }, { width: 1100, height: 248 }])("W6 fits every node and label in a 20-node canvas %o", size => {
    const nodes = Array.from({ length: 20 }, (_, i) => ({ id: `service-with-long-name-${i}`, x: i % 5 * 100 - 250, y: Math.floor(i / 5) * 100 - 200, symbolSize: 30, priority: 20 - i }));
    const placed = rollups.fitServiceMap(nodes, size);
    expect(placed).toHaveLength(20);
    for (const node of placed) for (const box of [node.nodeBox, node.labelBox].filter(Boolean)) {
      expect(box!.x).toBeGreaterThanOrEqual(12); expect(box!.y).toBeGreaterThanOrEqual(12);
      expect(box!.x + box!.width).toBeLessThanOrEqual(size.width - 12 + .01); expect(box!.y + box!.height).toBeLessThanOrEqual(size.height - 12 + .01);
    }
    const labels = placed.flatMap(node => node.labelBox ? [node.labelBox] : []);
    for (let a = 0; a < labels.length; a++) for (let b = a + 1; b < labels.length; b++) expect(labels[a].x < labels[b].x + labels[b].width && labels[a].x + labels[a].width > labels[b].x && labels[a].y < labels[b].y + labels[b].height && labels[a].y + labels[a].height > labels[b].y).toBe(false);
  });
  it("W7 fills the heatmap and anchors the scale beneath its right edge", () => {
    const got = analysis.analysisOption({ ...panel, viz: "heatmap" }, result, chartThemeFor(false));
    expect(got.grid).toMatchObject({ top: 8, bottom: 48 });
    expect(got.visualMap).toMatchObject({ right: 16, bottom: 0, orient: "horizontal", padding: 0 });
    expect(got.visualMap).not.toHaveProperty("left");
  });
  it("W8 formats compact locale timestamps with year only outside the current year", () => {
    const stamp = new Date(2026, 9, 6, 15, 38, 13).getTime();
    expect(units.formatTimestamp(stamp, new Date(2026, 9, 7), "en-US")).toBe("Oct 6 15:38:13");
    expect(units.formatTimestamp(stamp, new Date(2027, 0, 1), "en-US")).toBe("Oct 6 2026 15:38:13");
    expect(units.formatTimestamp(NaN)).toBe("—");
  });
  it.each(["table", "logs", "traces"] as const)("W8 makes %s time cells nowrap with full ISO titles", async viz => {
    const stamp = Date.UTC(2026, 9, 6, 15, 38, 13);
    const host = await render(<Viz panel={{ ...panel, viz }} result={{ ...result, frame: { columns: [{ name: viz === "traces" ? "start" : "time", type: "time", role: "time" }], values: [[stamp]], rows: 1 } }} dark={false} height={300} group="g" />);
    const time = host.querySelector<HTMLElement>(`[title="${new Date(stamp).toISOString()}"]`)!;
    expect(time).not.toBeNull(); expect(time.style.whiteSpace).toBe("nowrap");
  });
  it("W9 grows the tiles and error sparkline to use the body", async () => {
    const host = await render(<Viz panel={{ ...panel, viz: "health" }} result={{ ...result, frame: { ...frame, health: { health: "healthy", counts: { healthy: 2, degraded: 0, unhealthy: 0 }, service_count: 2, total_spans: 100, error_rate: 0, error_trend: [0, 1] } } }} dark={false} height={248} group="g" />);
    expect(host.querySelector<HTMLElement>('[aria-label^="Service health:"]')!.style.flex).toBe("1 1 auto");
    expect(host.querySelector<HTMLElement>('[data-health-tiles]')!.style.flex).toBe("1 1 auto");
    expect(host.querySelector<HTMLElement>('[data-health-trend]')!.style.flex).toBe("1 1 auto");
  });
});
