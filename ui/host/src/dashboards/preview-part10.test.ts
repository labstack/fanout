import { expect, it } from "vitest";
import { analysisOption, heatRamp, heatStep } from "../../../panels/analysis";
import { chartThemeFor } from "../../../panels/compile";
import type { PanelResult } from "../../../panels/types";

function option(counts: (number | null)[], dark = false) {
  const result: PanelResult = { id: "h", status: "ok", elapsed_ms: 1, interval: "1m", frame: {
    columns: [{ name: "time", type: "time", role: "time" }, { name: "count", type: "number", role: "measure" }],
    values: [counts.map((_, i) => i * 60000), counts], rows: counts.length,
  } };
  return analysisOption({ id: "h", title: "Heat", viz: "heatmap" }, result, chartThemeFor(dark)) as any;
}
function fill(o: any, count: number) {
  const series = o.series[0], point = series.data.find((d: any) => d.value[2] === count);
  return series.renderItem({}, { value: (i: number) => point.value[i], coord: (v: number[]) => [v[0] / 1000, 20], size: () => [60, 20], style: () => ({}) }).style.fill;
}

it("Part 10 maps decades to distinct monotonic log steps with cap 3,000", () => {
  expect([1, 10, 100, 1000, 3000].map(n => heatStep(n, 3000))).toEqual([0, 1, 3, 5, 6]);
});
it("Part 10 keeps low counts in the bottom steps of a fixed magnitude scale", () => {
  expect([1, 2, 3, 4, 5].map(n => heatStep(n, 3000))).toEqual([0, 0, 0, 1, 1]);
});
it.each([false, true])("Part 10 equal counts share colours across distributions with the same p99 cap (%s)", dark => {
  const a = option([1, 10, 100, 1000, 3000], dark), b = option([100, 3000, 3000, 3000, 3000], dark);
  expect(fill(a, 100)).toBe(fill(b, 100));
  expect(a.visualMap.inRange.color).toEqual(heatRamp(chartThemeFor(dark)));
  expect(new Set([1, 10, 100, 1000, 3000].map(n => fill(a, n))).size).toBe(5);
});
it("Part 10 derives nearest-rank p99 from occupied cells and saturates outliers", () => {
  const o = option([0, null, -1, ...Array.from({ length: 99 }, () => 3000), 1e9]);
  expect(o.series[0].data).toHaveLength(100);
  expect(o.visualMap.text).toEqual(["3k", "1"]);
  expect(fill(o, 1e9)).toBe(o.visualMap.inRange.color[6]);
});
it("Part 10 legend labels real endpoints and two interior log ticks", () => {
  const o = option([1, 10, 100, 1000, 3000]);
  expect(o.visualMap.text).toEqual(["3k", "1"]);
  const ticks = o.graphic.flatMap((g: any) => g.children ?? []).filter((g: any) => g.type === "text");
  expect(ticks.map((g: any) => g.style.text)).toEqual(["10", "100"]);
  expect(ticks[0].x).toBeCloseTo(-96 + 96 / Math.log10(3000));
  expect(ticks[1].x).toBeCloseTo(-96 + 192 / Math.log10(3000));
});
it("Part 10 handles empty and unit caps without non-finite steps", () => {
  expect(option([0, null]).series[0].data).toEqual([]);
  expect(heatStep(1, 1)).toBe(0);
  expect(heatStep(3000, 3000)).toBe(6);
  expect(heatStep(10000, 3000)).toBe(6);
});
