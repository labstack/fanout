import { chartTheme, seriesColor, statusHex } from "../chart";
import { fonts } from "../tokens";
import { toCategories, toSeries } from "./frame";
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

const colorFor = (name: string, theme: ChartTheme) => (name === "Other" ? theme.muted : seriesColor(name, theme.dark));

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
export function timeseriesOption(panel: Panel, result: PanelResult, theme: ChartTheme): Option {
  const current = result.frame ? toSeries(result.frame) : [];
  const unit = current[0]?.unit ?? panel.unit;
  const previousSeries = result.previous && result.shift_ms !== undefined ? toSeries(result.previous) : [];
  const units = [...new Set([...current, ...previousSeries].map((s) => s.unit ?? panel.unit))];
  if (units.length === 0) units.push(panel.unit);
  const style = panel.options?.style ?? "line";
  const period = result.shift_ms;
  const lines = current.map((s, i) => {
    const color = colorFor(s.name, theme);
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
  const axes = units.map((axisUnit, i) => ({ type: panel.options?.scale === "log" ? "log" : "value", position: i === 0 ? "left" : "right", offset: Math.max(0, i - 1) * 56, max: i === 0 ? thresholdMax(panel) : undefined, axisLine: { show: false }, splitLine: { show: i === 0, lineStyle: { color: theme.grid } }, axisLabel: { color: theme.muted, formatter: formatAxis(axisUnit) } }));
  return {
    ...baseOption(theme, unit),
    legend: { type: "scroll", show: legend, top: 0, left: 0, right: 0, icon: "roundRect", itemWidth: 10, itemHeight: 10, textStyle: { color: theme.text, fontSize: 12 }, data: current.map((s) => s.name) },
    grid: { left: 8, right: 16 + Math.max(0, units.length - 2) * 56, top: legend ? 30 : 12, bottom: 8, containLabel: true },
    xAxis: { type: "time", axisLine: { lineStyle: { color: theme.grid } }, axisTick: { show: false }, splitLine: { show: false }, axisLabel: { color: theme.muted, hideOverlap: true, formatter: formatTimeAxis } },
    yAxis: axes.length === 1 ? axes[0] : axes,
    series: [...lines, ...previous],
  };
}

/** Horizontal bars sorted by the server, value labels at the bar end. */
export function barOption(panel: Panel, frame: Frame, theme: ChartTheme): Option {
  const { categories, series } = toCategories(frame);
  const unit = series[0]?.unit ?? panel.unit;
  const units = [...new Set(series.map((s) => s.unit ?? panel.unit))];
  if (units.length === 0) units.push(panel.unit);
  const multi = series.length > 1;
  const axes = units.map((axisUnit, i) => ({ type: "value", position: i === 0 ? "bottom" : "top", offset: Math.max(0, i - 1) * 36, splitLine: { show: i === 0, lineStyle: { color: theme.grid } }, axisLabel: { color: theme.muted, formatter: formatAxis(axisUnit), hideOverlap: true } }));
  return {
    ...baseOption(theme, unit),
    tooltip: { ...(baseOption(theme, unit).tooltip as Option), axisPointer: { type: "shadow" } },
    legend: { type: "scroll", show: multi, top: 0, left: 0, right: 0, icon: "roundRect", itemWidth: 10, itemHeight: 10, textStyle: { color: theme.text, fontSize: 12 } },
    grid: { left: 8, right: 56, top: (multi ? 30 : 8) + Math.max(0, units.length - 1) * 36, bottom: 8, containLabel: true },
    yAxis: { type: "category", inverse: true, data: categories, axisLine: { lineStyle: { color: theme.grid } }, axisTick: { show: false }, axisLabel: { color: theme.text, width: 180, overflow: "truncate" } },
    xAxis: axes.length === 1 ? axes[0] : axes,
    series: series.map((s, i) => ({
      name: s.name,
      xAxisIndex: units.indexOf(s.unit ?? panel.unit),
      tooltip: { valueFormatter: (value: number) => formatValue(s.unit ?? panel.unit, value) },
      type: "bar",
      data: s.values,
      barMaxWidth: 16,
      barGap: "20%",
      itemStyle: { color: multi ? colorFor(s.name, theme) : seriesColor(panel.id, theme.dark), borderRadius: [0, 3, 3, 0] },
      label: { show: !multi || i === 0, position: "right", color: theme.muted, formatter: ({ value }: { value: number }) => formatValue(s.unit ?? panel.unit, value) },
    })),
  };
}

/** A gauge with its thresholds as coloured bands. */
export function gaugeOption(panel: Panel, value: number | null, theme: ChartTheme, unit = panel.unit as string | undefined): Option {
  const min = panel.min ?? 0;
  const max = panel.max ?? 100;
  const span = max - min || 1;
  const sorted = [...(panel.thresholds ?? [])].sort((a, b) => a.value - b.value);
  const bands = gaugeBands(panel, min, max, theme);
  return {
    series: [{
      type: "gauge",
      min,
      max,
      startAngle: 210,
      endAngle: -30,
      radius: "95%",
      center: ["50%", "60%"],
      axisLine: { lineStyle: { width: 10, color: sorted.length ? bands : [[1, theme.grid]] } },
      progress: { show: sorted.length === 0, width: 10, itemStyle: { color: seriesColor(panel.id, theme.dark) } },
      pointer: { show: sorted.length > 0, length: "55%", width: 4, itemStyle: { color: theme.text } },
      axisTick: { show: false },
      splitLine: { show: false },
      axisLabel: { show: false },
      anchor: { show: false },
      detail: { valueAnimation: true, offsetCenter: [0, "35%"], color: theme.text, fontSize: 22, fontWeight: 600, fontFamily: theme.font, formatter: () => formatValue(unit, value) },
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
