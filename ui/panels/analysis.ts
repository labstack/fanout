import { heatRamp, heatStep } from "./heat-scale";
import { escapeHTML, htmlTooltip, tooltipLines, type TooltipPoint } from "./escape";
import { seriesSlot } from "../chart";
import type { ChartSize, ChartTheme } from "./compile";
import { serviceMapModel } from "./rollups";
import { frameRows } from "./rows";
import { isOtherSeries, seriesGroups, sumPresent, wrappingLegend } from "./series";
import { statusFor } from "./thresholds";
import type { Cell, Panel, PanelResult } from "./types";
import { formatAxis, formatBucket, formatTimeAxis, formatTimestamp, formatValue, valueAxis, valueAxisTicks } from "./units";
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

const heatCountLabel = (count: number) => count >= 1000 && count < 1e6 && count % 1000 === 0 ? `${count / 1000}k` : formatValue("count", count);

/** State runs merge only touching buckets of the same row and status. */
function mergeStateRuns<T extends { value: (string | number | null | boolean)[]; selection: { from?: string; to?: string } }>(buckets: T[]): T[] {
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
    xAxis: { type: "value",axisLine:{show:false},axisTick:{show:false},axisLabel:{color:theme.muted,fontFamily:theme.font,fontSize:11},splitLine:{lineStyle:{color:theme.grid}} }, yAxis: { type: "value",splitNumber:valueAxisTicks(size.height-92),axisLine:{show:false},axisTick:{show:false},axisLabel:{color:theme.muted,fontFamily:theme.font,fontSize:11},splitLine:{lineStyle:{color:theme.grid}} }, series: [] as unknown[],
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
    const axis = (unit: string | undefined, scale: string | undefined, measure: string, pixels: number) => {
      const values = rows.map(row => row[measure]).filter((v): v is number => typeof v === "number" && Number.isFinite(v));
      const min = Math.min(0, ...values), max = Math.max(0, ...values);
      return { type: scale === "log" ? "log" : "value", ...(scale === "log" ? {splitNumber:valueAxisTicks(pixels)} : valueAxis(min, max, unit, pixels)),
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
      grid: { ...base.grid, top: legendLayout.option.show ? legendLayout.top + 20 : 28, left: 8, right: 16, bottom: 32 },
      xAxis: { ...axis(xUnit, xScale, measures[0]?.name, size.width - 80), name: measures[0]?.name ?? "x", nameLocation: "middle", nameGap: 28, nameTextStyle: { color: theme.muted, fontFamily: theme.font, fontSize: 11, align: "right", verticalAlign: "top" } },
      yAxis: { ...axis(yUnit, yScale, measures[1]?.name, size.height-legendLayout.top-74), name: measures[1]?.name ?? "y", nameLocation: "end", nameGap: 8, nameRotate: 0, nameTextStyle: { color: theme.muted, fontFamily: theme.font, fontSize: 11, align: "right", verticalAlign: "bottom" } },
      series: groups.map(({ name, items }, index) => ({
        type: "scatter", name, interactive: true, keyboard_unit: yUnit, keyboard_x_unit: xUnit, itemStyle: markStyle(isOtherSeries(name) ? theme.muted : seriesSlot(index, theme.dark),theme),
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
    const series = names.map(({ name, items }, index) => ({
        type: "bar", name, interactive: true, keyboard_unit: "count", itemStyle: markStyle(isOtherSeries(name) ? theme.muted : seriesSlot(index, theme.dark),theme),
        data: buckets.map(bucket => {
          const matches = rows.filter(row => (!split || items.some(item => item.name === String(row[split] ?? ""))) && bucketKey(row) === bucketKey(bucket));
          return { value: sumPresent(matches.map(row => typeof row.count === "number" ? row.count : null)) ?? 0, selection: {
            dimensions: split ? { [panel.query?.by?.[0] ?? split]: name } : {}, bucket: bucketSelection(matches[0]),
          } };
        }),
      }));
    return {
      ...base,
      legend: legendLayout.option,
      grid: { ...base.grid, left:16,right:8,top:legendLayout.option.show ? legendLayout.top + 20 : 28,bottom:8 },
      xAxis: { ...base.xAxis, type: "category", data: labels, splitLine:{show:false} },
      yAxis: { ...base.yAxis, type: "value", name:"count",nameLocation:"end",nameGap:8,nameRotate:0,...valueAxis(0, Math.max(0, ...series.flatMap(s => s.data.map(d => d.value))), "count", size.height-(legendLayout.option.show ? legendLayout.top + 20 : 28)-30),axisLabel:{...base.yAxis.axisLabel,hideOverlap:true},nameTextStyle:{color:theme.muted,fontFamily:theme.font,fontSize:11,align:"right",verticalAlign:"bottom"} },
      series,
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
    const allNames = [...new Set((heat ? [...rows].sort(compareBuckets) : rows).map(category))];
    const names = heat ? allNames : allNames.slice(0,Math.max(1,Math.floor((size.height-54)/16)));
    const hiddenRows = allNames.slice(names.length);
    const labels = new Map(rows.map(row => [bucketKey(row), bucketLabel(row, bucketUnit)]));
    const interval = spanMs(result.interval);
    const measure = measures[0]?.name ?? "count";
    const buckets = rows.filter(row => typeof row.time === "number" && names.includes(category(row))).map(row => {
      const value = row[measure];
      const status = typeof value === "number" ? statusFor(value, panel.thresholds, panel.better ?? result.better) ?? "ok" : null;
      return {
        value: [row.time, names.indexOf(category(row)), value, Number(row.time) + interval, heat || status === null ? 0 : status === "bad" ? 3 : status === "warn" ? 2 : 1],
        selection: { ...(heat ? { time: row.time } : {}), dimensions: heat ? {} : { [panel.query?.by?.[0] ?? item]: String(row[item] ?? "") }, bucket: heat ? bucketSelection(row) : undefined,
          ...(heat ? {} : { from: new Date(Number(row.time)).toISOString(), to: new Date(Number(row.time) + interval).toISOString() }) },
        itemStyle: heat ? undefined : { color: status === null ? `${theme.text}26` : theme.status[status] },
      };
    });
    const occupied = buckets.filter(bucket => Number.isFinite(Number(bucket.value[2])) && Number(bucket.value[2]) > 0);
    const counts = occupied.map(bucket => Number(bucket.value[2])).sort((a,b)=>a-b);
    // Nearest-rank p99 uses only finite, non-empty cells; outliers saturate.
    const cap = counts[Math.max(0, Math.ceil(counts.length * .99) - 1)] ?? 1, ramp = heatRamp(theme);
    const data = heat ? occupied.map(bucket => ({ ...bucket, value: [...bucket.value, heatStep(Number(bucket.value[2]), cap)] })) : mergeStateRuns(buckets).map(run => ({ ...run, tooltip: { formatter: () => `${escapeHTML(names[Number(run.value[1])])} · ${["Unknown", "OK", "Warn", "Bad"][Number(run.value[4])]}\n${formatTimestamp(Number(run.value[0]))} – ${formatTimestamp(Number(run.value[3]))}` } }));
    const capLabel = heatCountLabel(counts.length ? cap : 0);
    return {
      ...base,
      grid: { ...base.grid, left: 8, right: 8, top: 8, bottom: heat ? 32 : hiddenRows.length ? 24 : 8 },
      tooltip: { ...base.tooltip, trigger: "axis", formatter: (params: TooltipPoint | TooltipPoint[]) => {
        const points = Array.isArray(params) ? params : [params];
        return points.map(point => {
          const v = Array.isArray(point.value) ? point.value : [];
          return tooltipLines([`${point.seriesName ?? ""} · ${point.name ?? ""}`, heat ? labels.get(names[Number(v[1])]) ?? "" : names[Number(v[1])] ?? "",
            `${formatTimestamp(Number(v[0]))} – ${formatTimestamp(Number(v[3]))}`, `${measure}: ${formatValue(heat ? "count" : panel.unit ?? measures[0]?.unit, typeof v[2] === "number" ? v[2] : null)}`]);
        }).join("<br/>");
      } },
      axisPointer: { link: [{ xAxisIndex: "all" }] },
      xAxis: { type: "time", min: result.from_ms, max: result.to_ms, axisPointer: { show: true }, axisLine: { lineStyle: { color: theme.grid } }, axisTick: { show: false }, splitLine:{show:false}, axisLabel: { fontFamily: theme.font, fontSize: 11, color: theme.muted, formatter: formatTimeAxis, hideOverlap: true } },
      yAxis: { type: "category", data: heat ? names.map(name => labels.get(name)) : names, axisLine: { show: false }, axisTick: { show: false }, splitLine:{show:false}, axisLabel: { fontFamily: theme.font, fontSize: 11, color: theme.muted, width: 140, overflow: "truncate", ...(heat ? { interval: (size.height-60)/Math.max(1,names.length)<12 ? 1 : 0, hideOverlap: true } : { interval: 0 }) } },
      visualMap: heat ? {
        type: "continuous", show: true, orient: "horizontal", right: 16, bottom: 0, padding: 0,
        itemWidth: 8, itemHeight: 96, text: [capLabel, counts.length ? "1" : "0"], textGap: 10, textStyle: { color: theme.muted,fontFamily:theme.font,fontSize:11 },
        min: 0, max: 6, dimension: 5, inRange: { color: ramp },
      } : undefined,
      graphic: !heat && hiddenRows.length ? [{type:"text",right:0,bottom:0,style:{text:`+${hiddenRows.length} rows`,fill:theme.muted,fontSize:11,fontFamily:theme.font},tooltip:{formatter:()=>hiddenRows.map(escapeHTML).join("<br/>")}}] : [],
      series: [{
        type: "custom", clip: true, name: panel.title, interactive: true, keyboard_unit: heat ? "count" : panel.unit ?? measures[0]?.unit, encode: { x: [0, 3], y: 1, tooltip: 2 }, data,
        renderItem: (_params: unknown, api: { value: (index: number) => number; coord: (value: number[]) => number[]; size: (value: number[]) => number[]; visual: (key: "color") => string }) => {
          const left = api.coord([api.value(0), api.value(1)]);
          const right = api.coord([api.value(3), api.value(1)]);
          const height = Math.abs(api.size([0, 1])[1]) * (heat ? 1 : .65);
          const gap = heat && Math.abs(right[0]-left[0]) >= 8 ? 1 : 0;
          if (heat && !(api.value(2) > 0)) return undefined;
          return { type: "rect", shape: { x: left[0] + gap/2, y: left[1] - height / 2 + gap/2, width: Math.max(0, right[0] - left[0] - gap), height: Math.max(0, height - gap), r: heat ? 0 : Math.min(3, height / 4) }, style: { fill: api.visual("color"), stroke: undefined, lineWidth: 0, ...(heat ? { fill: ramp[heatStep(api.value(2), cap)] } : {}) } };
        },
      }],
    };
  }
  return base;
}
