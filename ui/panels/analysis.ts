import { seriesSlot, healthSymbol, healthBorderType } from "../chart";
import type { ChartTheme } from "./compile";
import { serviceMapOption } from "./rollups";
import { frameRows } from "./rows";
import { seriesGroups, sumPresent } from "./series";
import { statusFor } from "./thresholds";
import type { Cell, Panel, PanelResult } from "./types";
import { formatAxis, formatBucket, formatTimeAxis, formatValue } from "./units";

function spanMs(interval?: string): number {
  const match = /^(\d+)(s|m|h|d)$/.exec(interval ?? "");
  return match ? Number(match[1]) * ({ s: 1000, m: 60000, h: 3600000, d: 86400000 }[match[2] as "s" | "m" | "h" | "d"]) : 60000;
}

const bucketLabel = (row: Record<string, Cell>, unit?: string) => formatBucket(typeof row.bucket_lower === "number" ? row.bucket_lower : null, typeof row.bucket_upper === "number" ? row.bucket_upper : null, unit);
// Display labels can round alike; bucket identity and selection remain exact.
const bucketKey = (row: Record<string, Cell>) => `${row.bucket_lower}:${row.bucket_upper}`;
const bucketSelection = (row: Record<string, Cell> | undefined) => typeof row?.bucket_lower === "number"
  ? { lower: row.bucket_lower, upper: typeof row.bucket_upper === "number" ? row.bucket_upper : undefined } : undefined;

/** Numeric order for both distribution axes; open lower bounds sort first. */
function compareBuckets(a: Record<string, Cell>, b: Record<string, Cell>): number {
  const lower = (a.bucket_lower === null ? -Infinity : Number(a.bucket_lower)) - (b.bucket_lower === null ? -Infinity : Number(b.bucket_lower));
  return lower || (a.bucket_upper === null ? Infinity : Number(a.bucket_upper)) - (b.bucket_upper === null ? Infinity : Number(b.bucket_upper));
}

export function analysisSummary(panel: Panel, result: PanelResult): string {
  const columns = (result.frame?.columns ?? []).map(column => column.name + (column.unit ? ` (${column.unit})` : "")).join(", ");
  return `${panel.title}: ${result.frame?.rows ?? 0} rows; ${columns}${result.frame?.truncated ? ", limited data" : ""}`;
}

/** Compile server frames without importing ECharts or browser dependencies. */
export function analysisOption(panel: Panel, result: PanelResult, theme: ChartTheme): Record<string, unknown> {
  const frame = result.frame ?? { columns: [], values: [], rows: 0 };
  const rows = frameRows(frame);
  const dimensions = frame.columns.filter(column => column.role === "dimension").map(column => column.name);
  const measures = frame.columns.filter(column => column.role === "measure");
  const bucketUnit = panel.unit ?? frame.columns.find(column => column.name === "bucket_lower")?.unit;
  const base = {
    animation: false,
    aria: { enabled: true, description: analysisSummary(panel, result) },
    textStyle: { fontFamily: theme.font, color: theme.text },
    grid: { left: 70, right: 24, top: 30, bottom: 40, containLabel: true },
    tooltip: { trigger: "item", backgroundColor: theme.surface, borderColor: theme.border, textStyle: { color: theme.text }, renderMode: "richText" },
    legend: { show: false, textStyle: { color: theme.muted } },
    xAxis: { type: "value" }, yAxis: { type: "value" }, series: [] as unknown[],
  };

  if (panel.viz === "scatter") {
    const item = dimensions[0];
    const colour = dimensions[1];
    const groups = seriesGroups([...new Set(rows.map(row => colour ? String(row[colour] ?? "") : "Items"))].map(name => ({ name })), panel);
    const xScale = panel.options?.x_scale ?? panel.options?.scale;
    const yScale = panel.options?.y_scale ?? panel.options?.scale;
    const xUnit = panel.x_unit ?? measures[0]?.unit;
    const yUnit = panel.unit ?? measures[1]?.unit;
    return {
      ...base,
      legend: { ...base.legend, show: groups.length > 1 && panel.options?.legend !== "hidden" },
      xAxis: { type: xScale === "log" ? "log" : "value", name: xUnit, axisLabel: { formatter: formatAxis(xUnit) } },
      yAxis: { type: yScale === "log" ? "log" : "value", name: yUnit, axisLabel: { formatter: formatAxis(yUnit) } },
      series: groups.map(({ name, items }, index) => ({
        type: "scatter", name, itemStyle: { color: name === "Other" ? theme.muted : seriesSlot(index, theme.dark) },
        data: rows.filter(row => !colour || items.some(item => item.name === String(row[colour] ?? "")))
          .filter(row => typeof row[measures[0]?.name] === "number" && typeof row[measures[1]?.name] === "number")
          .filter(row => (xScale !== "log" || Number(row[measures[0].name]) > 0) && (yScale !== "log" || Number(row[measures[1].name]) > 0))
          .map(row => ({
            name: String(row[item] ?? ""), value: [row[measures[0].name], row[measures[1].name]],
            selection: { dimensions: Object.fromEntries((panel.query?.by ?? []).map((key, index) => [key, String(row[dimensions[index]] ?? "")])) },
          })),
      })),
    };
  }

  if (panel.viz === "histogram") {
    const split = dimensions.find(name => name !== "bucket_lower" && name !== "bucket_upper");
    const buckets = [...new Map([...rows].sort(compareBuckets).map(row => [bucketKey(row), row])).values()];
    const labels = buckets.map(row => bucketLabel(row, bucketUnit));
    const names = seriesGroups([...new Set(rows.map(row => split ? String(row[split] ?? "") : "Count"))].map(name => ({ name })), panel);
    return {
      ...base,
      legend: { ...base.legend, show: names.length > 1 && panel.options?.legend !== "hidden" },
      xAxis: { type: "category", data: labels },
      yAxis: { type: "value", name: "count" },
      series: names.map(({ name, items }, index) => ({
        type: "bar", name, itemStyle: { color: name === "Other" ? theme.muted : seriesSlot(index, theme.dark) },
        data: buckets.map(bucket => {
          const matches = rows.filter(row => (!split || items.some(item => item.name === String(row[split] ?? ""))) && bucketKey(row) === bucketKey(bucket));
          return { value: sumPresent(matches.map(row => typeof row.count === "number" ? row.count : null)) ?? 0, selection: {
            dimensions: split ? { [panel.query?.by?.[0] ?? split]: name } : {}, bucket: bucketSelection(matches[0]),
          } };
        }),
      })),
    };
  }

  if (panel.viz === "service_map") {
    // Task 6 owns the topology compiler; use it for both entry points.
    return { ...base, ...serviceMapOption(frame, theme), xAxis: undefined, yAxis: undefined, legend: undefined };
  }

  if (panel.viz === "heatmap" || panel.viz === "state_timeline") {
    const heat = panel.viz === "heatmap";
    const item = dimensions[0];
    const category = (row: Record<string, Cell>) => heat ? bucketKey(row) : String(row[item] ?? "");
    const names = [...new Set((heat ? [...rows].sort(compareBuckets) : rows).map(category))];
    const labels = new Map(rows.map(row => [bucketKey(row), bucketLabel(row, bucketUnit)]));
    const interval = spanMs(result.interval);
    const measure = measures[0]?.name ?? "count";
    const data = rows.filter(row => typeof row.time === "number").map(row => {
      const value = row[measure];
      const status = typeof value === "number" ? statusFor(value, panel.thresholds, panel.better ?? result.better) ?? "ok" : null;
      return {
        value: [row.time, names.indexOf(category(row)), value, Number(row.time) + interval, heat || status === null ? 0 : status === "bad" ? 3 : status === "warn" ? 2 : 1],
        selection: { time: row.time, dimensions: heat ? {} : { [panel.query?.by?.[0] ?? item]: String(row[item] ?? "") }, bucket: heat ? bucketSelection(row) : undefined },
        itemStyle: heat ? undefined : { color: status === null ? theme.muted : theme.status[status] },
      };
    });
    const counts = rows.map(row => Number(row[measure])).filter(n => Number.isFinite(n) && n > 0).sort((a, b) => a - b);
    const maxCount = Math.max(1, counts[Math.ceil(counts.length * .99) - 1] ?? 1);
    return {
      ...base,
      grid: { ...base.grid, bottom: heat ? 60 : base.grid.bottom },
      tooltip: { ...base.tooltip, trigger: "axis" },
      axisPointer: { link: [{ xAxisIndex: "all" }] },
      xAxis: { type: "time", axisPointer: { show: true }, axisLabel: { formatter: formatTimeAxis, hideOverlap: true } },
      yAxis: { type: "category", data: heat ? names.map(name => labels.get(name)) : names },
      visualMap: heat ? {
        type: "continuous", show: true, orient: "horizontal", left: "center", bottom: 0,
        itemWidth: 8, itemHeight: 96, text: [`${formatValue("count", maxCount)}+`, "0"], textStyle: { color: theme.muted },
        min: 0, max: maxCount, dimension: 2, inRange: { color: [theme.surface, seriesSlot(0, theme.dark)] },
      } : undefined,
      series: [{
        type: "custom", name: panel.title, encode: { x: [0, 3], y: 1, tooltip: 2 }, data,
        renderItem: (_params: unknown, api: { value: (index: number) => number; coord: (value: number[]) => number[]; size: (value: number[]) => number[]; style: () => Record<string, unknown> }) => {
          const left = api.coord([api.value(0), api.value(1)]);
          const right = api.coord([api.value(3), api.value(1)]);
          const height = Math.abs(api.size([0, 1])[1]) * .85;
          const cell = { type: "rect", shape: { x: left[0], y: left[1] - height / 2, width: Math.max(1, right[0] - left[0]), height }, style: api.style() };
          if (heat) return cell;
          const health = ["unknown", "healthy", "degraded", "unhealthy"][api.value(4)] ?? "unknown";
          const symbol = healthSymbol(health);
          const x = left[0] + Math.min(8, Math.max(1, (right[0] - left[0]) / 2)), y = left[1], r = Math.min(4, height / 3);
          const shape = symbol === "diamond" ? { type: "polygon", shape: { points: [[x, y - r], [x + r, y], [x, y + r], [x - r, y]] } }
            : symbol === "roundRect" ? { type: "rect", shape: { x: x - r, y: y - r, width: 2 * r, height: 2 * r, r: 2 } }
              : { type: "circle", shape: { cx: x, cy: y, r } };
          return { type: "group", children: [cell, { ...shape, style: { fill: health === "unknown" ? theme.surface : theme.text, stroke: theme.text, lineDash: healthBorderType(health) === "dashed" ? [2, 2] : undefined } }] };
        },
      }],
    };
  }
  return base;
}
