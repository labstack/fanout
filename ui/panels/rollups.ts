import { healthBorderType, healthSymbol, healthSymbolScale } from "../chart";
import type { ChartSize, ChartTheme } from "./compile";
import type { Frame } from "./types";
import { formatValue } from "./units";

export const healthGlyph: Record<string, string> = { healthy: "●", degraded: "■", unhealthy: "◆", unknown: "○" };

type MapPoint = { id: string; x: number; y: number; symbolSize: number; priority: number };
type MapBox = { x: number; y: number; width: number; height: number };
const overlaps = (a: MapBox, b: MapBox) => a.x < b.x + b.width + 3 && a.x + a.width + 3 > b.x && a.y < b.y + b.height + 3 && a.y + a.height + 3 > b.y;

/** Fit completed layout coordinates, retaining fixed-size symbols and labels.
 * High-priority labels get the first collision-free slot; all names stay in tooltips. */
export function fitServiceMap<T extends MapPoint>(nodes: T[], size: ChartSize) {
  if (!nodes.length) return [];
  const padding = 12;
  const labelWidth = Math.max(1, Math.min(140, size.width - 2 * padding));
  const radius = Math.max(...nodes.map(n => n.symbolSize / 2));
  const minX = Math.min(...nodes.map(n => n.x)), maxX = Math.max(...nodes.map(n => n.x));
  const minY = Math.min(...nodes.map(n => n.y)), maxY = Math.max(...nodes.map(n => n.y));
  const scale = Math.max(0, Math.min(1, (size.width - 2 * padding - Math.max(labelWidth, 2 * radius)) / Math.max(1, maxX - minX), (size.height - 2 * padding - 2 * radius - 38) / Math.max(1, maxY - minY)));
  const cx = (minX + maxX) / 2, cy = (minY + maxY) / 2;
  const placed = nodes.map(node => {
    const x = size.width / 2 + (node.x - cx) * scale, y = size.height / 2 + (node.y - cy) * scale;
    return { ...node, x, y, nodeBox: { x: x - node.symbolSize / 2, y: y - node.symbolSize / 2, width: node.symbolSize, height: node.symbolSize },
      labelBox: undefined as MapBox | undefined, label: { show: false, position: "bottom", distance: 5, width: Math.min(labelWidth, Math.max(14, Array.from(node.id).length * 11)), height: 14, lineHeight: 14, overflow: "truncate", ellipsis: "…", align: "center", verticalAlign: "top" } };
  });
  const labels: MapBox[] = [];
  for (const node of [...placed].sort((a, b) => b.priority - a.priority || a.id.localeCompare(b.id))) {
    const r = node.symbolSize / 2, width = node.label.width, height = 14;
    const choices = [
      { position: "bottom", align: "center", verticalAlign: "top", box: { x: node.x - width / 2, y: node.y + r + 5, width, height } },
      { position: "top", align: "center", verticalAlign: "bottom", box: { x: node.x - width / 2, y: node.y - r - 5 - height, width, height } },
      { position: "right", align: "left", verticalAlign: "middle", box: { x: node.x + r + 5, y: node.y - height / 2, width, height } },
      { position: "left", align: "right", verticalAlign: "middle", box: { x: node.x - r - 5 - width, y: node.y - height / 2, width, height } },
    ];
    const slot = choices.find(({ box }) => box.x >= padding && box.y >= padding && box.x + box.width <= size.width - padding && box.y + box.height <= size.height - padding && !labels.some(label => overlaps(box, label)) && !placed.some(other => overlaps(box, other.nodeBox)));
    if (!slot) continue;
    node.labelBox = slot.box;
    node.label = { ...node.label, show: true, position: slot.position, align: slot.align, verticalAlign: slot.verticalAlign };
    labels.push(slot.box);
  }
  return placed;
}

/** A deterministic, bounded force pass before fitting; no animation can move
 * nodes outside the measured canvas after the fit. Large graphs use rings. */
function mapLayout(nodes: { id: string; symbolSize: number; spans: number | null; health: string }[], links: { source: string; target: string }[]) {
  const points = nodes.map((n, i) => ({ id: n.id, symbolSize: n.symbolSize, priority: (n.health === "unhealthy" ? 1e12 : n.health === "degraded" ? 1e11 : 0) + (n.spans ?? 0), x: Math.cos(i * 2 * Math.PI / nodes.length) * Math.max(100, nodes.length * 10), y: Math.sin(i * 2 * Math.PI / nodes.length) * Math.max(100, nodes.length * 10) }));
  if (points.length > 100) return points;
  const index = new Map(points.map((p, i) => [p.id, i]));
  for (let pass = 0; pass < 80; pass++) {
    const forces = points.map(() => ({ x: 0, y: 0 }));
    for (let a = 0; a < points.length; a++) for (let b = a + 1; b < points.length; b++) {
      const dx = points[a].x - points[b].x, dy = points[a].y - points[b].y;
      const d = Math.max(1, Math.hypot(dx, dy)), force = 1500 / d;
      forces[a].x += dx / d * force; forces[a].y += dy / d * force;
      forces[b].x -= dx / d * force; forces[b].y -= dy / d * force;
    }
    for (const link of links) {
      const a = index.get(link.source), b = index.get(link.target);
      if (a === undefined || b === undefined || a === b) continue;
      const dx = points[b].x - points[a].x, dy = points[b].y - points[a].y;
      const d = Math.max(1, Math.hypot(dx, dy)), force = (d - 100) * .04;
      forces[a].x += dx / d * force; forces[a].y += dy / d * force;
      forces[b].x -= dx / d * force; forces[b].y -= dy / d * force;
    }
    points.forEach((p, i) => { p.x += Math.max(-10, Math.min(10, forces[i].x)); p.y += Math.max(-10, Math.min(10, forces[i].y)); });
  }
  return points;
}

/** The topology's health encodings, fed
 * by the panel frame. Edge-only endpoints remain visible as ungraded nodes. */
export function serviceMapOption(frame: Frame, theme: ChartTheme, size: ChartSize = { width: 500, height: 220 }) {
  const index = new Map(frame.columns.map((c, i) => [c.name, i]));
  const cell = (name: string, row: number) => frame.values[index.get(name) ?? -1]?.[row];
  const text = (name: string, row: number) => String(cell(name, row) ?? "");
  const num = (name: string, row: number) => { const n = cell(name, row); return typeof n === "number" && Number.isFinite(n) ? n : null; };
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
    selection: { dimensions: { service: node.service } },
    symbol: healthSymbol(node.health), symbolSize: 24 * healthSymbolScale(node.health),
    itemStyle: { color: theme.surface, borderColor: color(node.health), borderWidth: 3, borderType: healthBorderType(node.health) },
  }));
  const links = edges.map((edge) => {
    // Frames carry percent, rather than the observability contract's ratio.
    const failing = edge.error_rate >= 5;
    return { ...edge, source: edge.caller, target: edge.callee, value: edge.calls, lineStyle: { width: failing ? 3 + Math.min(5, edge.error_rate / 10) : 1, color: failing ? theme.status.bad : theme.border, opacity: failing ? 1 : 0.6, curveness: 0.08 } };
  });
  const layout = fitServiceMap(mapLayout(data, links), size);
  return {
    animation: false,
    grid: { left: 0, right: 0, top: 0, bottom: 0 },
    xAxis: { type: "value", min: 0, max: size.width, show: false },
    yAxis: { type: "value", min: 0, max: size.height, inverse: true, show: false },
    tooltip: { backgroundColor: theme.surface, borderColor: theme.border, textStyle: { color: theme.text, fontSize: 11 }, formatter: (params: { dataType?: string; data: Record<string, unknown> }) => {
      const item = params.data;
      if (params.dataType === "edge") return `${escapeHTML(String(item.caller))} → ${escapeHTML(String(item.callee))}<br/>${escapeHTML(String(item.edge_type))} · ${formatValue("count", Number(item.calls))} calls<br/>Average ${formatValue("ms", item.average_ms as number | null)} · ${formatValue("percent", Number(item.error_rate))} errors`;
      return `${escapeHTML(String(item.service))} · ${escapeHTML(String(item.health))}<br/>P95 ${formatValue("ms", item.p95_ms as number | null)} · ${formatValue("count", item.spans as number | null)} spans<br/>${formatValue("percent", item.error_rate as number | null)} errors`;
    } },
    series: [{ type: "graph", coordinateSystem: "cartesian2d", layout: "none", roam: false,
      label: { show: true, position: "bottom", color: theme.text, fontSize: 11 }, edgeSymbol: ["none", "arrow"], edgeSymbolSize: 6,
      data: data.map((node, i) => ({ ...node, x: layout[i].x, y: layout[i].y, value: [layout[i].x, layout[i].y], label: { ...layout[i].label, color: theme.text, fontSize: 11, fontFamily: theme.font } })), links, emphasis: { focus: "adjacency" },
    }],
  };
}

function escapeHTML(value: string): string {
  return value.replace(/[&<>"']/g, (c) => ({ "&": "&amp;", "<": "&lt;", ">": "&gt;", '"': "&quot;", "'": "&#39;" })[c]!);
}
