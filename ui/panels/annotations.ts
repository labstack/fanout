import type { ChartTheme } from "./compile";
import type { Panel, PanelResult, VarValue } from "./types";

export type AnnotationBody = { from: string; to: string; services?: string[]; namespace?: string };
export type Deploy = { namespace: string; service: string; version: string; at: string };
export type Anomaly = { namespace: string; service: string; kind: string; from: string; to: string; title: string; severity: string };
export type AnnotationsResponse = { deploys: Deploy[]; anomalies: Anomaly[]; truncated?: boolean };

export function withAnnotations(option: Record<string, unknown>, panel: Panel, result: PanelResult, annotations: AnnotationsResponse, _vars: Record<string, VarValue>, theme: ChartTheme): Record<string, unknown> {
  if (!["timeseries", "heatmap", "state_timeline"].includes(panel.viz)) return option;
  const scope = result.annotation_scope;
  if (result.annotation_error) return option;
  const matches = (a: { service: string; namespace: string }) => !scope || scope.services.some(s => s.service === a.service && (s.namespace === a.namespace || a.namespace === "" && !scope.namespace_scoped));
  const from = result.from_ms ?? -Infinity, to = result.to_ms ?? Infinity;
  const deploys = annotations.deploys.filter(matches).filter(a => Date.parse(a.at) >= from && Date.parse(a.at) < to).map(a => ({
    xAxis: Date.parse(a.at), name: `${a.service} ${a.version}`,
    label: { formatter: `${a.service} ${a.version}`, color: theme.muted, position: "end", distance: -8, rotate: 0, verticalAlign: "top" },
    lineStyle: { type: "dashed", color: theme.muted, width: 1 },
    tooltip: { formatter: () => `${a.service} · ${a.version} · ${a.at}` },
  }));
  const anomalies = annotations.anomalies.filter(matches).filter(a => Date.parse(a.to) > from && Date.parse(a.from) < to).map(a => [{
    xAxis: Math.max(from, Date.parse(a.from)), name: a.title,
    itemStyle: { color: a.severity === "bad" ? theme.status.bad : theme.status.warn, opacity: .08 },
    label: { show: false }, tooltip: { formatter: () => `${a.service} · ${a.title} · ${a.severity}` },
  }, { xAxis: Math.min(to, Date.parse(a.to)) }]);
  const series = (option.series ?? []) as Record<string, unknown>[];
  if (!series.length) return option;
  const grid = (option.grid ?? {}) as Record<string, unknown>;
  return {
    ...option, ...(deploys.length || anomalies.length ? { tooltip: { ...((option.tooltip as Record<string, unknown>) ?? {}), renderMode: "richText" } } : {}),
    // Brush tools occupy the top 22px; deploy labels start inside the plot below it.
    ...(deploys.length ? {
      toolbox: { ...((option.toolbox as Record<string, unknown>) ?? {}), top: 0, right: 8, itemSize: 12, padding: 5 },
      grid: { ...grid, top: Math.max(typeof grid.top === "number" ? grid.top : 0, 48) },
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
