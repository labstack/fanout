import { useRef, useEffect, useState, useCallback, useMemo } from "react";
import {
  forceSimulation,
  forceLink,
  forceManyBody,
  forceCenter,
  forceCollide,
  type SimulationNodeDatum,
  type SimulationLinkDatum,
} from "d3";
import type { TopologyData, TopologyNode, TopologyEdge } from "@/lib/types";

const NODE_RADIUS = 28;
const PADDING = 60;

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
        .distance(140),
    )
    .force("charge", forceManyBody().strength(-400))
    .force("center", forceCenter(width / 2, height / 2))
    .force("collide", forceCollide(NODE_RADIUS + 20))
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

export function TopologyBlock({ data }: { data: TopologyData }) {
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

  const height = Math.max(350, Math.min(500, data.nodes.length * 80));

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
    Math.max(1.5, Math.min(4, rpm / 400));

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
            markerWidth="8"
            markerHeight="6"
            orient="auto-start-reverse"
          >
            <polygon
              points="0 0, 10 3.5, 0 7"
              className="fill-muted-foreground"
              opacity={0.5}
            />
          </marker>
          <marker
            id="topo-arrow-warn"
            viewBox="0 0 10 7"
            refX="10"
            refY="3.5"
            markerWidth="8"
            markerHeight="6"
            orient="auto-start-reverse"
          >
            <polygon points="0 0, 10 3.5, 0 7" fill="#f59e0b" opacity={0.6} />
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
          const offsetX = (dx / dist) * NODE_RADIUS;
          const offsetY = (dy / dist) * NODE_RADIUS;

          const isWarn = link.errorRate > 1;
          const sw = edgeStrokeWidth(link.rpm);

          return (
            <line
              key={i}
              x1={x1 + offsetX}
              y1={y1 + offsetY}
              x2={x2 - offsetX}
              y2={y2 - offsetY}
              stroke={isWarn ? "#f59e0b" : "hsl(var(--muted-foreground))"}
              strokeWidth={sw}
              opacity={isWarn ? 0.7 : 0.4}
              markerEnd={isWarn ? "url(#topo-arrow-warn)" : "url(#topo-arrow)"}
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
            {/* Node circle */}
            <circle
              r={NODE_RADIUS}
              fill={statusFill(node.status)}
              stroke={statusStroke(node.status)}
              strokeWidth={2}
              opacity={0.9}
            />
            {/* Service name */}
            <text
              y={-4}
              textAnchor="middle"
              className="fill-white text-[10px] font-semibold"
              style={{ textShadow: "0 1px 2px rgba(0,0,0,0.5)" }}
            >
              {node.id.length > 10
                ? node.id.slice(0, 9) + "\u2026"
                : node.id}
            </text>
            {/* RPM label */}
            <text
              y={10}
              textAnchor="middle"
              className="fill-white/80 text-[8px]"
              style={{ textShadow: "0 1px 2px rgba(0,0,0,0.5)" }}
            >
              {node.rpm} rpm
            </text>
          </g>
        ))}
      </svg>

      {/* Legend */}
      <div className="mt-2 flex flex-wrap gap-3 px-1">
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
