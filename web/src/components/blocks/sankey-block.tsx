import { ResponsiveContainer, Sankey, Tooltip } from "recharts";
import { chartColors, statusColor, tooltipBox } from "@/lib/chart-theme";
import type { SankeyData } from "@/lib/types";

interface NodePayload {
  name: string;
  label: string;
  rpm: number;
  status?: string;
}

export function SankeyBlock({ data }: { data: SankeyData }) {
  if (data.nodes.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-muted/50 p-4 text-sm text-muted-foreground">
        No flow data to display.
      </div>
    );
  }

  const c = chartColors();
  const nodeIndex = new Map(data.nodes.map((n, i) => [n.id, i]));

  const nodes: NodePayload[] = data.nodes.map((n) => ({
    name: n.id,
    label: n.label,
    rpm: n.rpm,
    status: n.status,
  }));

  // Drop links whose endpoints aren't in the node set rather than silently
  // rewiring them to node 0 (which would fabricate misleading connections).
  const links = data.links.flatMap((l) => {
    const s = nodeIndex.get(l.source);
    const t = nodeIndex.get(l.target);
    if (s === undefined || t === undefined) return [];
    return [{ source: s, target: t, value: l.value }];
  });

  const height = Math.max(300, data.nodes.length * 40);

  return (
    <div className="block-card">
      <h3 className="block-title">Flow</h3>
      <ResponsiveContainer width="100%" height={height}>
        <Sankey
          data={{ nodes, links }}
          nodePadding={14}
          nodeWidth={12}
          margin={{ top: 16, right: 80, bottom: 16, left: 80 }}
          link={{ stroke: c.border, strokeOpacity: 0.35 }}
          node={({ x, y, width, height: h, payload }) => {
            const p = payload as unknown as NodePayload;
            const fill = statusColor(p.status ?? "healthy");
            return (
              <g>
                <rect x={x} y={y} width={width} height={h} fill={fill} />
                <text
                  x={x + width + 6}
                  y={y + h / 2}
                  dy={4}
                  fontSize={10}
                  fill={c.foreground}
                  fontWeight={500}
                >
                  {p.label}
                </text>
                <text
                  x={x + width + 6}
                  y={y + h / 2 + 12}
                  dy={4}
                  fontSize={9}
                  fill={c.mutedForeground}
                >
                  {p.rpm} rpm
                </text>
              </g>
            );
          }}
        >
          <Tooltip
            content={(props) => {
              const item = props.payload?.[0]?.payload as
                | { source: number | NodePayload; target: number | NodePayload; value: number }
                | NodePayload
                | undefined;
              if (!props.active || !item) return null;
              // Nodes carry `rpm`; links don't. Discriminate on that rather than
              // on `source`/`target` so a future field rename on the node side
              // can't silently misclassify a node hover as a link.
              if ("rpm" in item) {
                return (
                  <div style={tooltipBox(c)}>
                    <div style={{ fontWeight: 500 }}>{item.label}</div>
                    <div>{item.rpm} rpm</div>
                  </div>
                );
              }
              const src = typeof item.source === "number" ? nodes[item.source] : item.source;
              const tgt = typeof item.target === "number" ? nodes[item.target] : item.target;
              return (
                <div style={tooltipBox(c)}>
                  {src?.label ?? "?"} → {tgt?.label ?? "?"}: {item.value}
                </div>
              );
            }}
          />
        </Sankey>
      </ResponsiveContainer>
    </div>
  );
}
