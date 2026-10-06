import { healthBorderType, healthSymbol, healthSymbolScale } from "../chart";
import type { ChartTheme } from "./compile";
import type { Frame } from "./types";
import { formatValue } from "./units";

export const healthGlyph: Record<string, string> = { healthy: "●", degraded: "■", unhealthy: "◆", unknown: "○" };

/** The historical topology's stable circular layout and health encodings, fed
 * by the panel frame. Edge-only endpoints remain visible as ungraded nodes. */
export function serviceMapOption(frame: Frame, theme: ChartTheme) {
  const index = new Map(frame.columns.map((c, i) => [c.name, i]));
  const cell = (name: string, row: number) => frame.values[index.get(name) ?? -1]?.[row];
  const text = (name: string, row: number) => String(cell(name, row) ?? "");
  const num = (name: string, row: number) => { const n = cell(name, row); return typeof n === "number" ? n : null; };
  const nodes = new Map<string, { service: string; health: string; spans: number | null; p95_ms: number | null; error_rate: number | null }>();
  const edges: { caller: string; callee: string; edge_type: string; calls: number; average_ms: number | null; error_rate: number }[] = [];
  for (let r = 0; r < frame.rows; r++) {
    if (text("kind", r) === "node") {
      const service = text("service", r);
      nodes.set(service, { service, health: text("health", r), spans: num("spans", r), p95_ms: num("p95_ms", r), error_rate: num("error_rate", r) });
    } else if (text("kind", r) === "edge") {
      edges.push({ caller: text("caller", r), callee: text("callee", r), edge_type: text("edge_type", r), calls: num("calls", r) ?? 0, average_ms: num("average_ms", r), error_rate: num("error_rate", r) ?? 0 });
    }
  }
  for (const edge of edges) for (const service of [edge.caller, edge.callee]) {
    if (!nodes.has(service)) nodes.set(service, { service, health: "unknown", spans: null, p95_ms: null, error_rate: null });
  }
  const color = (health: string) => health === "healthy" ? theme.status.ok : health === "degraded" ? theme.status.warn : health === "unhealthy" ? theme.status.bad : theme.muted;
  const data = [...nodes.values()].sort((a, b) => a.service.localeCompare(b.service)).map((node) => ({
    ...node, id: node.service, name: node.service, value: node.spans,
    symbol: healthSymbol(node.health), symbolSize: healthSymbolScale(node.health) * Math.min(34, 18 + Math.log10(Math.max(node.spans ?? 1, 1)) * 4),
    itemStyle: { color: theme.surface, borderColor: color(node.health), borderWidth: 3, borderType: healthBorderType(node.health) },
  }));
  const links = edges.map((edge) => {
    // Frames carry percent, rather than the observability contract's ratio.
    const failing = edge.error_rate >= 5;
    return { ...edge, source: edge.caller, target: edge.callee, value: edge.calls, lineStyle: { width: failing ? 3.5 : Math.min(3, 1 + Math.log10(Math.max(edge.calls, 1))), color: failing ? theme.status.bad : theme.muted, opacity: failing ? 0.95 : 0.35, curveness: 0.08 } };
  });
  return {
    tooltip: { backgroundColor: theme.surface, borderColor: theme.border, textStyle: { color: theme.text, fontSize: 11 }, formatter: (params: { dataType?: string; data: Record<string, unknown> }) => {
      const item = params.data;
      if (params.dataType === "edge") return `${escapeHTML(String(item.caller))} → ${escapeHTML(String(item.callee))}<br/>${escapeHTML(String(item.edge_type))} · ${formatValue("count", Number(item.calls))} calls<br/>Average ${formatValue("ms", item.average_ms as number | null)} · ${formatValue("percent", Number(item.error_rate))} errors`;
      return `${escapeHTML(String(item.service))} · ${escapeHTML(String(item.health))}<br/>P95 ${formatValue("ms", item.p95_ms as number | null)} · ${formatValue("count", item.spans as number | null)} spans<br/>${formatValue("percent", item.error_rate as number | null)} errors`;
    } },
    series: [{ type: "graph", layout: "circular", circular: { rotateLabel: false }, roam: false, draggable: false,
      label: { show: true, position: "bottom", color: theme.text, fontSize: 11 }, edgeSymbol: ["none", "arrow"], edgeSymbolSize: 6,
      data, links, emphasis: { focus: "adjacency" },
    }],
  };
}

function escapeHTML(value: string): string {
  return value.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);
}
