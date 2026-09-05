export type DashboardLayoutItem = {
  i: string;
  x: number;
  y: number;
  w: number;
  h: number;
  minW?: number;
  minH?: number;
};

export function nextDashboardRow(layout: DashboardLayoutItem[]): number {
  return layout.reduce((bottom, item) => Math.max(bottom, item.y + item.h), 0);
}

export function compactDashboardLayout(layout: DashboardLayoutItem[], columns: number): DashboardLayoutItem[] {
  return layout.map((item) => ({
    ...item,
    x: 0,
    w: columns,
    minW: Math.min(item.minW ?? 1, columns),
  }));
}

export type WidgetType = "overview" | "topology" | "activity" | "assistant" | "performance" | "trace" | "logs";

/** Mirrors WidgetTypes in internal/dashboard/service.go. */
export const widgetTypes: WidgetType[] = ["overview", "topology", "activity", "assistant", "performance", "trace", "logs"];

/** Grid units: twelve columns, 76px rows. Sized so a card's default shape
 *  matches its content instead of leaving a blank band under it. */
export const widgetDefaults: Record<WidgetType, { w: number; h: number; minW: number; minH: number }> = {
  overview: { w: 4, h: 4, minW: 3, minH: 4 },
  activity: { w: 4, h: 5, minW: 3, minH: 4 },
  assistant: { w: 4, h: 3, minW: 3, minH: 3 },
  topology: { w: 8, h: 5, minW: 4, minH: 4 },
  performance: { w: 8, h: 5, minW: 4, minH: 4 },
  logs: { w: 8, h: 5, minW: 4, minH: 4 },
  trace: { w: 8, h: 4, minW: 4, minH: 4 },
};

function overlaps(a: { x: number; y: number; w: number; h: number }, b: { x: number; y: number; w: number; h: number }) {
  return a.x < b.x + b.w && b.x < a.x + a.w && a.y < b.y + b.h && b.y < a.y + a.h;
}

/** First free position for a w×h widget on the last row, else a new row. */
export function nextDashboardSlot(layout: DashboardLayoutItem[], w: number, h: number, columns = 12): { x: number; y: number } {
  if (layout.length === 0) return { x: 0, y: 0 };
  const rowY = Math.max(...layout.map((item) => item.y));
  for (let x = 0; x + w <= columns; x += 1) {
    const candidate = { x, y: rowY, w, h };
    if (!layout.some((item) => overlaps(item, candidate))) return { x, y: rowY };
  }
  return { x: 0, y: nextDashboardRow(layout) };
}
