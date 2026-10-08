import { htmlTooltip } from "./escape";
import { chartTheme, seriesSlot, statusHex } from "../chart";
import { fonts } from "../tokens";
import { toCategories, toSeries } from "./frame";
import { hiddenSeriesNote, isOtherSeries, visibleSeries, wrappingLegend } from "./series";
import { statusFor } from "./thresholds";
import type { Frame, Panel, PanelResult, Status } from "./types";
import { formatAxis, formatLabel, formatTimeAxis, formatValue } from "./units";
import { lineStyle, markStyle } from "./style";

export type ChartTheme = { dark: boolean; text: string; muted: string; grid: string; surface: string; border: string; status: { ok: string; warn: string; bad: string }; font: string };

export function chartThemeFor(dark: boolean): ChartTheme {
  const base = chartTheme(dark);
  const status = statusHex(dark);
  return { dark, text: base.text, muted: base.muted, grid: base.grid, surface: base.surface, border: base.border, status: { ok: status.ok, warn: status.warn, bad: status.bad }, font: fonts.display };
}

type Option = Record<string, unknown>;

const colorFor = (name: string, index: number, theme: ChartTheme) => (isOtherSeries(name) ? theme.muted : seriesSlot(index, theme.dark));

function chartSeries(frame: Frame, panel: Panel) {
  const series = toSeries(frame);
  if (!frame.columns.some(column => column.role === "dimension")) {
    for (const item of series) {
      const name = item.name;
      item.name = /^p\d+(?:\(.*\))?$/.test(name) && ["ms", "s", "ns"].includes(item.unit ?? "") ? `${name.match(/^p\d+/)![0]} latency`
        : /^error_rate(?:\(\))?$/.test(name) ? "Error rate"
        : /^rate(?:\(\))?$/.test(name) ? "Request rate"
        : name.replaceAll("_", " ").replace(/^./, letter => letter.toUpperCase());
    }
  }
  return visibleSeries(series, panel);
}

function baseOption(theme: ChartTheme, unit?: string): Option {
  return {
    animationDuration: 200,
    textStyle: { fontFamily: theme.font, color: theme.muted, fontSize: 11 },
    grid: { left: 8, right: 16, top: 28, bottom: 8, containLabel: true },
    tooltip: {
      trigger: "axis",
      formatter: htmlTooltip(value => formatValue(unit, value)),
      confine: true,
      backgroundColor: theme.surface,
      borderColor: theme.border,
      textStyle: { color: theme.text, fontSize: 12 },
      valueFormatter: (value: number) => formatValue(unit, value),
      axisPointer: { type: "line", lineStyle: { color: theme.border } },
    },
    aria: { enabled: true },
  };
}

/** Keep a threshold above the data in view instead of clipping it. */
function thresholdMax(panel: Panel): ((range: { max: number }) => number) | undefined {
  const values = (panel.thresholds ?? []).map((t) => t.value);
  if (values.length === 0) return undefined;
  const highest = Math.max(...values);
  return (range) => Math.max(range.max, highest);
}

function thresholdLines(panel: Panel, theme: ChartTheme, unit?: string) {
  return (panel.thresholds ?? []).map((t) => ({
    yAxis: t.value,
    label: { formatter: `${t.label ?? t.status} ${formatAxis(unit ?? panel.unit)(t.value).replace(/(?<=\d)(ms|s|m|h)$/, " $1")}`, position: "insideStartTop", color: theme.muted, textBorderColor: theme.surface, textBorderWidth: 3, fontSize: 12 },
    lineStyle: { color: theme.status[t.status], type: [4, 3], width: 1 },
  }));
}

/** A time series: one line per series, the previous period dashed and
 *  recessive, thresholds as labelled lines, stacking only when asked (the
 *  server refuses to stack non-additive measures). */
export function timeseriesOption(panel: Panel, result: PanelResult, theme: ChartTheme, size: ChartSize = { width: 500, height: 248 }): Option {
  const visible = result.frame ? chartSeries(result.frame, panel) : { shown: [], hidden: 0 };
  const current = visible.shown;
  const unit = current[0]?.unit ?? panel.unit;
  const currentNames = new Set(current.map(s => s.name));
  const other = current.find(s => isOtherSeries(s.name));
  const previousSeries = result.previous && result.shift_ms !== undefined ? chartSeries(result.previous, panel).shown.flatMap(s => {
    if (isOtherSeries(s.name)) return other ? [s] : [];
    return currentNames.has(s.name) ? [s] : [];
  }) : [];
  const units = [...new Set([...current, ...previousSeries].map((s) => s.unit ?? panel.unit))];
  if (units.length === 0) units.push(panel.unit);
  const style = panel.options?.style ?? "line";
  const direct = current.length >= 2 && current.length <= 6 && visible.hidden === 0 && style !== "bars" && style !== "stacked";
  const endWidth = direct ? Math.min(size.width * .3, 180, Math.max(40, ...current.map(s => (size.measureText?.(s.name, `12px ${theme.font}`) ?? Array.from(s.name).length * 7.2) + 8))) : 0;
  const period = result.shift_ms;
  const lines = current.map((s, i) => {
    const color = colorFor(s.name, i, theme);
    return {
      name: s.name,
      yAxisIndex: units.indexOf(s.unit ?? panel.unit),
      tooltip: { valueFormatter: (value: number) => formatValue(s.unit ?? panel.unit, value) },
      type: style === "bars" || style === "stacked" ? "bar" : "line",
      data: s.points,
      endLabel: direct ? { show: true, formatter: "{a}", color: theme.muted, fontFamily: theme.font, fontSize: 12, distance: 6, width: endWidth - 8, overflow: "truncate", ellipsis: "…" } : undefined,
      labelLayout: { moveOverlap: "shiftY" },
      showSymbol: false,
      connectNulls: false,
      sampling: style === "bars" || style === "stacked" ? undefined : "lttb",
      stack: style === "stacked" ? "total" : undefined,
      areaStyle: style === "area" ? { opacity: 0.12 } : undefined,
      lineStyle: { width: 2, ...lineStyle(color,theme) },
      itemStyle: { ...markStyle(color,theme), borderRadius: style === "bars" || style === "stacked" ? [2, 2, 0, 0] : undefined },
      barMaxWidth: 12,
      emphasis: { focus: "series" },
      markLine: i === 0 && (panel.thresholds?.length ?? 0) > 0 ? { symbol: ["none", "none"], silent: true, data: thresholdLines(panel, theme, unit) } : undefined,
    };
  });
  const previous = period !== undefined ? previousSeries.map((s) => ({
    name: `${s.name} · previous`,
    yAxisIndex: units.indexOf(s.unit ?? panel.unit),
    tooltip: { valueFormatter: (value: number) => formatValue(s.unit ?? panel.unit, value) },
    type: "line",
    data: s.points.map(([t, v]) => [t + period, v]),
    showSymbol: false,
    silent: true,
    z: 1,
    lineStyle: { width: 1, type: "dashed", color: theme.muted },
    itemStyle: { color: theme.muted },
  })) : [];
  const legend = (panel.options?.legend ?? "auto") !== "hidden" && current.length > 1;
  const legendLayout = wrappingLegend(current.map(s => s.name), size.width, legend, theme.text, theme.font, size.measureText);
  const plotHeight = size.height - legendLayout.top - (visible.hidden ? 24 : 8) - 22;
  const axes = units.map((axisUnit, i) => ({ splitNumber: Math.max(2, Math.floor(plotHeight / 32)), type: panel.options?.scale === "log" ? "log" : "value", position: i === 0 ? "left" : "right", offset: Math.max(0, i - 1) * 56, max: i === 0 ? thresholdMax(panel) : undefined, axisLine: { show: false }, splitLine: { show: i === 0, lineStyle: { color: theme.grid } }, axisLabel: { color: theme.muted, fontFamily: theme.font, fontSize: 11, hideOverlap: true, formatter: formatAxis(axisUnit) } }));
  return {
    ...baseOption(theme, unit),
    tooltip: { ...(baseOption(theme, unit).tooltip as object), formatter: htmlTooltip((value, name) => formatValue([...current, ...previousSeries].find(s => s.name === name || `${s.name} · previous` === name)?.unit ?? panel.unit, value), true) },
    legend: legendLayout.option,
    grid: { left: 8, right: 16 + (direct ? endWidth : 0) + Math.max(0, units.length - 2) * 56, top: legendLayout.top, bottom: visible.hidden ? 24 : 8, containLabel: true },
    xAxis: { type: "time", axisLine: { lineStyle: { color: theme.grid } }, axisTick: { show: false }, splitLine: { show: false }, axisLabel: { color: theme.muted, fontFamily: theme.font, fontSize: 11, hideOverlap: true, formatter: formatTimeAxis } },
    yAxis: axes.length === 1 ? axes[0] : axes,
    series: [...lines, ...previous],
    graphic: hiddenSeriesNote(visible.hidden, theme.muted),
  };
}

/** Horizontal bars sorted by the server, value labels at the bar end. */
function middleEllipsis(text: string, width: number, measure: (text: string) => number): string {
  if (measure(text) <= width) return text;
  const chars = Array.from(text);
  for (let n = chars.length - 1; n > 0; n--) {
    const prefix = Math.floor(n * .45), suffix = n - prefix;
    const label = chars.slice(0, prefix).join("") + "…" + chars.slice(-suffix).join("");
    if (measure(label) <= width) return label;
  }
  return "…";
}

export function barOption(panel: Panel, frame: Frame, theme: ChartTheme, size: ChartSize = { width: 500, height: 248 }): Option {
  const pivot = toCategories(frame);
  const categories = pivot.categories;
  const { shown: series, hidden } = visibleSeries(pivot.series, panel);
  const dims = frame.columns.flatMap((column, index) => column.role === "dimension" ? [index] : []);
  const selections = new Map<string, { dimensions: Record<string, string>; from?: string; to?: string }>();
  for (let row = 0; row < frame.rows; row++) {
    const dimensions: Record<string, string> = {};
    let window: {from: string; to: string} | undefined;
    let groupIndex = 0;
    for (const index of dims) {
      const value = String(frame.values[index][row] ?? "");
      if (frame.periods && frame.columns[index].name === "period") window = frame.periods[value];
      else dimensions[panel.query?.by?.[groupIndex++] ?? frame.columns[index].name] = value;
    }
    selections.set(JSON.stringify(dims.map(index => String(frame.values[index][row] ?? ""))), { dimensions, ...window });
  }
  const unit = series[0]?.unit ?? panel.unit;
  const units = [...new Set(series.map((s) => s.unit ?? panel.unit))];
  if (units.length === 0) units.push(panel.unit);
  const multi = series.length > 1;
  const split = Boolean(frame.periods?.["Before deploy"] || frame.periods?.["Since deploy"]);
  const measure = (text: string) => size.measureText?.(text, `11px ${theme.font}`) ?? Array.from(text).length * 6.6;
  const categoryWidth = Math.min(size.width * .4, Math.max(40, ...categories.map(measure)));
  const legendLayout = wrappingLegend(series.map(s => s.name), size.width, multi && panel.options?.legend !== "hidden", theme.text, theme.font, size.measureText);
  const axes = units.map((axisUnit, i) => ({ type: "value", position: i === 0 ? "bottom" : "top", offset: Math.max(0, i - 1) * 36, splitLine: { show: i === 0, lineStyle: { color: theme.grid } }, axisLabel: { color: theme.muted, fontFamily: theme.font, fontSize: 11, formatter: (v: number) => formatLabel(axisUnit,v), hideOverlap: true } }));
  return {
    ...baseOption(theme, unit),
    tooltip: { ...(baseOption(theme, unit).tooltip as object), formatter: htmlTooltip((value, name) => formatValue(series.find(s => s.name === name)?.unit ?? panel.unit, value)), axisPointer: { type: "shadow" } },
    legend: legendLayout.option,
    grid: { left: 8, right: 56, top: (multi ? legendLayout.top : 8) + Math.max(0, units.length - 1) * 36, bottom: hidden ? 24 : 8, containLabel: true },
    yAxis: { type: "category", inverse: true, data: categories, splitLine: {show:false}, axisLine: { lineStyle: { color: theme.grid } }, axisTick: { show: false }, axisLabel: { color: theme.muted, fontFamily: theme.font, fontSize: 11, width: categoryWidth, overflow: "truncate", formatter: (text: string) => middleEllipsis(text,categoryWidth,measure) } },
    xAxis: axes.length === 1 ? axes[0] : axes,
    series: series.map((s, i) => ({
      name: s.name,
      xAxisIndex: units.indexOf(s.unit ?? panel.unit),
      tooltip: { valueFormatter: (value: number) => formatValue(s.unit ?? panel.unit, value) },
      type: "bar",
      data: s.values.map((value, row) => ({ value, selection: selections.get(JSON.stringify(dims.length > 1 ? [categories[row], s.name] : [categories[row]])) })),
      barMaxWidth: split && s.name === "Before deploy" ? 7 : 14,
      barGap: "20%",
      itemStyle: { ...markStyle(split ? s.name === "Before deploy" ? theme.muted : seriesSlot(0,theme.dark) : colorFor(s.name, i, theme),theme), borderRadius: [0, 4, 4, 0] },
      label: { show: split ? s.name === "Since deploy" : !multi || i === 0, position: "right", color: theme.text, fontFamily: theme.font, fontSize: 11, formatter: ({ value }: { value: number }) => formatLabel(s.unit ?? panel.unit, value) },
    })),
    graphic: hiddenSeriesNote(hidden, theme.muted),
  };
}

/** A gauge with its thresholds as coloured bands. */
export type ChartSize = { width: number; height: number; measureText?: (text: string, font: string) => number };

export function gaugeOption(panel: Panel, value: number | null, theme: ChartTheme, unit = panel.unit as string | undefined, size: ChartSize = { width: 270, height: 140 }): Option {
  const min = panel.min ?? 0;
  const max = panel.max ?? 100;
  const bands = gaugeBands(panel, min, max, theme);
  const text = formatValue(unit, value);
  const status = statusFor(value, panel.thresholds, panel.better);
  const cue = status === "bad" ? "◆ Bad" : status === "warn" ? "■ Warn" : status === "ok" ? "● OK" : value === null ? "○ Unknown" : undefined;
  const mute = (hex: string) => `${hex}40`;
  const valueWidth = size.measureText?.(text,`600 28px ${theme.font}`) ?? text.length * 28 * .6;
  const cueWidth = size.measureText?.(cue ?? "",`11px ${theme.font}`) ?? (cue?.length ?? 0) * 11 * .6;
  const stacked = size.width < valueWidth + cueWidth + 16;
  const top = Math.max(0, (size.height - (stacked ? 78 : 62)) / 2), trackY = top + (stacked ? 50 : size.height < 62 ? 32 : 34);
  const trackWidth = Math.max(1, size.width - 8), fraction = Math.max(0, Math.min(1, ((value ?? min) - min) / (max - min || 1)));
  let start = 0;
  return {
    animation: false, textStyle: { fontFamily: theme.font, color: theme.text }, series: [],
    graphic: [
      { id: "gauge-value", type: "text", left: 4, top, style: { text, fill: theme.text, fontSize: 28, fontWeight: 600, fontFamily: theme.font } },
      ...(cue ? [{ type: "text", right: 4, top: top + (stacked ? 30 : 10), style: { text: cue, fill: theme.text, fontSize: 11, fontFamily: theme.font } }] : []),
      { id: "gauge-track", type: "rect", shape: { x: 4, y: trackY, width: trackWidth, height: 10, r: 5 }, style: { fill: theme.grid } },
      ...bands.map(([end, color],i) => { const x = start; start = end; return { type: "rect", shape: { x: 4 + x * trackWidth, y: trackY, width: (end - x) * trackWidth, height: 10, r: [i===0?5:0,i===bands.length-1?5:0,i===bands.length-1?5:0,i===0?5:0] }, style: { fill: mute(color) } }; }),
      { id: "gauge-fill", type: "rect", shape: { x: 4, y: trackY, width: fraction * trackWidth, height: 10, r: 5 }, style: { fill: status ? theme.status[status] : seriesSlot(0, theme.dark) } },
      { id: "gauge-marker", type: "rect", shape: { x: 3 + fraction * trackWidth, y: trackY - 2, width: 2, height: 14, r: 1 }, style: { fill: theme.text } },
      { type: "text", left: 4, top: trackY + 12, style: { text: formatValue(unit, min), fill: theme.muted, fontSize: 11, fontFamily: theme.font } },
      { type: "text", right: 4, top: trackY + 12, style: { text: formatValue(unit, max), fill: theme.muted, fontSize: 11, fontFamily: theme.font } },
    ],
  };

}

/** Gauge bands coloured by statusFor at each band's midpoint, so the gauge and
 *  the stat card can never disagree; adjacent bands of one status merge. */
export function gaugeBands(panel: Panel, min: number, max: number, theme: ChartTheme): [number, string][] {
  const span = max - min || 1;
  const edges = [min, ...(panel.thresholds ?? []).map((t) => Math.min(max, Math.max(min, t.value))), max].sort((a, b) => a - b);
  const bands: { end: number; status: Status }[] = [];
  for (let i = 0; i < edges.length - 1; i += 1) {
    if (edges[i + 1] === edges[i]) continue;
    const status = statusFor((edges[i] + edges[i + 1]) / 2, panel.thresholds, panel.better) ?? "ok";
    const last = bands[bands.length - 1];
    if (last && last.status === status) last.end = edges[i + 1];
    else bands.push({ end: edges[i + 1], status });
  }
  if (bands.length === 0) bands.push({ end: max, status: "ok" });
  return bands.map((b) => [(b.end - min) / span, theme.status[b.status]]);
}
