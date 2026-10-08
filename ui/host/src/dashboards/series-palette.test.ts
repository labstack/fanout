import { describe, expect, it } from "vitest";
import { chartThemeFor, barOption, gaugeOption, timeseriesOption } from "../../../panels/compile";
import { analysisOption } from "../../../panels/analysis";
import { series } from "../../../tokens";
import type { Frame, Panel, PanelResult } from "../../../panels/types";

const names = Array.from({ length: 8 }, (_, i) => `svc${i}`);
const panel: Panel = { id: "services", title: "Services", viz: "timeseries" };
const frame = (count: number): Frame => ({
  columns: [{ name: "time", type: "time", role: "time" }, { name: "service", type: "string", role: "dimension" }, { name: "count", type: "number", role: "measure", unit: "count" }],
  values: [Array(count).fill(1000), names.slice(0, count), Array.from({ length: count }, (_, i) => i + 1)], rows: count,
});
const result = (frame: Frame): PanelResult => ({ id: panel.id, status: "ok", elapsed_ms: 1, frame });
type Chart = { series: { name: string; itemStyle: { color: string }; data: unknown[] }[] };

describe("validated categorical series", () => {
  it("pins the validator-approved palette in exact slot order", () => {
    expect(series.dark).toEqual(["#3987e5", "#d95926", "#199e70", "#9085e9", "#c98500", "#d55181"]);
    expect(series.light).toEqual(["#2a78d6", "#eb6834", "#1baf7a", "#4a3aa7", "#eda100", "#e87ba4"]);
  });
  it.each([false, true])("assigns six distinct slots in chart order (dark=%s)", dark => {
    const theme = chartThemeFor(dark);
    for (const style of ["line", "bars", "stacked"] as const) {
      const chart = timeseriesOption({ ...panel, options: { style } }, result(frame(6)), theme) as Chart;
      expect(chart.series.map(s => s.itemStyle.color)).toEqual(series[dark ? "dark" : "light"]);
      expect(new Set(chart.series.map(s => s.itemStyle.color)).size).toBe(6);
    }
  });
  it("keeps six named series and a server Other, and leaves the rest out with a note instead of summing", () => {
    const theme = chartThemeFor(true);
    type Noted = Chart & { graphic: { style: { text: string } }[] };
    const f = frame(8);
    f.values[1][0] = "Other";
    const chart = timeseriesOption({ ...panel, options: { top: 8 } }, result(f), theme) as Noted;
    expect(chart.series.map(s => s.name)).toEqual([...names.slice(1, 7), "Other"]);
    expect(chart.series.slice(0, 6).map(s => s.itemStyle.color)).toEqual(series.dark);
    expect(chart.series[6].itemStyle.color).toBe(theme.muted);
    // The server's Other is drawn as it came; the left-out svc7 is not added to it.
    expect(chart.series[6].data).toEqual([[1000, 1]]);
    expect(chart.graphic[0].style.text).toBe("1 more series not shown");
    // A p95 (non-additive) frame with seven series never gains a summed Other.
    const seventh = timeseriesOption(panel, result(frame(7)), theme) as Noted;
    expect(seventh.series.map(s => s.name)).toEqual(names.slice(0, 6));
    expect(seventh.graphic[0].style.text).toBe("1 more series not shown");
    expect((timeseriesOption(panel, result(frame(6)), theme) as Noted).graphic).toEqual([]);
    expect((timeseriesOption({ ...panel, options: { top: 2 } }, result(frame(8)), theme) as Chart).series).toHaveLength(2);
  });
  it("uses ordered slots for grouped bars, histogram and scatter, preserving overflow points", () => {
    const theme = chartThemeFor(false);
    const grouped: Frame = { columns: [{ name: "route", type: "string", role: "dimension" }, frame(1).columns[1], frame(1).columns[2]], values: [Array(8).fill("/cart"), names, Array(8).fill(1)], rows: 8 };
    const bars = barOption({ ...panel, viz: "bar" }, grouped, theme) as Chart;
    expect(bars.series.map(s => s.name)).toEqual(names.slice(0, 6));
    expect(bars.series.slice(0, 6).map(s => s.itemStyle.color)).toEqual(series.light);
    expect((bars as unknown as { graphic: { style: { text: string } }[] }).graphic[0].style.text).toBe("2 more series not shown");
    const histogram: Frame = { columns: [frame(1).columns[1], { name: "bucket_lower", type: "number", role: "dimension", unit: "ms" }, { name: "bucket_upper", type: "number", role: "dimension", unit: "ms" }, frame(1).columns[2]], values: [names, Array(8).fill(0), Array(8).fill(1), Array(8).fill(1)], rows: 8 };
    const hist = analysisOption({ ...panel, viz: "histogram" }, result(histogram), theme) as Chart;
    expect(hist.series.slice(0, 6).map(s => s.itemStyle.color)).toEqual(series.light);
    expect(hist.series[6].name).toBe("Other (2)");
    expect(hist.series[6].data[0]).toMatchObject({ value: 2 });
    const scatter: Frame = { columns: [grouped.columns[0], grouped.columns[1], grouped.columns[2], { name: "p95", type: "number", role: "measure", unit: "ms" }], values: [...grouped.values, Array(8).fill(10)], rows: 8 };
    const points = analysisOption({ ...panel, viz: "scatter" }, result(scatter), theme) as Chart;
    expect(points.series.slice(0, 6).map(s => s.itemStyle.color)).toEqual(series.light);
    expect(points.series[6].name).toBe("Other (2)");
    expect(points.series[6].data).toHaveLength(2);
  });
  it("uses slot zero for single series and gauges, with a dedicated heatmap ramp", () => {
    const theme = chartThemeFor(true);
    const single: Frame = { columns: [{ name: "route", type: "string", role: "dimension" }, frame(1).columns[2]], values: [["/cart"], [5]], rows: 1 };
    expect((barOption({ ...panel, viz: "bar" }, single, theme) as Chart).series[0].itemStyle.color).toBe(series.dark[0]);
    const gauge = gaugeOption({ ...panel, viz: "gauge" }, 5, theme) as { graphic: {id?:string;style:{fill:string}}[] };
    expect(gauge.graphic.find(g=>g.id === "gauge-fill")!.style.fill).toBe(series.dark[0]);
    const heat = analysisOption({ ...panel, viz: "heatmap" }, result(frame(1)), theme) as { visualMap: { inRange: { color: string[] } } };
    expect(heat.visualMap.inRange.color.at(-1)).toBe("#7dd3fc"); expect(heat.visualMap.inRange.color).toHaveLength(7);
  });
});
