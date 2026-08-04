import { describe, expect, it } from "vitest";
import { compactDashboardLayout, nextDashboardRow, type DashboardLayoutItem } from "./dashboard-layout";

const layout: DashboardLayoutItem[] = [
  { i: "health", x: 0, y: 0, w: 4, h: 4, minW: 3 },
  { i: "topology", x: 4, y: 0, w: 8, h: 6, minW: 4 },
  { i: "assistant", x: 0, y: 6, w: 12, h: 3, minW: 4 },
];

describe("dashboard layout", () => {
  it("places new widgets on a finite row below every existing widget", () => {
    expect(nextDashboardRow(layout)).toBe(9);
    expect(Number.isFinite(nextDashboardRow(layout))).toBe(true);
    expect(nextDashboardRow([])).toBe(0);
  });

  it("clamps minimum widths to the responsive column count", () => {
    const mobile = compactDashboardLayout(layout, 1);
    expect(mobile.every((item) => item.x === 0 && item.w === 1 && item.minW === 1)).toBe(true);

    const tablet = compactDashboardLayout(layout, 6);
    expect(tablet.every((item) => item.w === 6)).toBe(true);
    expect(tablet.map((item) => item.minW)).toEqual([3, 4, 4]);
  });
});
