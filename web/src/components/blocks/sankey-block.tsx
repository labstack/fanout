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

  const links = data.links.map((l) => ({
    source: nodeIndex.get(l.source) ?? 0,
    target: nodeIndex.get(l.target) ?? 0,
    value: l.value,
  }));

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
                | (NodePayload & { source?: NodePayload; target?: NodePayload; value?: number })
                | undefined;
              if (!props.active || !item) return null;
              if (item.source && item.target) {
                return (
                  <div style={tooltipBox(c)}>
                    {item.source.label} → {item.target.label}: {item.value}
                  </div>
                );
              }
              return (
                <div style={tooltipBox(c)}>
                  <div style={{ fontWeight: 500 }}>{item.label}</div>
                  <div>{item.rpm} rpm</div>
                </div>
              );
            }}
          />
        </Sankey>
      </ResponsiveContainer>
    </div>
  );
}
