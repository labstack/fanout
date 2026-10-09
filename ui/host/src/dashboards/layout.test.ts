import { readFileSync } from "node:fs";
import { expect, it } from "vitest";
import { defaultRows, fragmentPanelHeight, pixels, rowHeight } from "./layout";

it("uses the stat minimum for default and small fragments, preserving authored heights", () => {
  const stat = { id: "stat", title: "Stat", viz: "stat" as const };
  for (const height of [undefined, "s"] as const) {
    expect(defaultRows({ ...stat, height })).toBe(4);
    expect(fragmentPanelHeight({ ...stat, height })).toBe(pixels(4));
  }
  expect(fragmentPanelHeight({ ...stat, height: "m" })).toBe(pixels(6));
  expect(fragmentPanelHeight({ ...stat, height: "l" })).toBe(pixels(10));
  expect(fragmentPanelHeight({ ...stat, grid: { x: 0, y: 0, w: 3, h: 3 } })).toBe(pixels(3));
});

it("mirrors the Go height tables and row unit for every size and visualization", () => {
  const go = readFileSync("../../internal/dashboard/layout.go", "utf8");
  const table = (name: string) => {
    const match = go.match(new RegExp(`var ${name} = map\\[string\\]int\\{([^}]+)\\}`));
    expect(match, `Go ${name} table`).not.toBeNull();
    return Object.fromEntries([...match![1].matchAll(/"([^"]+)":\s*(\d+)/g)].map(([, key, value]) => [key, Number(value)]));
  };
  const heights = table("heightRows"), minimums = table("minimumRows");
  expect(rowHeight).toBe(Number(go.match(/const RowHeight = (\d+)/)![1]));
  for (const viz of ["stat", "gauge", "timeseries", "bar", "table", "text", "heatmap", "histogram", "scatter", "state_timeline", "logs", "log_patterns", "traces", "service_map", "health"] as const) {
    for (const height of ["s", "m", "l"] as const) {
      expect(defaultRows({ id: "p", title: "P", viz, height })).toBe(Math.max(heights[height], minimums[viz] ?? 0));
    }
  }
});
