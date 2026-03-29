import { useMemo } from "react";
import echarts, { ReactECharts, cssVar } from "@/lib/echarts";
import type { SankeyData } from "@/lib/types";

function statusColor(status?: string): string {
  switch (status?.toLowerCase()) {
    case "degraded": return "#f59e0b";
    case "unhealthy": return "#ef4444";
    default: return "#22c55e";
  }
}

export function SankeyBlock({ data }: { data: SankeyData }) {
  const option = useMemo(() => {
    if (data.nodes.length === 0) return {};

    return {
      animation: false,
      tooltip: {
        trigger: "item",
        backgroundColor: cssVar("--popover"),
        borderColor: cssVar("--border"),
        textStyle: { color: cssVar("--popover-foreground"), fontSize: 12 },
      },
      series: [{
        type: "sankey",
        layout: "none",
        emphasis: { focus: "adjacency" },
        nodeAlign: "left",
        nodeWidth: 12,
        nodeGap: 14,
        data: data.nodes.map((n) => ({
          name: n.id,
          itemStyle: { color: statusColor(n.status) },
          label: {
            formatter: `{name|${n.label}}\n{rpm|${n.rpm} rpm}`,
            rich: {
              name: { fontSize: 10, color: cssVar("--foreground"), fontWeight: 500 },
              rpm: { fontSize: 9, color: cssVar("--muted-foreground") },
            },
          },
        })),
        links: data.links.map((l) => ({
          source: l.source,
          target: l.target,
          value: l.value,
        })),
        lineStyle: { color: "gradient", curveness: 0.5, opacity: 0.35 },
      }],
    };
  }, [data]);

  if (data.nodes.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-muted/50 p-4 text-sm text-muted-foreground">
        No flow data to display.
      </div>
    );
  }

  const height = Math.max(300, data.nodes.length * 40);

  return (
    <div>
      <ReactECharts echarts={echarts} option={option} style={{ height }} opts={{ renderer: "svg" }} />
    </div>
  );
}
