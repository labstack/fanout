import { useMemo, useState } from "react";
import {
  LineChart,
  ResponsiveContainer,
  useChartHeight,
  useChartWidth,
} from "recharts";
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

function TopologyLayer({
  data,
  onAction,
  c,
  setHover,
}: {
  data: TopologyData;
  onAction?: (prompt: string) => void;
  c: ReturnType<typeof chartColors>;
  setHover: (h: HoverTarget | null) => void;
}) {
  const w = useChartWidth();
  const h = useChartHeight();

  const positioned = useMemo(() => {
    if (!w || !h) return { nodes: [], edges: [] };

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
      .force("center", forceCenter(w / 2, h / 2))
      .stop();
    for (let i = 0; i < 300; i++) sim.tick();

    return { nodes, edges };
  }, [data, w, h]);

  return (
    <g>
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
      {positioned.edges.map((e, i) => {
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
      {positioned.nodes.map((n) => {
        if (n.x === undefined || n.y === undefined) return null;
        const color = statusColor(n.status);
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
    </g>
  );
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

  if (data.nodes.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-muted/50 p-4 text-sm text-muted-foreground">
        No topology data to display.
      </div>
    );
  }

  const height = Math.max(450, Math.min(650, data.nodes.length * 80));

  return (
    <div className="block-card">
      <h3 className="block-title">Topology</h3>
      <div style={{ position: "relative" }}>
        <ResponsiveContainer width="100%" height={height}>
          <LineChart margin={{ top: 0, right: 0, bottom: 0, left: 0 }}>
            <TopologyLayer data={data} onAction={onAction} c={c} setHover={setHover} />
          </LineChart>
        </ResponsiveContainer>
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
