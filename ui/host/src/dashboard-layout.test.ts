import { describe, expect, it } from "vitest";
import { compactDashboardLayout, nextDashboardRow, type DashboardLayoutItem, nextDashboardSlot, widgetDefaults, widgetTypes } from "./dashboard-layout";

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

describe("widget placement", () => {
  it("fills the free space on the last row before starting a new one", () => {
    expect(nextDashboardSlot([], 4, 4)).toEqual({ x: 0, y: 0 });
    expect(nextDashboardSlot([{ i: "a", x: 0, y: 0, w: 4, h: 4 }], 8, 5)).toEqual({ x: 4, y: 0 });
    expect(nextDashboardSlot([{ i: "a", x: 0, y: 0, w: 4, h: 4 }, { i: "b", x: 4, y: 0, w: 4, h: 4 }], 4, 4)).toEqual({ x: 8, y: 0 });
    expect(nextDashboardSlot([{ i: "a", x: 0, y: 0, w: 4, h: 4 }, { i: "b", x: 4, y: 0, w: 8, h: 6 }], 4, 4)).toEqual({ x: 0, y: 6 });
  });

  it("considers only the last row when looking for a gap", () => {
    const layout: DashboardLayoutItem[] = [
      { i: "a", x: 0, y: 0, w: 4, h: 4 },
      { i: "b", x: 0, y: 4, w: 8, h: 4 },
    ];
    expect(nextDashboardSlot(layout, 4, 4)).toEqual({ x: 8, y: 4 });
    expect(nextDashboardSlot(layout, 6, 4)).toEqual({ x: 0, y: 8 });
  });

  it("gives every widget type a default size that fits twelve columns", () => {
    expect(widgetTypes).toEqual(["overview", "topology", "activity", "assistant", "performance", "trace", "logs"]);
    for (const type of widgetTypes) {
      const size = widgetDefaults[type];
      expect(size.w).toBeLessThanOrEqual(12);
      expect(size.minW).toBeLessThanOrEqual(size.w);
      expect(size.minH).toBeLessThanOrEqual(size.h);
    }
    expect(widgetDefaults.assistant.w).toBe(4);
    expect(widgetDefaults.topology).toEqual({ w: 8, h: 5, minW: 4, minH: 4 });
  });
});
