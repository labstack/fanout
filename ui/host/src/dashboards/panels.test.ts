import { describe, expect, it } from "vitest";
import { duration } from "../../../format";
import { barOption, chartThemeFor, gaugeOption, timeseriesOption } from "../../../panels/compile";
import { statValue, toCategories, toSeries } from "../../../panels/frame";
import { statusFor } from "../../../panels/thresholds";
import type { Frame, Panel, PanelResult } from "../../../panels/types";
import { formatAxis, formatValue, valueAxis } from "../../../panels/units";

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
  it("reads a duration axis in one unit", () => {
    const ticks = (unit: string, axis: { min: number; max: number; interval: number }) => Array.from({ length: Math.round((axis.max - axis.min) / axis.interval) + 1 }, (_, i) => formatAxis(unit, axis)(axis.min + i * axis.interval));
    expect(ticks("ms", { min: 0, max: 10_000, interval: 5000 })).toEqual(["0s", "5s", "10s"]);
    expect(ticks("ms", { min: 0, max: 1500, interval: 500 })).toEqual(["0s", "0.5s", "1s", "1.5s"]);
    expect(ticks("ms", { min: 0, max: 300, interval: 100 })).toEqual(["0ms", "100ms", "200ms", "300ms"]);
    // A minute axis in thirds of a minute stays in seconds rather than printing 0.33m.
    expect(ticks("ms", { min: 0, max: 100_000, interval: 20_000 })).toEqual(["0s", "20s", "40s", "60s", "80s", "100s"]);
    expect(ticks("s", { min: 0, max: 600, interval: 300 })).toEqual(["0m", "5m", "10m"]);
    expect(ticks("ns", { min: 0, max: 2e6, interval: 1e6 })).toEqual(["0ms", "1ms", "2ms"]);
  });
  it("steps a bytes axis in whole binary units and reads it in one", () => {
    const axis = valueAxis(0, 300 * 2 ** 20, "bytes", 280);
    const ticks = Array.from({ length: Math.round((axis.max - axis.min) / axis.interval) + 1 }, (_, i) => formatAxis("bytes", axis)(axis.min + i * axis.interval));
    expect(ticks).toEqual(["0 MiB", "100 MiB", "200 MiB", "300 MiB"]);
    const wide = valueAxis(0, 1.1 * 2 ** 30, "bytes", 280);
    expect(Array.from({ length: Math.round(wide.max / wide.interval) + 1 }, (_, i) => formatAxis("bytes", wide)(i * wide.interval))).toEqual(["0 MiB", "500 MiB", "1,000 MiB", "1,500 MiB"]);
    expect(valueAxis(0, 2000, "bytes", 280).interval).toBe(1024);
    // Past the largest unit, steps keep growing in it and the tick budget still holds.
    for (const max of [1e16, 1e18]) {
      const huge = valueAxis(0, max, "bytes", 300);
      expect(huge.max).toBeGreaterThanOrEqual(max);
      expect(Math.round(huge.max / huge.interval)).toBeLessThanOrEqual(huge.splitNumber);
    }
    // Bytes are whole: an all-zero axis steps by one byte.
    expect(valueAxis(0, 0, "bytes", 280).interval).toBe(1);
  });
  type Axis = { min: number; max: number; interval: number; axisLabel: { formatter(v: number): string } };
  it("keeps a duration time-series axis in one unit", () => {
    const frame: Frame = { columns: [{ name: "time", type: "time", role: "time" }, { name: "p95", type: "number", role: "measure", unit: "ms" }], values: [[1000, 2000], [400, 9000]], rows: 2 };
    const option = timeseriesOption({ id: "p", title: "p95", viz: "timeseries" }, { id: "p", status: "ok", elapsed_ms: 1, frame, from_ms: 0, to_ms: 3000 }, chartThemeFor(false), { width: 600, height: 300 }) as { yAxis: Axis | Axis[] };
    const axis = Array.isArray(option.yAxis) ? option.yAxis[0] : option.yAxis;
    const labels = Array.from({ length: Math.round((axis.max - axis.min) / axis.interval) + 1 }, (_, i) => axis.axisLabel.formatter(axis.min + i * axis.interval));
    expect(new Set(labels.map(label => label.replace(/[\d.,-]/g, "")))).toEqual(new Set(["s"]));
  });
});

describe("frames", () => {
  it("labels a missing group value the same in time series and bars", () => {
    const time = { name: "time", type: "time" as const, role: "time" as const }, service = { name: "service", type: "string" as const, role: "dimension" as const }, count = { name: "count", type: "number" as const, role: "measure" as const };
    expect(toSeries({ columns: [time, service, count], values: [[1], [null], [3]], rows: 1 }).map(s => s.name)).toEqual(["(none)"]);
    const frame: Frame = { columns: [service, count], values: [["", "cart"], [3, 4]], rows: 2 };
    const option = barOption({ id: "b", title: "b", viz: "bar", query: { from: "spans", measures: ["count()"], by: ["service"] } }, frame, chartThemeFor(false), { width: 500, height: 248, measureText: text => text.length * 7 }) as unknown as { yAxis: { data: string[]; axisLabel: { width: number } }; series: { data: { selection?: { dimensions: Record<string, string> } }[] }[] };
    expect(option.yAxis.data).toEqual(["(none)", "cart"]);
    // The label width leaves the chart's own text measure some room.
    expect(option.yAxis.axisLabel.width).toBeGreaterThan("(none)".length * 7);
    // The drill still selects the stored value.
    expect(option.series[0].data[0].selection?.dimensions).toEqual({ service: "" });
    // A stored "(none)" stays its own group with its own drill.
    const both: Frame = { columns: [service, count], values: [["", "(none)"], [3, 4]], rows: 2 };
    expect(toSeries({ columns: [time, service, count], values: [[1, 1], ["", "(none)"], [3, 4]], rows: 2 })).toHaveLength(2);
    const split = barOption({ id: "b", title: "b", viz: "bar", query: { from: "spans", measures: ["count()"], by: ["service"] } }, both, chartThemeFor(false)) as unknown as typeof option;
    expect(split.yAxis.data).toEqual(["(none)", "(none)"]);
    expect(split.series[0].data.map(d => d.selection?.dimensions.service)).toEqual(["", "(none)"]);
  });
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

  it("shows six of twelve series with a note in a wrapping legend clear of the plot", () => {
    const measures: Frame["columns"] = Array.from({ length: 12 }, (_, i) => ({ name: `measure_${i}`, type: "number", role: "measure", unit: "count" }));
    const values = measures.map(() => [1]);
    const many: Frame = { columns: [series.columns[0], ...measures], values: [[1000], ...values], rows: 1 };
    const time = timeseriesOption(panel, { ...result, frame: many, previous: undefined }, theme);
    const bar = barOption({ ...panel, viz: "bar" }, { ...many, columns: [{ name: "route", type: "string", role: "dimension" }, ...measures], values: [["/cart"], ...values] }, theme);
    for (const option of [time, bar] as { legend: { type: string; top: number; itemHeight: number }; grid: { top: number }; series: unknown[]; graphic: { style: { text: string } }[] }[]) {
      expect(option.series).toHaveLength(6);
      expect(option.graphic[0].style.text).toBe("6 more series not shown");
      expect(option.legend.type).toBe("plain");
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
    const option = timeseriesOption({ ...panel, thresholds: [{ value: 750, status: "warn" }] }, result, theme) as { series: { name: string; lineStyle?: { type?: string }; markLine?: { data: unknown[] } }[]; legend: { show: boolean } };
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
    const gauge = gaugeOption({ id: "g", title: "g", viz: "gauge", min: 0, max: 50, unit: "count" } as Panel, 42, theme) as { graphic: {type:string;style?:{text:string}}[] };
    expect(gauge.graphic.filter(g=>g.type === "text").map(g=>g.style?.text)).toContain("50");
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

const reduce=(values:(number|null)[],reducer:string)=>statValue({id:"p",title:"Value",viz:"stat",reduce:reducer as Panel["reduce"]},{columns:[{name:"value",type:"number",role:"measure"}],values:[values],rows:values.length});

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

  it("keeps axis bounds independent of thresholds, and does not sample bars", () => {
    const p = { ...panel, thresholds: [{ value: 1500, status: "bad" as const }] };
    const option = timeseriesOption(p, { id: "x", status: "ok", frame: grouped, elapsed_ms: 1 }, theme) as { yAxis: { max: number } };
    const without = timeseriesOption(panel, { id: "x", status: "ok", frame: grouped, elapsed_ms: 1 }, theme) as { yAxis: { min: number; max: number; interval: number } };
    for (const key of ["min", "max", "interval"] as const) expect((option.yAxis as typeof without.yAxis)[key]).toBe(without.yAxis[key]);
    expect(option.yAxis.max).toBeLessThan(1500);
    const bars = timeseriesOption({ ...p, options: { style: "bars" } }, { id: "x", status: "ok", frame: grouped, elapsed_ms: 1 }, theme) as { series: S[] };
    expect(bars.series[0].sampling).toBeUndefined();
  });

  it("builds gauge bands from statusFor in both directions", () => {
    const base = { id: "g", title: "g", viz: "gauge", min: 0, max: 100 } as Panel;
    const higher = { ...base, better: "higher" as const, thresholds: [{ value: 25, status: "bad" as const }, { value: 50, status: "warn" as const }] };
    for(const [value,color] of [[12,theme.status.bad],[37,theme.status.warn],[75,theme.status.ok]] as const) expect((gaugeOption(higher,value,theme) as any).graphic.find((g:any)=>g.id==="gauge-fill").style.fill).toBe(color);
    const lower = { ...base, better: "lower" as const, max: 3000, thresholds: [{ value: 750, status: "warn" as const }, { value: 2000, status: "bad" as const }] };
    for(const [value,color] of [[100,theme.status.ok],[1000,theme.status.warn],[2500,theme.status.bad]] as const) expect((gaugeOption(lower,value,theme) as any).graphic.find((g:any)=>g.id==="gauge-fill").style.fill).toBe(color);
    const option = gaugeOption(higher, 75, theme) as { graphic: {id?:string;style:{fill:string}}[] };
    const color = theme.status[statusFor(75, higher.thresholds, "higher")!];
    expect(option.graphic.find(g => g.id === "gauge-fill")!.style.fill).toBe(color);
  });
});
