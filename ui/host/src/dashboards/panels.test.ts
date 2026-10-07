import { describe, expect, it } from "vitest";
import { duration } from "../../../format";
import { barOption, chartThemeFor, gaugeBands, gaugeOption, timeseriesOption } from "../../../panels/compile";
import { reduce, statValue, toCategories, toSeries } from "../../../panels/frame";
import { statusFor } from "../../../panels/thresholds";
import type { Frame, Panel, PanelResult } from "../../../panels/types";
import { formatAxis, formatValue } from "../../../panels/units";

const series: Frame = {
  columns: [{ name: "time", type: "time", role: "time" }, { name: "service", type: "string", role: "dimension" }, { name: "p95", type: "number", role: "measure", unit: "ms" }],
  values: [[1000, 1000, 2000, 2000], ["checkout", "cart", "checkout", "cart"], [120, 40, 900, null]],
  rows: 4,
};

describe("units", () => {
  it.each([ ["per_second", "/s"], ["ms", "ms"], ["percent", "%"], ["ratio", "×"], ["count", ""], ["per_minute", "/min"], ["bytes", " B"], ["none", ""] ])("preserves small significant values for %s in values and axes", (unit, suffix) => {
    for (const value of [0.03, 0.0004, -0.03, -0.0004]) {
      expect(formatValue(unit, value)).toBe(`${value}${suffix}`);
      expect(formatAxis(unit)(value)).toBe(`${value}${suffix}`);
    }
    expect(formatValue(unit, 1e-7)).not.toMatch(/^0(?:\D|$)/);
  });
  it("preserves small durations after seconds and nanoseconds conversion", () => {
    expect(formatValue("s", 4e-7)).toBe("0.0004ms");
    expect(formatValue("ns", 400)).toBe("0.0004ms");
  });
  it("formats by unit without mixing scales", () => {
    expect(formatValue("ms", 850)).toBe("850ms");
    expect(formatValue("ms", 600_000)).toBe("10m");
    expect(formatValue("percent", 4.2)).toBe("4.20%");
    expect(formatValue("per_second", 1234)).toBe("1.2K/s");
    expect(formatValue("bytes", 1536)).toBe("1.5 KiB");
    expect(formatValue("count", null)).toBe("—");
    expect(formatAxis("ms")(1500)).toBe("1.5s");
  });
});

describe("frames", () => {
  it("pivots a grouped series by dimension value", () => {
    const out = toSeries(series);
    expect(out.map((s) => s.name)).toEqual(["checkout", "cart"]);
    expect(out[0].points).toEqual([[1000, 120], [2000, 900]]);
    expect(out[1].points).toEqual([[1000, 40], [2000, null]]);
  });

  it("builds categories for bars", () => {
    const frame: Frame = { columns: [{ name: "route", type: "string", role: "dimension" }, { name: "count", type: "number", role: "measure", unit: "count" }], values: [["/a", "/b"], [5, 3]], rows: 2 };
    expect(toCategories(frame)).toEqual({ categories: ["/a", "/b"], series: [{ name: "count", unit: "count", values: [5, 3] }] });
  });

  it("reduces stat values, preferring the window total", () => {
    const panel = { id: "x", title: "x", viz: "stat", reduce: "window" } as Panel;
    const frame: Frame = { columns: [{ name: "time", type: "time", role: "time" }, { name: "count", type: "number", role: "measure" }], values: [[1, 2], [3, 4]], rows: 2, totals: [null, 10] };
    expect(statValue(panel, frame)).toBe(10);
    expect(statValue({ ...panel, reduce: "last" }, frame)).toBe(4);
    expect(reduce([1, null, 5], "mean")).toBe(3);
  });
});

describe("thresholds", () => {
  it("grades by direction", () => {
    const thresholds = [{ value: 750, status: "warn" as const }, { value: 2000, status: "bad" as const }];
    expect(statusFor(500, thresholds, "lower")).toBe("ok");
    expect(statusFor(900, thresholds, "lower")).toBe("warn");
    expect(statusFor(2500, thresholds, "lower")).toBe("bad");
    expect(statusFor(20, [{ value: 25, status: "bad" }], "higher")).toBe("bad");
    expect(statusFor(null, thresholds)).toBeNull();
    expect(statusFor(5, undefined)).toBeNull();
  });
});

describe("compile", () => {
  const theme = chartThemeFor(false);
  const panel = { id: "latency", title: "Latency", viz: "timeseries", unit: "ms", thresholds: [{ value: 1500, status: "bad", label: "budget" }] } as Panel;
  const result: PanelResult = { id: "latency", status: "ok", frame: series, previous: series, elapsed_ms: 3, shift_ms: 1000 };

  it("shows six of twelve series with a note in a scroll legend clear of the plot", () => {
    const measures: Frame["columns"] = Array.from({ length: 12 }, (_, i) => ({ name: `measure_${i}`, type: "number", role: "measure", unit: "count" }));
    const values = measures.map(() => [1]);
    const many: Frame = { columns: [series.columns[0], ...measures], values: [[1000], ...values], rows: 1 };
    const time = timeseriesOption(panel, { ...result, frame: many, previous: undefined }, theme);
    const bar = barOption({ ...panel, viz: "bar" }, { ...many, columns: [{ name: "route", type: "string", role: "dimension" }, ...measures], values: [["/cart"], ...values] }, theme);
    for (const option of [time, bar] as { legend: { type: string; top: number; itemHeight: number }; grid: { top: number }; series: unknown[]; graphic: { style: { text: string } }[] }[]) {
      expect(option.series).toHaveLength(6);
      expect(option.graphic[0].style.text).toBe("6 more series not shown");
      expect(option.legend.type).toBe("scroll");
      expect(option.grid.top).toBeGreaterThanOrEqual(option.legend.top + option.legend.itemHeight + 20);
    }
  });

  it("uses each column unit for mixed measure axes, tooltips and bar labels", () => {
    const columns: Frame["columns"] = [{ name: "p95", type: "number", role: "measure", unit: "ms" }, { name: "count_distinct", type: "number", role: "measure", unit: "count" }];
    const mixed: Frame = { columns: [series.columns[0], ...columns], values: [[1000], [95], [1]], rows: 1 };
    type Axis = { axisLabel: { formatter: (value: number) => string } };
    type ChartSeries = { yAxisIndex?: number; xAxisIndex?: number; tooltip: { valueFormatter: (value: number) => string }; label: { formatter: (p: { value: number }) => string } };
    const option = timeseriesOption(panel, { ...result, frame: mixed, previous: mixed }, theme) as { yAxis: Axis[]; series: ChartSeries[] };
    expect(option.series[1].tooltip.valueFormatter(1)).toBe("1");
    expect(option.series[3].tooltip.valueFormatter(1)).toBe("1");
    expect(option.yAxis[option.series[1].yAxisIndex!].axisLabel.formatter(1)).toBe("1");
    expect(option.yAxis[option.series[0].yAxisIndex!].axisLabel.formatter(95)).toContain("ms");
    const bars = barOption({ ...panel, viz: "bar" }, { ...mixed, columns: [{ name: "route", type: "string", role: "dimension" }, ...columns], values: [["/cart"], [95], [1]] }, theme) as { xAxis: Axis[]; series: ChartSeries[] };
    expect(bars.series[1].tooltip.valueFormatter(1)).toBe("1");
    expect(bars.series[1].label.formatter({ value: 1 })).toBe("1");
    expect(bars.xAxis[bars.series[1].xAxisIndex!].axisLabel.formatter(1)).toBe("1");
  });

  it("draws series, thresholds and the previous period", () => {
    const option = timeseriesOption(panel, result, theme) as { series: { name: string; lineStyle?: { type?: string }; markLine?: { data: unknown[] } }[]; legend: { show: boolean } };
    expect(option.series.map((s) => s.name)).toEqual(["checkout", "cart", "checkout · previous", "cart · previous"]);
    expect(option.series[2].lineStyle?.type).toBe("dashed");
    expect(option.series[0].markLine?.data).toHaveLength(1);
    expect(option.legend.show).toBe(true);
  });

  it("stacks only when asked", () => {
    const stacked = timeseriesOption({ ...panel, options: { style: "stacked" } }, { ...result, previous: undefined }, theme) as { series: { stack?: string }[] };
    expect(stacked.series.every((s) => s.stack === "total")).toBe(true);
  });

  it("draws horizontal bars and gauges", () => {
    const frame: Frame = { columns: [{ name: "route", type: "string", role: "dimension" }, { name: "count", type: "number", role: "measure", unit: "count" }], values: [["/a", "/b"], [5, 3]], rows: 2 };
    const bar = barOption({ id: "b", title: "b", viz: "bar" } as Panel, frame, theme) as { yAxis: { data: string[]; inverse: boolean } };
    expect(bar.yAxis.data).toEqual(["/a", "/b"]);
    expect(bar.yAxis.inverse).toBe(true);
    const gauge = gaugeOption({ id: "g", title: "g", viz: "gauge", min: 0, max: 50, unit: "count" } as Panel, 42, theme) as { series: { min: number; max: number }[] };
    expect(gauge.series[0].max).toBe(50);
  });
});

describe("units, more", () => {
  it("converts and renders every unit", () => {
    expect(formatValue("s", 2)).toBe(duration(2000));
    expect(formatValue("ns", 5_000_000)).toBe(duration(5));
    expect(formatValue("ratio", 1.5)).toBe("1.5×");
    expect(formatValue("per_minute", 12)).toBe("12/min");
    expect(formatValue("none", 1234.567)).toBe("1,234.57");
    expect(formatValue(undefined, 20_000)).toBe("20K");
    for (const bad of [null, undefined, Number.NaN, Number.POSITIVE_INFINITY]) expect(formatValue("ms", bad)).toBe("—");
  });
});

describe("reducers", () => {
  const values = [3, null, 1, 5, null];
  it("reduces ignoring nulls", () => {
    expect(reduce(values, "min")).toBe(1);
    expect(reduce(values, "max")).toBe(5);
    expect(reduce(values, "sum")).toBe(9);
    expect(reduce(values, "mean")).toBe(3);
    expect(reduce(values, "last")).toBe(5);
    expect(reduce([null, null], "max")).toBeNull();
  });
});

describe("toSeries, more", () => {
  const measure = (name: string): Frame["columns"][number] => ({ name, type: "number", role: "measure" });
  const time: Frame["columns"][number] = { name: "time", type: "time", role: "time" };
  it("handles empty, ungrouped and single-series frames", () => {
    expect(toSeries({ columns: [], values: [], rows: 0 })).toEqual([]);
    expect(toSeries({ columns: [measure("count")], values: [[1]], rows: 1 })).toEqual([]);
    const two = toSeries({ columns: [time, measure("a"), measure("b")], values: [[1, 2], [3, 4], [5, 6]], rows: 2 });
    expect(two.map((s) => s.name)).toEqual(["a", "b"]);
    expect(two[1].points).toEqual([[1, 5], [2, 6]]);
    const one = toSeries({ columns: [time, measure("a")], values: [[1], [null]], rows: 1 });
    expect(one).toEqual([{ name: "a", unit: undefined, points: [[1, null]] }]);
  });
});

describe("compile, more", () => {
  const theme = chartThemeFor(false);
  const panel = { id: "latency", title: "Latency", viz: "timeseries", unit: "ms" } as Panel;
  const grouped: Frame = {
    columns: [{ name: "time", type: "time", role: "time" }, { name: "service", type: "string", role: "dimension" }, { name: "p95", type: "number", role: "measure", unit: "ms" }],
    values: [[1000, 1000], ["checkout", "Other"], [1, 2]],
    rows: 2,
  };
  type S = { name: string; data: unknown[]; lineStyle: { color: string }; sampling?: string };

  it("colours Other with the muted tone", () => {
    const option = timeseriesOption(panel, { id: "x", status: "ok", frame: grouped, elapsed_ms: 1 }, theme) as { series: S[] };
    expect(option.series[1].lineStyle.color).toBe(theme.muted);
    expect(option.series[0].lineStyle.color).not.toBe(theme.muted);
  });

  it("hides the legend and switches to a log axis on request", () => {
    const hidden = timeseriesOption({ ...panel, options: { legend: "hidden", scale: "log" } }, { id: "x", status: "ok", frame: grouped, elapsed_ms: 1 }, theme) as { legend: { show: boolean }; yAxis: { type: string } };
    expect(hidden.legend.show).toBe(false);
    expect(hidden.yAxis.type).toBe("log");
  });

  it("shifts the previous period by the server's shift, never a guess", () => {
    const previous: Frame = { ...grouped, values: [[5000, 6000], ["checkout", "checkout"], [1, 2]] };
    const result: PanelResult = { id: "x", status: "ok", frame: grouped, previous, elapsed_ms: 1, shift_ms: 3000 };
    const option = timeseriesOption(panel, result, theme) as { series: S[] };
    const prev = option.series.find((s) => s.name === "checkout · previous")!;
    expect(prev.data).toEqual([[8000, 1], [9000, 2]]);
    const none = timeseriesOption(panel, { ...result, shift_ms: undefined }, theme) as { series: S[] };
    expect(none.series.some((s) => s.name.endsWith("previous"))).toBe(false);
  });

  it("keeps a threshold above the data in view, and does not sample bars", () => {
    const p = { ...panel, thresholds: [{ value: 1500, status: "bad" as const }] };
    const option = timeseriesOption(p, { id: "x", status: "ok", frame: grouped, elapsed_ms: 1 }, theme) as { yAxis: { max: (r: { max: number }) => number } };
    expect(option.yAxis.max({ max: 100 })).toBe(1500);
    expect(option.yAxis.max({ max: 9000 })).toBe(9000);
    const bars = timeseriesOption({ ...p, options: { style: "bars" } }, { id: "x", status: "ok", frame: grouped, elapsed_ms: 1 }, theme) as { series: S[] };
    expect(bars.series[0].sampling).toBeUndefined();
  });

  it("builds gauge bands from statusFor in both directions", () => {
    const base = { id: "g", title: "g", viz: "gauge", min: 0, max: 100 } as Panel;
    const higher = { ...base, better: "higher" as const, thresholds: [{ value: 25, status: "bad" as const }, { value: 50, status: "warn" as const }] };
    expect(gaugeBands(higher, 0, 100, theme)).toEqual([[0.25, theme.status.bad], [0.5, theme.status.warn], [1, theme.status.ok]]);
    const lower = { ...base, better: "lower" as const, max: 3000, thresholds: [{ value: 750, status: "warn" as const }, { value: 2000, status: "bad" as const }] };
    expect(gaugeBands(lower, 0, 3000, theme)).toEqual([[0.25, theme.status.ok], [2000 / 3000, theme.status.warn], [1, theme.status.bad]]);
    // The value's own band matches the stat colour.
    const option = gaugeOption(higher, 75, theme) as { series: { axisLine: { lineStyle: { color: [number, string][] } } }[] };
    const bands = option.series[0].axisLine.lineStyle.color;
    expect(bands.find(([end]) => end >= 0.75)?.[1]).toBe(theme.status[statusFor(75, higher.thresholds, "higher")!]);
  });
});
