import { useMemo, useState } from "react";
import {
  forceCenter,
  forceLink,
  forceManyBody,
  forceSimulation,
  type Simulation,
  type SimulationLinkDatum,
  type SimulationNodeDatum,
} from "d3-force";
import { chartColors, statusColor, tooltipBox } from "@/lib/chart-theme";
import { fmt } from "@/lib/utils";
import type { TopologyData } from "@/lib/types";

interface SimNode extends SimulationNodeDatum {
  id: string;
  status: string;
  rpm: number;
  p95: number;
  errors: number;
}

interface SimEdge extends SimulationLinkDatum<SimNode> {
  source: string | SimNode;
  target: string | SimNode;
  rpm: number;
  errorRate: number;
}

type HoverTarget =
  | { kind: "node"; node: SimNode; x: number; y: number }
  | { kind: "edge"; edge: SimEdge; x: number; y: number };

// Nominal layout space. The result is fit to a bounding-box viewBox below, so
// the SVG scales responsively to its container without re-running the layout.
const LAYOUT_W = 800;

function edgeColor(rate: number, c: ReturnType<typeof chartColors>): string {
  if (rate > 3) return c.destructive;
  if (rate > 1) return c.warning;
  return c.border;
}

function edgeOpacity(rate: number): number {
  if (rate > 3) return 0.8;
  if (rate > 1) return 0.7;
  return 0.6;
}

export function TopologyBlock({
  data,
  onAction,
}: {
  data: TopologyData;
  onAction?: (prompt: string) => void;
}) {
  const c = chartColors();
  const [hover, setHover] = useState<HoverTarget | null>(null);

  // Run the force simulation ONCE per data change (not on every resize). The
  // 300 ticks are synchronous, so keying on `data` alone keeps them off the
  // resize path; responsiveness is handled by the viewBox, not by re-layout.
  const layout = useMemo(() => {
    const height = Math.max(450, Math.min(650, data.nodes.length * 80));
    if (data.nodes.length === 0) {
      return { nodes: [] as SimNode[], edges: [] as SimEdge[], height, viewBox: `0 0 ${LAYOUT_W} ${height}` };
    }

    const nodes: SimNode[] = data.nodes.map((n) => ({
      id: n.id,
      status: n.status,
      rpm: n.rpm,
      p95: n.p95,
      errors: n.errors,
    }));
    const edges: SimEdge[] = data.edges.map((e) => ({
      source: e.source,
      target: e.target,
      rpm: e.rpm,
      errorRate: e.errorRate,
    }));

    const sim: Simulation<SimNode, SimEdge> = forceSimulation(nodes)
      .force(
        "link",
        forceLink<SimNode, SimEdge>(edges)
          .id((d) => d.id)
          .distance(200),
      )
      .force("charge", forceManyBody().strength(-800))
      .force("center", forceCenter(LAYOUT_W / 2, height / 2))
      .stop();
    for (let i = 0; i < 300; i++) sim.tick();

    // Fit the viewBox to the actual node extent so nothing is clipped
    // regardless of how the simulation spread the graph.
    let minX = Infinity, minY = Infinity, maxX = -Infinity, maxY = -Infinity;
    for (const n of nodes) {
      if (n.x === undefined || n.y === undefined) continue;
      if (n.x < minX) minX = n.x;
      if (n.x > maxX) maxX = n.x;
      if (n.y < minY) minY = n.y;
      if (n.y > maxY) maxY = n.y;
    }
    // Padding leaves room for node radius (24) plus the two label lines below.
    const padX = 48, padTop = 36, padBottom = 64;
    const viewBox = Number.isFinite(minX)
      ? `${minX - padX} ${minY - padTop} ${maxX - minX + padX * 2} ${maxY - minY + padTop + padBottom}`
      : `0 0 ${LAYOUT_W} ${height}`;

    return { nodes, edges, height, viewBox };
  }, [data]);

  if (data.nodes.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-muted/50 p-4 text-sm text-muted-foreground">
        No topology data to display.
      </div>
    );
  }

  return (
    <div className="block-card">
      <h3 className="block-title">Topology</h3>
      <div style={{ position: "relative" }}>
        <svg
          width="100%"
          height={layout.height}
          viewBox={layout.viewBox}
          preserveAspectRatio="xMidYMid meet"
          role="img"
          aria-label="Service topology graph"
        >
          <defs>
            <marker
              id="topology-arrow"
              viewBox="0 0 8 8"
              refX={6}
              refY={4}
              markerWidth={6}
              markerHeight={6}
              orient="auto"
            >
              <path d="M 0 0 L 8 4 L 0 8 z" fill={c.mutedForeground} />
            </marker>
            <filter id="topology-glow" x="-50%" y="-50%" width="200%" height="200%">
              <feGaussianBlur stdDeviation="3" result="blur" />
              <feMerge>
                <feMergeNode in="blur" />
                <feMergeNode in="SourceGraphic" />
              </feMerge>
            </filter>
          </defs>
          {layout.edges.map((e, i) => {
            const s = e.source as SimNode;
            const t = e.target as SimNode;
            if (s.x === undefined || s.y === undefined || t.x === undefined || t.y === undefined) {
              return null;
            }
            return (
              <line
                key={`e-${i}`}
                x1={s.x}
                y1={s.y}
                x2={t.x}
                y2={t.y}
                stroke={edgeColor(e.errorRate, c)}
                strokeWidth={Math.max(1.5, Math.min(5, e.rpm / 300))}
                opacity={edgeOpacity(e.errorRate)}
                markerEnd="url(#topology-arrow)"
                onMouseEnter={(ev) => setHover({ kind: "edge", edge: e, x: ev.clientX, y: ev.clientY })}
                onMouseMove={(ev) => setHover({ kind: "edge", edge: e, x: ev.clientX, y: ev.clientY })}
                onMouseLeave={() => setHover(null)}
              />
            );
          })}
          {layout.nodes.map((n) => {
            if (n.x === undefined || n.y === undefined) return null;
            const color = statusColor(n.status, c);
            return (
              <g
                key={`n-${n.id}`}
                style={{ cursor: onAction ? "pointer" : "default" }}
                onClick={() => onAction?.(`Diagnose ${n.id}`)}
                onMouseEnter={(ev) => setHover({ kind: "node", node: n, x: ev.clientX, y: ev.clientY })}
                onMouseMove={(ev) => setHover({ kind: "node", node: n, x: ev.clientX, y: ev.clientY })}
                onMouseLeave={() => setHover(null)}
              >
                <circle
                  cx={n.x}
                  cy={n.y}
                  r={24}
                  fill={color}
                  stroke={color}
                  strokeWidth={2.5}
                  filter={n.status !== "healthy" ? "url(#topology-glow)" : undefined}
                />
                <text
                  x={n.x}
                  y={n.y + 38}
                  textAnchor="middle"
                  fontSize={11}
                  fill={c.foreground}
                  fontWeight={500}
                >
                  {n.id}
                </text>
                <text x={n.x} y={n.y + 52} textAnchor="middle" fontSize={9} fill={c.mutedForeground}>
                  {fmt(n.rpm)} rpm
                </text>
              </g>
            );
          })}
        </svg>
        {hover && (
          <div
            style={{
              ...tooltipBox(c),
              position: "fixed",
              left: hover.x + 12,
              top: hover.y + 12,
              pointerEvents: "none",
              zIndex: 50,
            }}
          >
            {hover.kind === "node" ? (
              <>
                <div style={{ fontWeight: 600 }}>{hover.node.id}</div>
                <div>Status: {hover.node.status}</div>
                <div>Throughput: {fmt(hover.node.rpm)} rpm</div>
                <div>P95: {fmt(hover.node.p95)}ms</div>
                <div>Errors: {fmt(hover.node.errors)}%</div>
              </>
            ) : (
              <>
                <div style={{ fontWeight: 600 }}>
                  {typeof hover.edge.source === "string" ? hover.edge.source : hover.edge.source.id} →{" "}
                  {typeof hover.edge.target === "string" ? hover.edge.target : hover.edge.target.id}
                </div>
                <div>Volume: {fmt(hover.edge.rpm)} rpm</div>
                <div>Error Rate: {fmt(hover.edge.errorRate)}%</div>
              </>
            )}
          </div>
        )}
      </div>
      <div className="mt-1 flex flex-wrap gap-4 px-1 justify-center">
        {[
          { color: c.success, label: "Healthy" },
          { color: c.warning, label: "Degraded" },
          { color: c.destructive, label: "Unhealthy" },
        ].map((item) => (
          <div key={item.label} className="flex items-center gap-1.5 text-xs">
            <span
              className="inline-block h-2.5 w-2.5 rounded-full"
              style={{ backgroundColor: item.color }}
            />
            <span className="text-muted-foreground">{item.label}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
