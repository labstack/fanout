import { expect, it } from "vitest";
import { analysisOption } from "../../../panels/analysis";
import { heatRamp, heatStep } from "../../../panels/heat-scale";
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
  return series.renderItem({}, { value: (i: number) => point.value[i], coord: (v: number[]) => [v[0] / 1000, 20], size: () => [60, 20], visual: () => "#fff" }).style.fill;
}

it("maps decades to distinct monotonic log steps with cap 3,000", () => {
  expect([1, 10, 100, 1000, 3000].map(n => heatStep(n, 3000))).toEqual([0, 1, 3, 5, 6]);
});
it("keeps low counts in the bottom steps of a fixed magnitude scale", () => {
  expect([1, 2, 3, 4, 5].map(n => heatStep(n, 3000))).toEqual([0, 0, 0, 1, 1]);
});
it.each([false, true])("equal counts share colours across distributions with the same p99 cap (%s)", dark => {
  const a = option([1, 10, 100, 1000, 3000], dark), b = option([100, 3000, 3000, 3000, 3000], dark);
  expect(fill(a, 100)).toBe(fill(b, 100));
  expect(a.visualMap.inRange.color).toEqual(heatRamp(chartThemeFor(dark)));
  expect(new Set([1, 10, 100, 1000, 3000].map(n => fill(a, n))).size).toBe(5);
});
it("derives nearest-rank p99 from occupied cells and saturates outliers", () => {
  const o = option([0, null, -1, ...Array.from({ length: 99 }, () => 3000), 1e9]);
  expect(o.series[0].data).toHaveLength(100);
  expect(o.visualMap.text).toEqual(["3k", "1"]);
  expect(fill(o, 1e9)).toBe(o.visualMap.inRange.color[6]);
});
it.each([false, true])("shows only one heatmap scale label per end with no extra positioned text (%s)", dark => {
  const o = option([1, 10, 100, 1000, 3000], dark);
  expect(o.visualMap.text).toEqual(["3k", "1"]);
  expect(o.visualMap.orient).toBe("horizontal");
  expect(o.visualMap.inRange.color).toEqual(heatRamp(chartThemeFor(dark)));
  expect(o.graphic).toEqual([]);
});
it("handles empty and unit caps without non-finite steps", () => {
  expect(option([0, null]).series[0].data).toEqual([]);
  expect(heatStep(1, 1)).toBe(0);
  expect(heatStep(3000, 3000)).toBe(6);
  expect(heatStep(10000, 3000)).toBe(6);
});
