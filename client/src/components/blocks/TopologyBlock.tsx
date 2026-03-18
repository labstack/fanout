import { useRef, useEffect, useState, useCallback, useMemo } from "react";
import {
  forceSimulation,
  forceLink,
  forceManyBody,
  forceCenter,
  forceCollide,
  forceX,
  forceY,
  type SimulationNodeDatum,
  type SimulationLinkDatum,
} from "d3";
import type { TopologyData, TopologyNode, TopologyEdge } from "@/lib/types";

const NODE_RADIUS = 24;
const PADDING = 80;

interface SimNode extends SimulationNodeDatum {
  id: string;
  status: string;
  rpm: number;
  p95: number;
  errors: number;
}

interface SimLink extends SimulationLinkDatum<SimNode> {
  rpm: number;
  errorRate: number;
}

function statusFill(status: string): string {
  switch (status.toLowerCase()) {
    case "healthy":
      return "#22c55e";
    case "degraded":
      return "#f59e0b";
    case "unhealthy":
      return "#ef4444";
    default:
      return "#6b7280";
  }
}

function statusStroke(status: string): string {
  switch (status.toLowerCase()) {
    case "healthy":
      return "#16a34a";
    case "degraded":
      return "#d97706";
    case "unhealthy":
      return "#dc2626";
    default:
      return "#4b5563";
  }
}

/** Run d3-force synchronously and return positioned nodes/links. */
function computeLayout(
  nodes: TopologyNode[],
  edges: TopologyEdge[],
  width: number,
  height: number,
): { nodes: SimNode[]; links: SimLink[] } {
  const simNodes: SimNode[] = nodes.map((n) => ({
    id: n.id,
    status: n.status,
    rpm: n.rpm,
    p95: n.p95,
    errors: n.errors,
  }));

  const nodeById = new Map(simNodes.map((n) => [n.id, n]));
  const simLinks: SimLink[] = edges
    .filter((e) => nodeById.has(e.source) && nodeById.has(e.target))
    .map((e) => ({
      source: e.source,
      target: e.target,
      rpm: e.rpm,
      errorRate: e.errorRate,
    }));

  const simulation = forceSimulation<SimNode>(simNodes)
    .force(
      "link",
      forceLink<SimNode, SimLink>(simLinks)
        .id((d) => d.id)
        .distance(200),
    )
    .force("charge", forceManyBody().strength(-800))
    .force("center", forceCenter(width / 2, height / 2))
    .force("collide", forceCollide(NODE_RADIUS + 40))
    .force("x", forceX(width / 2).strength(0.05))
    .force("y", forceY(height / 2).strength(0.05))
    .stop();

  // Run synchronously
  for (let i = 0; i < 300; i++) {
    simulation.tick();
  }

  // Clamp nodes within bounds
  for (const node of simNodes) {
    node.x = Math.max(PADDING, Math.min(width - PADDING, node.x ?? 0));
    node.y = Math.max(PADDING, Math.min(height - PADDING, node.y ?? 0));
  }

  return { nodes: simNodes, links: simLinks };
}

export function TopologyBlock({ data, onAction }: { data: TopologyData; onAction?: (prompt: string) => void }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(700);
  const [tooltip, setTooltip] = useState<{
    x: number;
    y: number;
    content: { title: string; rows: [string, string, string?][] };
  } | null>(null);

  const updateWidth = useCallback(() => {
    if (containerRef.current) {
      setWidth(containerRef.current.clientWidth);
    }
  }, []);

  useEffect(() => {
    updateWidth();
    const observer = new ResizeObserver(updateWidth);
    if (containerRef.current) {
      observer.observe(containerRef.current);
    }
    return () => observer.disconnect();
  }, [updateWidth]);

  const height = Math.max(450, Math.min(650, data.nodes.length * 80));

  const layout = useMemo(
    () => computeLayout(data.nodes, data.edges, width, height),
    [data.nodes, data.edges, width, height],
  );

  if (data.nodes.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-muted/50 p-4 text-sm text-muted-foreground">
        No topology data to display.
      </div>
    );
  }

  const edgeStrokeWidth = (rpm: number) =>
    Math.max(1.5, Math.min(5, rpm / 300));

  return (
    <div ref={containerRef} className="relative">
      <svg
        width={width}
        height={height}
        className="block select-none"
        onMouseLeave={() => setTooltip(null)}
      >
        <defs>
          <marker
            id="topo-arrow"
            viewBox="0 0 10 7"
            refX="10"
            refY="3.5"
            markerWidth="7"
            markerHeight="5"
            orient="auto-start-reverse"
          >
            <polygon
              points="0 0, 10 3.5, 0 7"
              fill="hsl(var(--border))"
            />
          </marker>
          <marker
            id="topo-arrow-warn"
            viewBox="0 0 10 7"
            refX="10"
            refY="3.5"
            markerWidth="7"
            markerHeight="5"
            orient="auto-start-reverse"
          >
            <polygon points="0 0, 10 3.5, 0 7" fill="#f59e0b" />
          </marker>
          <marker
            id="topo-arrow-danger"
            viewBox="0 0 10 7"
            refX="10"
            refY="3.5"
            markerWidth="7"
            markerHeight="5"
            orient="auto-start-reverse"
          >
            <polygon points="0 0, 10 3.5, 0 7" fill="#ef4444" />
          </marker>
        </defs>

        {/* Edges */}
        {layout.links.map((link, i) => {
          const source = link.source as SimNode;
          const target = link.target as SimNode;
          if (!source.x || !source.y || !target.x || !target.y) return null;

          const x1 = source.x;
          const y1 = source.y;
          const x2 = target.x;
          const y2 = target.y;

          // Shorten the line so the arrowhead ends at the node circle edge
          const dx = x2 - x1;
          const dy = y2 - y1;
          const dist = Math.sqrt(dx * dx + dy * dy) || 1;

          // Skip edges where nodes overlap — avoids floating arrowheads
          if (dist < (NODE_RADIUS + 4) * 2 + 10) return null;

          const offsetX = (dx / dist) * (NODE_RADIUS + 4);
          const offsetY = (dy / dist) * (NODE_RADIUS + 4);

          const isDanger = link.errorRate > 3;
          const isWarn = link.errorRate > 1;
          const sw = edgeStrokeWidth(link.rpm);
          const marker = isDanger
            ? "url(#topo-arrow-danger)"
            : isWarn
              ? "url(#topo-arrow-warn)"
              : "url(#topo-arrow)";
          const stroke = isDanger
            ? "#ef4444"
            : isWarn
              ? "#f59e0b"
              : "hsl(var(--border))";

          return (
            <line
              key={i}
              x1={x1 + offsetX}
              y1={y1 + offsetY}
              x2={x2 - offsetX}
              y2={y2 - offsetY}
              stroke={stroke}
              strokeWidth={sw}
              opacity={isDanger ? 0.8 : isWarn ? 0.7 : 0.6}
              markerEnd={marker}
              className="cursor-pointer"
              onMouseEnter={(e) => {
                const rect = containerRef.current?.getBoundingClientRect();
                if (rect) {
                  setTooltip({
                    x: e.clientX - rect.left,
                    y: e.clientY - rect.top,
                    content: {
                      title: `${source.id} \u2192 ${target.id}`,
                      rows: [
                        ["Volume", `${link.rpm} rpm`],
                        [
                          "Error Rate",
                          `${link.errorRate}%`,
                          link.errorRate > 1 ? "#f59e0b" : undefined,
                        ],
                      ],
                    },
                  });
                }
              }}
              onMouseLeave={() => setTooltip(null)}
            />
          );
        })}

        {/* Nodes */}
        {layout.nodes.map((node) => (
          <g
            key={node.id}
            transform={`translate(${node.x ?? 0},${node.y ?? 0})`}
            className="cursor-pointer"
            onClick={() => onAction?.(`Diagnose ${node.id}`)}
            onMouseEnter={(e) => {
              const rect = containerRef.current?.getBoundingClientRect();
              if (rect) {
                setTooltip({
                  x: e.clientX - rect.left,
                  y: e.clientY - rect.top,
                  content: {
                    title: node.id,
                    rows: [
                      ["Status", node.status, statusFill(node.status)],
                      ["Throughput", `${node.rpm} rpm`],
                      ["P95 Latency", `${node.p95}ms`],
                      ["Error Rate", `${node.errors}%`],
                    ],
                  },
                });
              }
            }}
            onMouseLeave={() => setTooltip(null)}
          >
            {/* Outer glow for unhealthy/degraded */}
            {(node.status === "unhealthy" || node.status === "degraded") && (
              <circle
                r={NODE_RADIUS + 4}
                fill="none"
                stroke={statusFill(node.status)}
                strokeWidth={2}
                opacity={0.3}
                strokeDasharray={node.status === "unhealthy" ? "4,3" : "none"}
              />
            )}
            {/* Node circle */}
            <circle
              r={NODE_RADIUS}
              fill={statusFill(node.status)}
              stroke={statusStroke(node.status)}
              strokeWidth={2.5}
              opacity={0.9}
            />
            {/* RPM inside circle */}
            <text
              y={1}
              textAnchor="middle"
              dominantBaseline="central"
              className="fill-white text-[9px] font-bold"
              style={{ textShadow: "0 1px 2px rgba(0,0,0,0.5)" }}
            >
              {node.rpm >= 1000 ? `${(node.rpm / 1000).toFixed(1)}k` : node.rpm}
            </text>
            {/* Service name below circle */}
            <text
              y={NODE_RADIUS + 14}
              textAnchor="middle"
              className="fill-foreground text-[11px] font-medium"
            >
              {node.id}
            </text>
          </g>
        ))}
      </svg>

      {/* Legend */}
      <div className="mt-1 flex flex-wrap gap-4 px-1 justify-center">
        {[
          { color: "#22c55e", label: "Healthy" },
          { color: "#f59e0b", label: "Degraded" },
          { color: "#ef4444", label: "Unhealthy" },
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

      {/* Tooltip */}
      {tooltip && (
        <div
          className="pointer-events-none absolute z-50 rounded-md border border-border bg-popover px-3 py-2 text-xs text-popover-foreground shadow-md"
          style={{
            left: Math.min(tooltip.x + 12, width - 200),
            top: tooltip.y - 8,
          }}
        >
          <div className="mb-1 font-medium">{tooltip.content.title}</div>
          <div className="space-y-0.5 text-muted-foreground">
            {tooltip.content.rows.map(([label, value, color], i) => (
              <div key={i} className="flex justify-between gap-4">
                <span>{label}</span>
                <span
                  className="font-mono"
                  style={{ color: color ?? "hsl(var(--foreground))" }}
                >
                  {value}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
