import { expect, it } from "vitest";
import { readFileSync } from "node:fs";
const source = (file: string): string => readFileSync(`src/dashboards/${file}`, "utf8");
it("removes the test-only map layout boundary and misleading zoom state", () => {
  expect(source("viz/service-map-layout.ts")).not.toMatch(/export function layoutServiceMap\(/);
  expect(source("viz/service-map.tsx")).not.toMatch(/export \{|zoomed/);
  expect(source("panel-card.tsx")).not.toContain("mapView?.zoomed");
});
it("removes map geometry test instrumentation from production", () => {
  expect(/data-(pan-y|text-width)/.test(source("viz/service-map.tsx"))).toBe(false);
});
it("removes obsolete map zoom exercises, duplicate assertions and chart mock keys", () => {
  const map = source("service-map.test.tsx");
  expect(map).not.toMatch(/ctrlKey|const zoom = 1/);
  expect(map).not.toMatch(/expect\(viewport.scrollLeft\)\.toBe\(0\);expect\(viewport.scrollLeft\)/);
  expect(source("viz-regressions.test.tsx")).not.toMatch(/GaugeChart|GraphChart/);
});
