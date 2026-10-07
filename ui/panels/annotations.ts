import type { ChartSize, ChartTheme } from "./compile";
import type { Panel, PanelResult, VarValue } from "./types";

export type AnnotationBody = { from: string; to: string; services?: string[]; namespace?: string };
export type Deploy = { namespace: string; service: string; version: string; at: string };
export type Anomaly = { namespace: string; service: string; kind: string; from: string; to: string; title: string; severity: string };
export type AnnotationsResponse = { deploys: Deploy[]; anomalies: Anomaly[]; truncated?: boolean };

export function withAnnotations(option: Record<string, unknown>, panel: Panel, result: PanelResult, annotations: AnnotationsResponse, _vars: Record<string, VarValue>, theme: ChartTheme, size: ChartSize = { width: 500, height: 248 }): Record<string, unknown> {
  if (!["timeseries", "heatmap", "state_timeline"].includes(panel.viz)) return option;
  const scope = result.annotation_scope;
  if (result.annotation_error) return option;
  const matches = (a: { service: string; namespace: string }) => !scope || scope.services.some(s => s.service === a.service && (s.namespace === a.namespace || a.namespace === "" && !scope.namespace_scoped));
  const from = result.from_ms ?? -Infinity, to = result.to_ms ?? Infinity;
  const deploys = annotations.deploys.filter(matches).filter(a => Date.parse(a.at) >= from && Date.parse(a.at) < to).map(a => ({
    xAxis: Date.parse(a.at), name: `${a.service} ${a.version}`,
    label: { show: false, formatter: `${a.service} ${a.version}`, color: theme.text, position: "end", distance: 8, rotate: 0, verticalAlign: "bottom", backgroundColor: theme.surface, padding: [3, 5], height: 12, fontSize: 10, overflow: "truncate", ellipsis: "…", width: 120, offset: [0, 0] },
    lineStyle: { type: "dashed", color: theme.muted, width: 1 },
    tooltip: { formatter: () => `${a.service} · ${a.version} · ${a.at}` },
  }));
  const episodes: { service: string; namespace: string; from: number; to: number; bad: boolean; titles: string[]; details: string[] }[] = [];
  for (const a of annotations.anomalies.filter(matches).filter(a => Date.parse(a.to) > from && Date.parse(a.from) < to)
    .sort((a, b) => a.namespace.localeCompare(b.namespace) || a.service.localeCompare(b.service) || Date.parse(a.from) - Date.parse(b.from))) {
    const start = Math.max(from, Date.parse(a.from)), end = Math.min(to, Date.parse(a.to));
    const detail = `${a.service} · ${a.title} · ${a.severity}`;
    const last = episodes.at(-1);
    if (last && last.service === a.service && last.namespace === a.namespace && start <= last.to) {
      last.to = Math.max(last.to, end); last.bad ||= a.severity === "bad";
      if (!last.titles.includes(a.title)) last.titles.push(a.title);
      if (!last.details.includes(detail)) last.details.push(detail);
    } else episodes.push({ service: a.service, namespace: a.namespace, from: start, to: end, bad: a.severity === "bad", titles: [a.title], details: [detail] });
  }
  const anomalies = episodes.map(a => [{
    xAxis: a.from, name: a.titles.join(" · "),
    itemStyle: { color: a.bad ? theme.status.bad : theme.status.warn, opacity: theme.dark ? .12 : .09 },
    label: { show: false }, tooltip: { formatter: () => a.details.join("\n") },
  }, { xAxis: a.to }]);
  const series = (option.series ?? []) as Record<string, unknown>[];
  if (!series.length) return option;
  const grid = (option.grid ?? {}) as Record<string, unknown>;
  const yAxis = (Array.isArray(option.yAxis) ? option.yAxis[0] : option.yAxis) as { axisLabel?: { width?: number; margin?: number } } | undefined;
  // Use a conservative axis-label budget when grid containment moves the plot
  // inward. It prevents labels separated in nominal coordinates from colliding.
  const axisBudget = grid.containLabel ? (yAxis?.axisLabel?.width ?? 64) + (yAxis?.axisLabel?.margin ?? 8) : 0;
  const left = (typeof grid.left === "number" ? grid.left : 8) + axisBudget;
  const right = size.width - (typeof grid.right === "number" ? grid.right : 16);
  const laneWidth = Math.max(1, right - left);
  const width = Math.max(1, Math.min(120, laneWidth / 2 - 14));
  const times = deploys.map(d => d.xAxis);
  const windowFrom = Number.isFinite(from) ? from : Math.min(...times);
  const windowTo = Number.isFinite(to) ? to : Math.max(...times) + 1;
  const clusters: { first: typeof deploys[number]; start: number; end: number; count: number; x: number; details: string[] }[] = [];
  for (const deploy of [...deploys].sort((a, b) => a.xAxis - b.xAxis)) {
    const x = left + (deploy.xAxis - windowFrom) / Math.max(1, windowTo - windowFrom) * laneWidth;
    const start = Math.max(left, Math.min(right - width - 10, x - (width + 10) / 2));
    const end = start + width + 10;
    const last = clusters.at(-1);
    if (last && start < last.end + 4) { last.count++; last.end = Math.max(last.end, end); last.details.push(deploy.tooltip.formatter()); }
    else clusters.push({ first: deploy, start, end, count: 0, x, details: [deploy.tooltip.formatter()] });
  }
  for (const cluster of clusters) {
    const suffix = cluster.count ? ` +${cluster.count}` : "";
    const maxChars = Math.max(1, Math.floor(width / 6) - suffix.length);
    const name = cluster.first.name;
    cluster.first.label = { ...cluster.first.label, show: true, width,
      formatter: `${name.length > maxChars ? `${name.slice(0, Math.max(0, maxChars - 1))}…` : name}${suffix}`,
      offset: [cluster.start + (width + 10) / 2 - cluster.x, 0] };
    cluster.first.tooltip.formatter = () => cluster.details.join("\n");
  }
  return {
    ...option, ...(deploys.length || anomalies.length ? { tooltip: { ...((option.tooltip as Record<string, unknown>) ?? {}), renderMode: "richText" } } : {}),
    // Labels end eight pixels above the plot; the lane follows any legend.
    ...(deploys.length ? {
      grid: { ...grid, top: (typeof grid.top === "number" ? grid.top : 12) + 28 },
    } : {}),
    series: series.map((s, i) => {
      if (i !== 0) return s;
      const markLine = (s.markLine ?? {}) as { data?: unknown[] };
      const markArea = (s.markArea ?? {}) as { data?: unknown[] };
      return { ...s,
        markLine: { ...markLine, silent: false, symbol: ["none", "none"], data: [...(markLine.data ?? []), ...deploys] },
        markArea: { ...markArea, silent: false, data: [...(markArea.data ?? []), ...anomalies] },
      };
    }),
  };
}
