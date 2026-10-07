import { escapeHTML } from "./escape";
import type { Panel } from "./types";

export const isOtherSeries = (name?: string) => /^Other(?: \(\d+\))?$/.test(name ?? "");

/** Preserve chart order, reserving the muted Other group outside the six slots. */
export function seriesGroups<T extends { name: string }>(items: T[], panel: Panel): { name: string; items: T[] }[] {
  const limit = Math.min(6, Math.max(1, Math.floor(panel.options?.top || 6)));
  const named = items.filter(item => !isOtherSeries(item.name));
  const groups = named.slice(0, limit).map(item => ({ name: item.name, items: [item] }));
  const other = [...items.filter(item => isOtherSeries(item.name)), ...named.slice(limit)];
  if (other.length) groups.push({ name: other.length === 1 && isOtherSeries(other[0].name) ? other[0].name : `Other (${other.reduce((sum, item) => sum + (isOtherSeries(item.name) ? Number(item.name.match(/\d+/)?.[0] ?? 1) : 1), 0)})`, items: other });
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
  const named = items.filter(item => !isOtherSeries(item.name));
  return { shown: [...named.slice(0, limit), ...items.filter(item => isOtherSeries(item.name))], hidden: Math.max(0, named.length - limit) };
}

export function hiddenSeriesNote(hidden: number, muted: string): Record<string, unknown>[] {
  return hidden > 0 ? [{ type: "text", right: 8, bottom: 2, silent: true, style: { text: `${hidden} more series not shown`, fill: muted, fontSize: 11 } }] : [];
}

/** Match the legend's explicit icon, text and gap sizes, reserving each wrapped
 * row. The conservative text budget also works before canvas fonts are ready. */
export function wrappingLegend(names: string[], width: number, show: boolean, color: string, font = "sans-serif", measureText?: (text: string, font: string) => number) {
  const formatter = (name: string) => Array.from(name).length > 24 ? `${Array.from(name).slice(0, 23).join("")}…` : name;
  const available = Math.max(1, width - 16);
  let rows = 1, used = 0;
  for (const name of names) {
    const entry = 15 + (measureText?.(formatter(name), `12px ${font}`) ?? Array.from(formatter(name)).length * 7.2);
    if (used && used + 10 + entry > available) { rows++; used = 0; }
    used += (used ? 10 : 0) + entry;
  }
  const scroll = rows > 2;
  return { top: show ? (scroll ? 30 : rows * 26 + 4) : 12,
    option: { type: scroll ? "scroll" : "plain", show, top: 0, left: 0, right: 0, padding: 0, itemGap: 10,
      icon: "roundRect", itemWidth: 10, itemHeight: 10, textStyle: { color, fontSize: 12, lineHeight: 16 },
      data: names, formatter, tooltip: { show: true, renderMode: "html", formatter: (params: { name: string }) => escapeHTML(params.name) } } };
}
