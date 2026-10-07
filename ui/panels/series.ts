import type { Panel } from "./types";

/** Preserve chart order, reserving the muted Other group outside the six slots. */
export function seriesGroups<T extends { name: string }>(items: T[], panel: Panel): { name: string; items: T[] }[] {
  const limit = Math.min(6, Math.max(1, Math.floor(panel.options?.top || 6)));
  const named = items.filter(item => item.name !== "Other");
  const groups = named.slice(0, limit).map(item => ({ name: item.name, items: [item] }));
  const other = [...items.filter(item => item.name === "Other"), ...named.slice(limit)];
  if (other.length) groups.push({ name: "Other", items: other });
  return groups;
}

export function sumPresent(values: (number | null)[]): number | null {
  const present = values.filter((value): value is number => value !== null);
  return present.length ? present.reduce((sum, value) => sum + value, 0) : null;
}

/** Chart measures are not always additive (p95, rates), so series past the
 *  six slots are left out rather than summed into Other. A server-computed
 *  Other is kept as it is. `hidden` counts what was left out. */
export function visibleSeries<T extends { name: string }>(items: T[], panel: Panel): { shown: T[]; hidden: number } {
  const limit = Math.min(6, Math.max(1, Math.floor(panel.options?.top || 6)));
  const named = items.filter(item => item.name !== "Other");
  return { shown: [...named.slice(0, limit), ...items.filter(item => item.name === "Other")], hidden: Math.max(0, named.length - limit) };
}

export function hiddenSeriesNote(hidden: number, muted: string): Record<string, unknown>[] {
  return hidden > 0 ? [{ type: "text", right: 8, bottom: 2, silent: true, style: { text: `${hidden} more series not shown`, fill: muted, fontSize: 11 } }] : [];
}
