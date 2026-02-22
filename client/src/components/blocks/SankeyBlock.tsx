import { useRef, useEffect, useState, useCallback, useMemo } from "react";
import type { SankeyData } from "@/lib/types";

const PAD_X = 40;
const PAD_Y = 24;
const NODE_W = 12;
const NODE_GAP = 14;

interface PositionedNode {
  id: string;
  label: string;
  rpm: number;
  status?: string;
  layer: number;
  x: number;
  y: number;
  h: number;
}

function statusColor(status?: string): string {
  switch (status?.toLowerCase()) {
    case "degraded":
      return "#f59e0b";
    case "unhealthy":
      return "#ef4444";
    default:
      return "#22c55e";
  }
}

/** Assign layers via topological order (BFS from sources). */
function assignLayers(
  nodes: SankeyData["nodes"],
  links: SankeyData["links"],
): Map<string, number> {
  const outgoing = new Map<string, string[]>();
  const incoming = new Map<string, string[]>();

  for (const n of nodes) {
    outgoing.set(n.id, []);
    incoming.set(n.id, []);
  }
  for (const l of links) {
    outgoing.get(l.source)?.push(l.target);
    incoming.get(l.target)?.push(l.source);
  }

  const layers = new Map<string, number>();
  const queue: string[] = [];

  // Sources: nodes with no incoming links
  for (const n of nodes) {
    if ((incoming.get(n.id)?.length ?? 0) === 0) {
      layers.set(n.id, 0);
      queue.push(n.id);
    }
  }

  // If no pure sources, start with all nodes at layer 0
  if (queue.length === 0) {
    for (const n of nodes) {
      layers.set(n.id, 0);
      queue.push(n.id);
    }
  }

  while (queue.length > 0) {
    const id = queue.shift()!;
    const layer = layers.get(id) ?? 0;
    for (const target of outgoing.get(id) ?? []) {
      const existing = layers.get(target);
      if (existing === undefined || existing <= layer) {
        layers.set(target, layer + 1);
        queue.push(target);
      }
    }
  }

  return layers;
}

export function SankeyBlock({ data }: { data: SankeyData }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(800);
  const [tooltip, setTooltip] = useState<{
    x: number;
    y: number;
    content: { title: string; rows: [string, string][] };
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

  const layout = useMemo(() => {
    if (data.nodes.length === 0) return { nodes: [], links: [], height: 100 };

    const layers = assignLayers(data.nodes, data.links);
    const maxLayer = Math.max(...layers.values(), 0);
    const layerW = maxLayer > 0 ? (width - PAD_X * 2 - NODE_W) / maxLayer : 0;

    // Group nodes by layer
    const groups = new Map<number, typeof data.nodes>();
    for (const n of data.nodes) {
      const l = layers.get(n.id) ?? 0;
      if (!groups.has(l)) groups.set(l, []);
      groups.get(l)!.push(n);
    }

    const maxRPM = Math.max(...data.nodes.map((n) => n.rpm), 1);
    const maxNodeH = 180;

    // Position nodes
    const positioned = new Map<string, PositionedNode>();
    let totalHeight = 0;

    for (let l = 0; l <= maxLayer; l++) {
      const group = groups.get(l) ?? [];
      const x = PAD_X + l * layerW;
      let yOff = PAD_Y;

      for (const n of group) {
        const h = Math.max(24, (n.rpm / maxRPM) * maxNodeH);
        positioned.set(n.id, {
          ...n,
          layer: l,
          x,
          y: yOff,
          h,
        });
        yOff += h + NODE_GAP;
      }
      totalHeight = Math.max(totalHeight, yOff);
    }

    // Sort links by value descending
    const sortedLinks = [...data.links].sort((a, b) => b.value - a.value);

    // Track offsets for stacking link bands
    const outOff = new Map<string, number>();
    const inOff = new Map<string, number>();
    for (const n of data.nodes) {
      outOff.set(n.id, 0);
      inOff.set(n.id, 0);
    }

    const positionedLinks = sortedLinks.map((link) => {
      const src = positioned.get(link.source);
      const tgt = positioned.get(link.target);
      if (!src || !tgt) return null;

      const srcRPM = src.rpm || 1;
      const tgtRPM = tgt.rpm || 1;
      const linkHSrc = Math.max(3, (link.value / srcRPM) * src.h);
      const linkHTgt = Math.max(3, (link.value / tgtRPM) * tgt.h);

      const x1 = src.x + NODE_W;
      const y1 = src.y + (outOff.get(link.source) ?? 0);
      const x2 = tgt.x;
      const y2 = tgt.y + (inOff.get(link.target) ?? 0);

      outOff.set(link.source, (outOff.get(link.source) ?? 0) + linkHSrc);
      inOff.set(link.target, (inOff.get(link.target) ?? 0) + linkHTgt);

      const cx = (x1 + x2) / 2;
      const isDegraded =
        tgt.status === "degraded" || src.status === "degraded";
      const color = isDegraded ? "#f59e0b" : "#0ea5e9";

      const path =
        `M ${x1} ${y1} C ${cx} ${y1}, ${cx} ${y2}, ${x2} ${y2} ` +
        `L ${x2} ${y2 + linkHTgt} ` +
        `C ${cx} ${y2 + linkHTgt}, ${cx} ${y1 + linkHSrc}, ${x1} ${y1 + linkHSrc} Z`;

      return { ...link, path, color };
    });

    return {
      nodes: Array.from(positioned.values()),
      links: positionedLinks.filter(Boolean) as {
        source: string;
        target: string;
        value: number;
        path: string;
        color: string;
      }[],
      height: totalHeight + PAD_Y,
      maxLayer,
    };
  }, [data, width]);

  if (data.nodes.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-muted/50 p-4 text-sm text-muted-foreground">
        No flow data to display.
      </div>
    );
  }

  return (
    <div ref={containerRef} className="relative">
      <svg
        width={width}
        height={layout.height}
        className="block select-none"
        onMouseLeave={() => setTooltip(null)}
      >
        {/* Links */}
        {layout.links.map((link, i) => (
          <path
            key={i}
            d={link.path}
            fill={link.color}
            stroke={link.color}
            strokeWidth={0.5}
            opacity={0.35}
            className="cursor-pointer transition-opacity hover:opacity-60"
            onMouseEnter={(e) => {
              const rect = containerRef.current?.getBoundingClientRect();
              if (rect) {
                setTooltip({
                  x: e.clientX - rect.left,
                  y: e.clientY - rect.top,
                  content: {
                    title: `${link.source} \u2192 ${link.target}`,
                    rows: [["Volume", `${link.value} rpm`]],
                  },
                });
              }
            }}
            onMouseLeave={() => setTooltip(null)}
          />
        ))}

        {/* Nodes */}
        {layout.nodes.map((node) => {
          const isLast = node.layer === (layout.maxLayer ?? 0);
          const labelX = isLast ? node.x + NODE_W + 8 : node.x - 8;
          const anchor = isLast ? "start" : "end";

          return (
            <g
              key={node.id}
              className="cursor-pointer"
              onMouseEnter={(e) => {
                const rect = containerRef.current?.getBoundingClientRect();
                if (rect) {
                  setTooltip({
                    x: e.clientX - rect.left,
                    y: e.clientY - rect.top,
                    content: {
                      title: node.label,
                      rows: [
                        ["Volume", `${node.rpm} rpm`],
                        ["Status", node.status ?? "healthy"],
                      ],
                    },
                  });
                }
              }}
              onMouseLeave={() => setTooltip(null)}
            >
              <rect
                x={node.x}
                y={node.y}
                width={NODE_W}
                height={node.h}
                fill={statusColor(node.status)}
                rx={2}
                opacity={0.9}
              />
              <text
                x={labelX}
                y={node.y + node.h / 2 - 4}
                textAnchor={anchor}
                className="fill-foreground text-[10px] font-medium"
              >
                {node.label}
              </text>
              <text
                x={labelX}
                y={node.y + node.h / 2 + 9}
                textAnchor={anchor}
                className="fill-muted-foreground text-[9px]"
              >
                {node.rpm} rpm
              </text>
            </g>
          );
        })}
      </svg>

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
            {tooltip.content.rows.map(([label, value], i) => (
              <div key={i} className="flex justify-between gap-4">
                <span>{label}</span>
                <span className="font-mono text-foreground">{value}</span>
              </div>
            ))}
          </div>
        </div>
      )}
    </div>
  );
}
