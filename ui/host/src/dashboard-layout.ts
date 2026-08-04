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
