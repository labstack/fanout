import { useMemo } from "react";
import echarts, { ReactECharts, tooltipStyle, statusColor, cssVar } from "@/lib/echarts";
import type { SankeyData } from "@/lib/types";

export function SankeyBlock({ data }: { data: SankeyData }) {
  const option = useMemo(() => {
    if (data.nodes.length === 0) return {};

    return {
      animation: false,
      tooltip: { trigger: "item", ...tooltipStyle() },
      series: [{
        type: "sankey",
        emphasis: { focus: "adjacency" },
        nodeAlign: "left",
        nodeWidth: 12,
        nodeGap: 14,
        data: data.nodes.map((n) => ({
          name: n.id,
          itemStyle: { color: statusColor(n.status ?? "healthy") },
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
    <div className="block-card">
      <h3 className="block-title">Flow</h3>
      <ReactECharts echarts={echarts} option={option} style={{ height }} opts={{ renderer: "svg" }} />
    </div>
  );
}
