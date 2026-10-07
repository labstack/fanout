import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { PanelCard } from "./panel-card";
import { StatViz } from "./viz/stat";
import { chartThemeFor } from "../../../panels/compile";
import { seriesSlot } from "../../../chart";
import type { Panel, PanelResult } from "../../../panels/types";

vi.mock("./echart-canvas", () => ({ EChartCanvas: ({ label }: { label: string }) => <div role="img" aria-label={label} /> }));
const panel: Panel = { id: "p", title: "Latency", viz: "timeseries", query: { from: "spans" }, drill: "traces" };
const result: PanelResult = { id: "p", status: "ok", elapsed_ms: 1, frame: { columns: [{ name: "time", role: "time", type: "time" }, { name: "p95", role: "measure", type: "number", unit: "ms" }], values: [[1000], [500]], rows: 1 }, sql: "SELECT 1" };
const cleanup: (() => void)[] = [];
beforeEach(() => vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true));
afterEach(async () => { await act(async () => cleanup.splice(0).forEach(fn => fn())); vi.restoreAllMocks(); document.body.innerHTML = ""; });
async function render(p = panel, dark = false) {
  const host = document.createElement("div"); document.body.append(host); const root = createRoot(host); cleanup.push(() => root.unmount());
  await act(async () => root.render(<MantineProvider forceColorScheme={dark ? "dark" : "light"}><PanelCard panel={p} title={p.title} result={result} loading={false} height={300} group="g" editing={false} agentAvailable={false} onView={vi.fn()} onCopyLink={vi.fn()} onExplain={vi.fn()} /></MantineProvider>));
  return host;
}
describe("preview V1", () => {
  it.each([false, true])("provides subtitle, accessible Chart/Data/Spec, syntax tokens and 16px body padding (%s)", async dark => {
    const host = await render(panel, dark);
    expect(host.querySelector('[data-panel-subtitle]')?.textContent).toBe("time series · spans");
    expect((host.querySelector('[data-panel-title]') as HTMLElement).style.fontSize).toBe("calc(0.9375rem * var(--mantine-scale))");
    expect((host.querySelector('[data-panel-body]') as HTMLElement).style.padding).toBe("16px");
    expect(host.textContent).toContain("Click the chart for exemplar traces at that time.");
    const button = (view: string) => host.querySelector<HTMLButtonElement>(`[data-panel-view="${view}"]`)!;
    await act(async () => button("Data").click());
    expect(host.querySelector("table")?.textContent).toContain("500ms");
    expect(host.textContent).toContain("SELECT 1");
    expect(button("Data").getAttribute("aria-pressed")).toBe("true");
    await act(async () => button("Spec").click());
    expect(JSON.parse(host.querySelector("pre")!.textContent!)).toEqual(panel);
    expect(host.querySelector('[data-json-token="key"]')).not.toBeNull();
    expect(host.querySelector('[data-json-token="string"]')).not.toBeNull();
    expect(host.querySelector("pre")?.getAttribute("contenteditable")).toBeNull();
    await act(async () => button("Chart").click()); expect(host.querySelector('[role="img"]')).not.toBeNull();
  });
  it.each([
    [{ ...panel, viz: "bar", click: { set_variable: "route" }, drill: undefined }, "Click a bar to filter the board by route."],
    [{ ...panel, viz: "table", sql: "SELECT 1", query: undefined }, "Click a row to open the trace."],
    [{ ...panel, viz: "logs", query: { from: "logs" }, drill: undefined }, ""],
  ] as [Panel, string][])("uses the interaction hint for %s", async (p, hint) => {
    const host = await render(p); expect(host.querySelector('[data-panel-hint]')?.textContent ?? "").toBe(hint);
    if (p.sql) expect(host.querySelector('[data-panel-subtitle]')?.textContent).toBe("table · sql");
  });
});

describe("preview V2", () => {
  it("honours comparison off and health/threshold badge labels", async () => {
    const host = document.createElement("div"); document.body.append(host); const root = createRoot(host); cleanup.push(() => root.unmount());
    const health = { health: "unhealthy", counts: { healthy: 0, degraded: 0, unhealthy: 1 }, total_spans: 1, error_rate: 10, service_count: 1, error_trend: [] };
    await act(async () => root.render(<MantineProvider><StatViz compare={false} panel={{ ...panel, viz: "stat", reduce: "last" }} result={{ ...result, previous: result.frame, frame: { ...result.frame!, health } }} /></MantineProvider>));
    expect(host.querySelector('[data-stat-delta]')).toBeNull(); expect(host.querySelector('[data-stat-status]')?.textContent).toBe("◆ Unhealthy");
    await act(async () => root.render(<MantineProvider><StatViz panel={{ ...panel, viz: "stat", reduce: "last", thresholds: [{ value: 100, status: "warn", label: "Budget breached" }] }} result={result} /></MantineProvider>));
    expect(host.querySelector('[data-stat-status]')?.textContent).toBe("■ Budget breached");
  });
  it.each([false, true])("renders bold value, threshold pill, range delta and slot-zero area (%s)", async dark => {
    const host = document.createElement("div"); document.body.append(host); const root = createRoot(host); cleanup.push(() => root.unmount());
    const p: Panel = { ...panel, viz: "stat", reduce: "last", better: "lower", thresholds: [{ value: 800, status: "warn" }] };
    const f = { ...result.frame!, values: [[0, 1800000, 3599999], [800, 1010, 1010]], rows: 3 };
    const r = { ...result, frame: f, previous: { ...f, values: [f.values[0], [344, 344, 344]] }, from_ms: 0, to_ms: 3600000 };
    await act(async () => root.render(<MantineProvider><StatViz panel={p} result={r} dark={dark} /></MantineProvider>));
    expect((host.querySelector('[data-stat-value]') as HTMLElement).style.fontWeight).toBe("700");
    expect(host.querySelector('[data-stat-status]')?.textContent).toBe("■ Degraded");
    expect((host.querySelector('[data-stat-status]') as HTMLElement).style.borderRadius).toBe("999px");
    expect(host.textContent).toContain("▲ +194%"); expect(host.textContent).toContain("vs previous hour");
    expect((host.querySelector('[data-stat-delta]') as HTMLElement).style.color).toBe("var(--mantine-color-bad-text)");
    const svg = host.querySelector("svg")!; expect(svg.getAttribute("height")).toBe("40");
    expect(svg.querySelector('[data-sparkline-area]')?.getAttribute("fill")).toBe(seriesSlot(0, dark));
    expect(svg.querySelector('[data-sparkline-area]')?.getAttribute("opacity")).toBe("0.18");
    expect(svg.querySelector('[data-sparkline-line]')?.getAttribute("stroke")).toBe(seriesSlot(0, dark));
    expect(chartThemeFor(dark).status.warn).toBeTruthy();
  });
  it.each([["higher", "bad", "◆ Unhealthy"], ["lower", "ok", "● Healthy"]] as const)("uses %s direction and a previous-day percentage-points delta", async (better, status, label) => {
    const host = document.createElement("div"); document.body.append(host); const root = createRoot(host); cleanup.push(() => root.unmount());
    const f = { ...result.frame!, columns: [{ ...result.frame!.columns[0] }, { ...result.frame!.columns[1], unit: "percent" }], values: [[0], [4.1]], rows: 1 };
    await act(async () => root.render(<MantineProvider><StatViz panel={{ ...panel, viz: "stat", reduce: "last", better, thresholds: [{ value: 5, status: "bad" }] }} result={{ ...result, frame: f, previous: { ...f, values: [[0], [7.1]] }, from_ms: 0, to_ms: 86400000 }} /></MantineProvider>));
    expect(host.textContent).toContain(label); expect(host.textContent).toContain("▼ −3.0 pts"); expect(host.textContent).toContain("vs previous day");
    expect((host.querySelector('[data-stat-delta]') as HTMLElement).style.color).toBe(`var(--mantine-color-${status}-text)`);
  });
});
