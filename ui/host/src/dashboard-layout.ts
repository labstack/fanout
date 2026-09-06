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
  overview: { w: 4, h: 3, minW: 3, minH: 3 },
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

/** First free position for a w×h widget on the last row, else a narrower slot in
 *  the gap left on that row, else a new row. The narrow slot is what keeps a
 *  dashboard of eight-wide cards from leaving a four-column strip empty down its
 *  whole length: a card that will not fit at its default width is placed at the
 *  width the row has left, as long as that is still at least `minW`. */
export function nextDashboardSlot(layout: DashboardLayoutItem[], w: number, h: number, columns = 12, minW = w): { x: number; y: number; w: number } {
  if (layout.length === 0) return { x: 0, y: 0, w };
  const rowY = Math.max(...layout.map((item) => item.y));
  const free = (x: number, width: number) => !layout.some((item) => overlaps(item, { x, y: rowY, w: width, h }));
  for (let x = 0; x + w <= columns; x += 1) {
    if (free(x, w)) return { x, y: rowY, w };
  }
  const edge = layout.reduce((right, item) => (overlaps(item, { x: 0, y: rowY, w: columns, h }) ? Math.max(right, item.x + item.w) : right), 0);
  let best = { x: edge, w: 0 };
  for (let start = Math.min(edge, columns); start < columns;) {
    if (!free(start, 1)) { start += 1; continue; }
    let end = start;
    while (end < columns && free(end, 1)) end += 1;
    if (end - start > best.w) best = { x: start, w: end - start };
    start = end;
  }
  if (best.w >= minW) return { x: best.x, y: rowY, w: best.w };
  return { x: 0, y: nextDashboardRow(layout), w };
}
