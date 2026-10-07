import { escapeHTML } from "./escape";
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
    label: { show: true, formatter: `deploy ${a.service} ${a.version}`, color: theme.muted, position: "insideEndTop", distance: 8, rotate: 90, textBorderColor: theme.surface, textBorderWidth: 3, fontFamily: theme.font, fontSize: 12, overflow: "truncate", ellipsis: "…", width: Math.max(24, size.height - 60) },
    lineStyle: { type: "dashed", color: theme.muted, width: 1 },
    tooltip: { formatter: () => escapeHTML(`${a.service} · ${a.version} · ${a.at}`) },
  }));
  const episodes: { service: string; namespace: string; from: number; to: number; bad: boolean; titles: string[]; details: string[] }[] = [];
  for (const a of annotations.anomalies.filter(matches).filter(a => Date.parse(a.to) > from && Date.parse(a.from) < to)
    .sort((a, b) => a.namespace.localeCompare(b.namespace) || a.service.localeCompare(b.service) || Date.parse(a.from) - Date.parse(b.from))) {
    const start = Math.max(from, Date.parse(a.from)), end = Math.min(to, Date.parse(a.to));
    const detail = escapeHTML(`${a.service} · ${a.title} · ${a.severity} · ${a.from} – ${a.to}`);
    const last = episodes.at(-1);
    if (last && last.service === a.service && last.namespace === a.namespace && start <= last.to) {
      last.to = Math.max(last.to, end); last.bad ||= a.severity === "bad";
      if (!last.titles.includes(a.title)) last.titles.push(a.title);
      if (!last.details.includes(detail)) last.details.push(detail);
    } else episodes.push({ service: a.service, namespace: a.namespace, from: start, to: end, bad: a.severity === "bad", titles: [a.title], details: [detail] });
  }
  const anomalies = episodes.map(a => [{
    xAxis: a.from, name: a.titles.join(" · "),
    itemStyle: { color: theme.status.warn, opacity: theme.dark ? .12 : .09 },
    label: { show: true, formatter: "anomaly", position: "insideTopRight", color: theme.muted, fontFamily: theme.font, fontSize: 12 }, tooltip: { formatter: () => a.details.join("\n") },
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
  const times = deploys.map(d => d.xAxis);
  const windowFrom = Number.isFinite(from) ? from : Math.min(...times);
  const windowTo = Number.isFinite(to) ? to : Math.max(...times) + 1;
  const clusters: { first: typeof deploys[number]; count: number; x: number; details: string[] }[] = [];
  for (const deploy of [...deploys].sort((a, b) => a.xAxis - b.xAxis)) {
    const x = left + (deploy.xAxis - windowFrom) / Math.max(1, windowTo - windowFrom) * laneWidth;
    const last = clusters.at(-1);
    if (last && x - last.x <= 12) { last.count++; last.details.push(deploy.tooltip.formatter()); }
    else clusters.push({ first: deploy, count: 1, x, details: [deploy.tooltip.formatter()] });
  }
  for (const cluster of clusters) {
    if (cluster.count > 1) cluster.first.label.formatter = `${cluster.count} deploys`;
    cluster.first.tooltip.formatter = () => cluster.details.join("<br/>");
  }
  return {
    ...option, ...(deploys.length || anomalies.length ? { tooltip: { ...((option.tooltip as Record<string, unknown>) ?? {}), renderMode: "html" } } : {}),
    series: series.map((s, i) => {
      if (i !== 0) return s;
      const markLine = (s.markLine ?? {}) as { data?: unknown[] };
      const markArea = (s.markArea ?? {}) as { data?: unknown[] };
      return { ...s,
        markLine: { ...markLine, silent: false, symbol: ["none", "none"], data: [...(markLine.data ?? []), ...clusters.map(c => c.first)] },
        markArea: { ...markArea, silent: false, data: [...(markArea.data ?? []), ...anomalies] },
      };
    }),
  };
}
