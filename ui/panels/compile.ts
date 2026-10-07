import { chartTheme, seriesSlot, statusHex } from "../chart";
import { fonts } from "../tokens";
import { toCategories, toSeries } from "./frame";
import { hiddenSeriesNote, isOtherSeries, visibleSeries, wrappingLegend } from "./series";
import { statusFor } from "./thresholds";
import type { Frame, Panel, PanelResult, Status } from "./types";
import { formatAxis, formatTimeAxis, formatValue } from "./units";

export type ChartTheme = { dark: boolean; text: string; muted: string; grid: string; surface: string; border: string; status: { ok: string; warn: string; bad: string }; font: string };

export function chartThemeFor(dark: boolean): ChartTheme {
  const base = chartTheme(dark);
  const status = statusHex(dark);
  return { dark, text: base.text, muted: base.muted, grid: base.grid, surface: base.surface, border: base.border, status: { ok: status.ok, warn: status.warn, bad: status.bad }, font: fonts.display };
}

type Option = Record<string, unknown>;

const colorFor = (name: string, index: number, theme: ChartTheme) => (isOtherSeries(name) ? theme.muted : seriesSlot(index, theme.dark));

function chartSeries(frame: Frame, panel: Panel) {
  return visibleSeries(toSeries(frame), panel);
}

function baseOption(theme: ChartTheme, unit?: string): Option {
  return {
    animationDuration: 200,
    textStyle: { fontFamily: theme.font, color: theme.muted, fontSize: 11 },
    grid: { left: 8, right: 16, top: 28, bottom: 8, containLabel: true },
    tooltip: {
      trigger: "axis",
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
    label: { formatter: t.label ?? `${t.status} ${formatValue(unit ?? panel.unit, t.value)}`, position: "insideStartTop", color: theme.muted, fontSize: 10 },
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
  const previousSeries = result.previous && result.shift_ms !== undefined ? chartSeries(result.previous, panel).shown : [];
  const units = [...new Set([...current, ...previousSeries].map((s) => s.unit ?? panel.unit))];
  if (units.length === 0) units.push(panel.unit);
  const style = panel.options?.style ?? "line";
  const period = result.shift_ms;
  const lines = current.map((s, i) => {
    const color = colorFor(s.name, i, theme);
    return {
      name: s.name,
      yAxisIndex: units.indexOf(s.unit ?? panel.unit),
      tooltip: { valueFormatter: (value: number) => formatValue(s.unit ?? panel.unit, value) },
      type: style === "bars" || style === "stacked" ? "bar" : "line",
      data: s.points,
      showSymbol: false,
      connectNulls: false,
      sampling: style === "bars" || style === "stacked" ? undefined : "lttb",
      stack: style === "stacked" ? "total" : undefined,
      areaStyle: style === "area" ? { opacity: 0.12 } : undefined,
      lineStyle: { width: 2, color },
      itemStyle: { color, borderRadius: style === "bars" || style === "stacked" ? [2, 2, 0, 0] : undefined },
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
    lineStyle: { width: 1, type: "dashed", color: theme.border },
    itemStyle: { color: theme.border },
  })) : [];
  const legend = (panel.options?.legend ?? "auto") !== "hidden" && current.length > 1;
  const legendLayout = wrappingLegend(current.map(s => s.name), size.width, legend, theme.text, theme.font, size.measureText);
  const axes = units.map((axisUnit, i) => ({ type: panel.options?.scale === "log" ? "log" : "value", position: i === 0 ? "left" : "right", offset: Math.max(0, i - 1) * 56, max: i === 0 ? thresholdMax(panel) : undefined, axisLine: { show: false }, splitLine: { show: i === 0, lineStyle: { color: theme.grid } }, axisLabel: { color: theme.muted, formatter: formatAxis(axisUnit) } }));
  return {
    ...baseOption(theme, unit),
    legend: legendLayout.option,
    grid: { left: 8, right: 16 + Math.max(0, units.length - 2) * 56, top: legendLayout.top, bottom: visible.hidden ? 24 : 8, containLabel: true },
    xAxis: { type: "time", axisLine: { lineStyle: { color: theme.grid } }, axisTick: { show: false }, splitLine: { show: false }, axisLabel: { color: theme.muted, hideOverlap: true, formatter: formatTimeAxis } },
    yAxis: axes.length === 1 ? axes[0] : axes,
    series: [...lines, ...previous],
    graphic: hiddenSeriesNote(visible.hidden, theme.muted),
  };
}

/** Horizontal bars sorted by the server, value labels at the bar end. */
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
  const legendLayout = wrappingLegend(series.map(s => s.name), size.width, multi && panel.options?.legend !== "hidden", theme.text, theme.font, size.measureText);
  const axes = units.map((axisUnit, i) => ({ type: "value", position: i === 0 ? "bottom" : "top", offset: Math.max(0, i - 1) * 36, splitLine: { show: i === 0, lineStyle: { color: theme.grid } }, axisLabel: { color: theme.muted, formatter: formatAxis(axisUnit), hideOverlap: true } }));
  return {
    ...baseOption(theme, unit),
    tooltip: { ...(baseOption(theme, unit).tooltip as Option), axisPointer: { type: "shadow" } },
    legend: legendLayout.option,
    grid: { left: 8, right: 56, top: (multi ? legendLayout.top : 8) + Math.max(0, units.length - 1) * 36, bottom: hidden ? 24 : 8, containLabel: true },
    yAxis: { type: "category", inverse: true, data: categories, axisLine: { lineStyle: { color: theme.grid } }, axisTick: { show: false }, axisLabel: { color: theme.text, width: 180, overflow: "truncate" } },
    xAxis: axes.length === 1 ? axes[0] : axes,
    series: series.map((s, i) => ({
      name: s.name,
      xAxisIndex: units.indexOf(s.unit ?? panel.unit),
      tooltip: { valueFormatter: (value: number) => formatValue(s.unit ?? panel.unit, value) },
      type: "bar",
      data: s.values.map((value, row) => ({ value, selection: selections.get(JSON.stringify(dims.length > 1 ? [categories[row], s.name] : [categories[row]])) })),
      barMaxWidth: 16,
      barGap: "20%",
      itemStyle: { color: colorFor(s.name, i, theme), borderRadius: [0, 3, 3, 0] },
      label: { show: !multi || i === 0, position: "right", color: theme.muted, formatter: ({ value }: { value: number }) => formatValue(s.unit ?? panel.unit, value) },
    })),
    graphic: hiddenSeriesNote(hidden, theme.muted),
  };
}

/** A gauge with its thresholds as coloured bands. */
export type ChartSize = { width: number; height: number; measureText?: (text: string, font: string) => number };

export function gaugeOption(panel: Panel, value: number | null, theme: ChartTheme, unit = panel.unit as string | undefined, size: ChartSize = { width: 270, height: 140 }): Option {
  const min = panel.min ?? 0;
  const max = panel.max ?? 100;
  const sorted = [...(panel.thresholds ?? [])].sort((a, b) => a.value - b.value);
  const bands = gaugeBands(panel, min, max, theme);
  const radius = Math.max(1, Math.min((size.width - 40) / 2, size.height - 30));
  const center = [size.width / 2, (size.height + radius - 24) / 2];
  const bandWidth = Math.min(10, radius * .14);
  const text = formatValue(unit, value);
  const fontSize = Math.max(1, Math.min(32, radius * .32, radius * 1.15 / Math.max(1, text.length * .65)));
  const status = statusFor(value, panel.thresholds, panel.better);
  const cue = status === "bad" ? "◆ Bad" : status === "warn" ? "■ Warn" : status === "ok" ? "● OK" : value === null ? "○ Unknown" : undefined;
  const mute = (hex: string) => `${hex}40`;
  return {
    animation: false,
    textStyle: { fontFamily: theme.font, color: theme.text },
    graphic: [
      { type: "text", left: center[0] - radius, top: center[1] + 6, style: { text: formatValue(unit, min), fill: theme.muted, fontSize: 11, fontFamily: theme.font } },
      { type: "text", right: size.width - center[0] - radius, top: center[1] + 6, style: { text: formatValue(unit, max), fill: theme.muted, fontSize: 11, fontFamily: theme.font } },
      ...(cue ? [{ type: "text", left: "center", top: center[1] - 7, style: { text: cue, fill: theme.text, fontSize: 10, fontFamily: theme.font } }] : []),
    ],
    series: [{
      type: "gauge",
      min,
      max,
      startAngle: 180,
      endAngle: 0,
      radius,
      center,
      axisLine: { lineStyle: { width: bandWidth, color: sorted.length ? bands.map(([end, color]) => [end, mute(color)]) : [[1, theme.grid]] } },
      progress: { show: true, width: bandWidth, roundCap: true, itemStyle: { color: status ? theme.status[status] : seriesSlot(0, theme.dark) } },
      pointer: { show: false },
      axisTick: { show: false },
      splitLine: { show: false },
      axisLabel: { show: false },
      anchor: { show: false },
      detail: { valueAnimation: true, offsetCenter: [0, -radius * .37], width: radius * 1.15, height: fontSize * 1.3, overflow: "truncate", color: theme.text, fontSize, fontWeight: 600, fontFamily: theme.font, formatter: () => text },
      data: [{ value: value ?? min }],
    }],
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
