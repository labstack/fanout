import type { Cell, Frame, Panel } from "./types";

export type Series = { name: string; unit?: string; points: [number, number | null][] };
export type Categories = { categories: string[]; series: { name: string; unit?: string; values: (number | null)[] }[] };

const num = (cell: Cell | undefined): number | null => (typeof cell === "number" && Number.isFinite(cell) ? cell : null);

function measureColumns(frame: Frame): number[] {
  return frame.columns.flatMap((column, index) => (column.role === "measure" ? [index] : []));
}

/** Time series: with a dimension, one series per dimension value (first
 *  measure); without, one series per measure. Order of first appearance. */
export function toSeries(frame: Frame): Series[] {
  const time = frame.columns.findIndex((c) => c.role === "time");
  if (time < 0) return [];
  const dim = frame.columns.findIndex((c) => c.role === "dimension");
  const measures = measureColumns(frame);
  if (dim >= 0 && measures.length > 0) {
    const m = measures[0];
    const byName = new Map<string, Series>();
    for (let row = 0; row < frame.rows; row += 1) {
      const name = String(frame.values[dim][row] ?? "");
      let s = byName.get(name);
      if (!s) {
        s = { name: name || "(none)", unit: frame.columns[m].unit, points: [] };
        byName.set(name, s);
      }
      s.points.push([Number(frame.values[time][row]), num(frame.values[m][row])]);
    }
    return [...byName.values()];
  }
  return measures.map((m) => ({
    name: frame.columns[m].name,
    unit: frame.columns[m].unit,
    points: frame.values[time].map((t, row) => [Number(t), num(frame.values[m][row])] as [number, number | null]),
  }));
}

/** Bars: the first dimension is the category; a second dimension splits
 *  series; otherwise each measure is a series. */
export function toCategories(frame: Frame): Categories {
  const dims = frame.columns.flatMap((c, i) => (c.role === "dimension" ? [i] : []));
  const measures = measureColumns(frame);
  if (dims.length === 0 || measures.length === 0) return { categories: [], series: [] };
  const cat = dims[0];
  const categories: string[] = [];
  for (let row = 0; row < frame.rows; row += 1) {
    const value = String(frame.values[cat][row] ?? "");
    if (!categories.includes(value)) categories.push(value);
  }
  if (dims.length > 1) {
    const split = dims[1];
    const m = measures[0];
    const groups = new Map<string, (number | null)[]>();
    for (let row = 0; row < frame.rows; row += 1) {
      const group = String(frame.values[split][row] ?? "");
      if (!groups.has(group)) groups.set(group, categories.map(() => null));
      groups.get(group)![categories.indexOf(String(frame.values[cat][row] ?? ""))] = num(frame.values[m][row]);
    }
    return { categories, series: [...groups.entries()].map(([name, values]) => ({ name, unit: frame.columns[m].unit, values })) };
  }
  return { categories, series: measures.map((m) => ({ name: frame.columns[m].name, unit: frame.columns[m].unit, values: frame.values[m].map(num) })) };
}

function reduce(values: (number | null)[], reducer: string): number | null {
  const present = values.filter((v): v is number => v !== null);
  if (present.length === 0) return null;
  switch (reducer) {
    case "last": return present[present.length - 1];
    case "min": return Math.min(...present);
    case "max": return Math.max(...present);
    case "sum": return present.reduce((a, b) => a + b, 0);
    default: return present.reduce((a, b) => a + b, 0) / present.length;
  }
}

/** The headline number: the window total for reduce=window, otherwise the
 *  series reduced in the browser. Never the last partial bucket by accident
 *  (#232 item 1). */
export function statValue(panel: Panel, frame: Frame): number | null {
  const m = measureColumns(frame)[0];
  if (m === undefined) return null;
  if ((panel.reduce ?? "window") === "window") {
    if (frame.totals) return num(frame.totals[m]);
    return reduce(frame.values[m].map(num), "mean");
  }
  return reduce(frame.values[m].map(num), panel.reduce ?? "mean");
}

export function sparkline(frame: Frame): (number | null)[] {
  const m = measureColumns(frame)[0];
  return m === undefined ? [] : frame.values[m].map(num);
}
