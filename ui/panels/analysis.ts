import { escapeHTML, htmlTooltip, tooltipLines, type TooltipPoint } from "./escape";
import { seriesSlot } from "../chart";
import type { ChartSize, ChartTheme } from "./compile";
import { serviceMapModel } from "./rollups";
import { frameRows } from "./rows";
import { isOtherSeries, seriesGroups, sumPresent, wrappingLegend } from "./series";
import { statusFor } from "./thresholds";
import type { Cell, Panel, PanelResult } from "./types";
import { formatAxis, formatBucket, formatTimeAxis, formatTimestamp, formatValue, niceDurationInterval } from "./units";
import { markStyle } from "./style";

function spanMs(interval?: string): number {
  const match = /^(\d+)(s|m|h|d)$/.exec(interval ?? "");
  return match ? Number(match[1]) * ({ s: 1000, m: 60000, h: 3600000, d: 86400000 }[match[2] as "s" | "m" | "h" | "d"]) : 60000;
}

const bucketLabel = (row: Record<string, Cell>, unit?: string) => formatBucket(typeof row.bucket_lower === "number" ? row.bucket_lower : null, typeof row.bucket_upper === "number" ? row.bucket_upper : null, unit);
// Display labels can round alike; bucket identity and selection remain exact.
const bucketKey = (row: Record<string, Cell>) => `${row.bucket_lower}:${row.bucket_upper}`;
const bucketSelection = (row: Record<string, Cell> | undefined) => typeof row?.bucket_lower === "number"
  ? { lower: row.bucket_lower, upper: typeof row.bucket_upper === "number" ? row.bucket_upper : undefined } : undefined;

/** Surface-tinted low counts, one hue through the bright identity slot. */
function heatRamp(theme: ChartTheme): string[] {
  const high = seriesSlot(0, theme.dark);
  return [.08, .22, .4, .6, .8, 1].map(amount => "#" + [1,3,5].map(i =>
    Math.round(parseInt(theme.surface.slice(i,i+2),16) * (1-amount) + parseInt(high.slice(i,i+2),16) * amount).toString(16).padStart(2,"0")).join(""));
}

/** State runs merge only touching buckets of the same row and status. */
export function mergeStateRuns<T extends { value: (string | number | null | boolean)[]; selection: { from?: string; to?: string } }>(buckets: T[]): T[] {
  const runs: T[] = [];
  for (const bucket of [...buckets].sort((a, b) => Number(a.value[1]) - Number(b.value[1]) || Number(a.value[0]) - Number(b.value[0]))) {
    const last = runs.at(-1);
    if (last && last.value[1] === bucket.value[1] && last.value[4] === bucket.value[4] && last.value[3] === bucket.value[0]) {
      last.value[3] = bucket.value[3];
      last.selection.to = bucket.selection.to;
    } else runs.push({ ...bucket, value: [...bucket.value], selection: { ...bucket.selection } });
  }
  return runs;
}

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
export function analysisOption(panel: Panel, result: PanelResult, theme: ChartTheme, size: ChartSize = { width: 500, height: 248 }): Record<string, unknown> {
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
    tooltip: { trigger: "item", backgroundColor: theme.surface, borderColor: theme.border, textStyle: { color: theme.text,fontFamily:theme.font,fontSize:12 }, renderMode: "html", formatter: htmlTooltip(value => formatValue(panel.unit, value)) },
    legend: { show: false, textStyle: { color: theme.muted } },
    xAxis: { type: "value",axisLine:{show:false},axisTick:{show:false},axisLabel:{color:theme.muted,fontFamily:theme.font,fontSize:11},splitLine:{lineStyle:{color:theme.grid}} }, yAxis: { type: "value",axisLine:{show:false},axisTick:{show:false},axisLabel:{color:theme.muted,fontFamily:theme.font,fontSize:11},splitLine:{lineStyle:{color:theme.grid}} }, series: [] as unknown[],
  };

  if (panel.viz === "scatter") {
    const item = dimensions[0];
    const colour = dimensions[1];
    const groups = seriesGroups([...new Set(rows.map(row => colour ? String(row[colour] ?? "") : "Items"))].map(name => ({ name })), panel);
    const legendLayout = wrappingLegend(groups.map(g => g.name), size.width, groups.length > 1 && panel.options?.legend !== "hidden", theme.text, theme.font, size.measureText);
    const xScale = panel.options?.x_scale ?? panel.options?.scale;
    const yScale = panel.options?.y_scale ?? panel.options?.scale;
    const xUnit = panel.x_unit ?? measures[0]?.unit;
    const yUnit = panel.unit ?? measures[1]?.unit;
    const axis = (unit: string | undefined, scale: string | undefined, measure: string) => {
      const values = rows.map(row => row[measure]).filter((v): v is number => typeof v === "number" && Number.isFinite(v));
      const min = Math.min(0, ...values), max = Math.max(0, ...values);
      const interval = scale === "log" ? undefined : niceDurationInterval(min, max, unit);
      return { type: scale === "log" ? "log" : "value", ...(interval ? { interval, min: Math.floor(min / interval) * interval, max: Math.max(interval, Math.ceil(max / interval) * interval) } : {}),
        axisLine: { lineStyle: { color: theme.grid } }, axisTick: { show: false }, splitLine: { lineStyle: { color: theme.grid } }, axisLabel: { fontFamily: theme.font, fontSize: 11, color: theme.muted, formatter: formatAxis(unit), hideOverlap: true } };
    };
    return {
      ...base,
      tooltip: { ...base.tooltip, formatter: (params: TooltipPoint | TooltipPoint[]) => {
        const points = Array.isArray(params) ? params : [params];
        return points.map(point => {
          const value = Array.isArray(point.value) ? point.value : [];
          return tooltipLines([`${point.seriesName ?? ""} · ${point.name ?? ""}`, `${measures[0]?.name ?? "x"}: ${formatValue(xUnit, Number(value[0]))}`, `${measures[1]?.name ?? "y"}: ${formatValue(yUnit, Number(value[1]))}`]);
        }).join("<br/>");
      } },
      legend: legendLayout.option,
      grid: { ...base.grid, top: legendLayout.option.show ? legendLayout.top : 8, left: 8, right: 16, bottom: 8 },
      xAxis: axis(xUnit, xScale, measures[0]?.name),
      yAxis: axis(yUnit, yScale, measures[1]?.name),
      series: groups.map(({ name, items }, index) => ({
        type: "scatter", name, itemStyle: markStyle(isOtherSeries(name) ? theme.muted : seriesSlot(index, theme.dark),theme),
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
    const legendLayout = wrappingLegend(names.map(g => g.name), size.width, names.length > 1 && panel.options?.legend !== "hidden", theme.text, theme.font, size.measureText);
    return {
      ...base,
      legend: legendLayout.option,
      grid: { ...base.grid, top: legendLayout.top },
      xAxis: { ...base.xAxis, type: "category", data: labels, splitLine:{show:false} },
      yAxis: { ...base.yAxis, type: "value", name: "count",nameTextStyle:{color:theme.muted,fontFamily:theme.font,fontSize:11} },
      series: names.map(({ name, items }, index) => ({
        type: "bar", name, itemStyle: markStyle(isOtherSeries(name) ? theme.muted : seriesSlot(index, theme.dark),theme),
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
    // The host lays out this package-free graph model with Dagre.
    return { model: serviceMapModel(frame, result) };
  }

  if (panel.viz === "heatmap" || panel.viz === "state_timeline") {
    const heat = panel.viz === "heatmap";
    const item = dimensions[0];
    const category = (row: Record<string, Cell>) => heat ? bucketKey(row) : String(row[item] ?? "");
    const names = [...new Set((heat ? [...rows].sort(compareBuckets) : rows).map(category))];
    const labels = new Map(rows.map(row => [bucketKey(row), bucketLabel(row, bucketUnit)]));
    const interval = spanMs(result.interval);
    const measure = measures[0]?.name ?? "count";
    const buckets = rows.filter(row => typeof row.time === "number").map(row => {
      const value = row[measure];
      const status = typeof value === "number" ? statusFor(value, panel.thresholds, panel.better ?? result.better) ?? "ok" : null;
      return {
        value: [row.time, names.indexOf(category(row)), value, Number(row.time) + interval, heat || status === null ? 0 : status === "bad" ? 3 : status === "warn" ? 2 : 1],
        selection: { ...(heat ? { time: row.time } : {}), dimensions: heat ? {} : { [panel.query?.by?.[0] ?? item]: String(row[item] ?? "") }, bucket: heat ? bucketSelection(row) : undefined,
          ...(heat ? {} : { from: new Date(Number(row.time)).toISOString(), to: new Date(Number(row.time) + interval).toISOString() }) },
        itemStyle: heat ? undefined : { color: status === null ? `${theme.text}26` : theme.status[status] },
      };
    });
    const data = heat ? buckets : mergeStateRuns(buckets).map(run => ({ ...run, tooltip: { formatter: () => `${escapeHTML(names[Number(run.value[1])])} · ${["Unknown", "OK", "Warn", "Bad"][Number(run.value[4])]}\n${formatTimestamp(Number(run.value[0]))} – ${formatTimestamp(Number(run.value[3]))}` } }));
    const counts = rows.map(row => Number(row[measure])).filter(n => Number.isFinite(n) && n > 0).sort((a, b) => a - b);
    const maxCount = Math.max(1, counts[Math.ceil(counts.length * .99) - 1] ?? 1);
    return {
      ...base,
      grid: { ...base.grid, left: 8, right: 8, top: 8, bottom: heat ? 32 : 8 },
      tooltip: { ...base.tooltip, trigger: "axis", formatter: (params: TooltipPoint | TooltipPoint[]) => {
        const points = Array.isArray(params) ? params : [params];
        return points.map(point => {
          const v = Array.isArray(point.value) ? point.value : [];
          return tooltipLines([`${point.seriesName ?? ""} · ${point.name ?? ""}`, heat ? labels.get(names[Number(v[1])]) ?? "" : names[Number(v[1])] ?? "",
            `${formatTimestamp(Number(v[0]))} – ${formatTimestamp(Number(v[3]))}`, `${measure}: ${formatValue(heat ? "count" : panel.unit ?? measures[0]?.unit, typeof v[2] === "number" ? v[2] : null)}`]);
        }).join("<br/>");
      } },
      axisPointer: { link: [{ xAxisIndex: "all" }] },
      xAxis: { type: "time", axisPointer: { show: true }, axisLine: { lineStyle: { color: theme.grid } }, axisTick: { show: false }, splitLine:{show:false}, axisLabel: { fontFamily: theme.font, fontSize: 11, color: theme.muted, formatter: formatTimeAxis, hideOverlap: true } },
      yAxis: { type: "category", data: heat ? names.map(name => labels.get(name)) : names, axisLine: { show: false }, axisTick: { show: false }, splitLine:{show:false}, axisLabel: { fontFamily: theme.font, fontSize: 11, color: theme.muted, width: 140, overflow: "truncate" } },
      visualMap: heat ? {
        type: "continuous", show: true, orient: "horizontal", right: 16, bottom: 0, padding: 0,
        itemWidth: 8, itemHeight: 96, text: [`${formatValue("count", maxCount)}+`, "0"], textStyle: { color: theme.muted,fontFamily:theme.font,fontSize:11 },
        min: 0, max: maxCount, dimension: 2, inRange: { color: heatRamp(theme) },
      } : undefined,
      series: [{
        type: "custom", name: panel.title, encode: { x: [0, 3], y: 1, tooltip: 2 }, data,
        renderItem: (_params: unknown, api: { value: (index: number) => number; coord: (value: number[]) => number[]; size: (value: number[]) => number[]; style: () => Record<string, unknown> }) => {
          const left = api.coord([api.value(0), api.value(1)]);
          const right = api.coord([api.value(3), api.value(1)]);
          const height = Math.abs(api.size([0, 1])[1]) * (heat ? 1 : .65);
          // The stroke is centred on the inset shape: outer bounds still leave
          // exactly one surface pixel between adjacent cells. Zero is absence.
          const outlined = heat && api.value(2) > 0;
          return { type: "rect", shape: { x: left[0] + (outlined ? 1 : heat ? .5 : 0), y: left[1] - height / 2 + (outlined ? 1 : heat ? .5 : 0), width: Math.max(0, right[0] - left[0] - (outlined ? 2 : heat ? 1 : 0)), height: Math.max(0, height - (outlined ? 2 : heat ? 1 : 0)), r: heat ? 0 : Math.min(3, height / 4) }, style: { ...api.style(), stroke: outlined ? seriesSlot(0,theme.dark) : undefined, lineWidth: outlined ? 1 : 0 } };
        },
      }],
    };
  }
  return base;
}
