import { useMemo } from "react";
import echarts, { ReactECharts, tooltipStyle, statusColor, cssVar, esc } from "@/lib/echarts";
import type { TopologyData } from "@/lib/types";
import { fmt } from "@/lib/utils";
import { COLORS } from "@/lib/theme";

export function TopologyBlock({ data, onAction }: { data: TopologyData; onAction?: (prompt: string) => void }) {
  const onEvents = useMemo(() => ({
    click: (params: any) => {
      if (params.dataType === "node" && onAction) {
        onAction(`Diagnose ${params.data.name}`);
      }
    },
  }), [onAction]);

  const option = useMemo(() => {
    if (data.nodes.length === 0) return {};

    return {
      animation: false,
      tooltip: {
        ...tooltipStyle(),
        formatter: (params: any) => {
          if (params.dataType === "node") {
            const d = params.data;
            return `<b>${esc(d.name)}</b><br/>Status: ${esc(d.status)}<br/>Throughput: ${fmt(d.rpm)} rpm<br/>P95: ${fmt(d.p95)}ms<br/>Errors: ${fmt(d.errors)}%`;
          }
          if (params.dataType === "edge") {
            const d = params.data;
            return `<b>${esc(d.source)} → ${esc(d.target)}</b><br/>Volume: ${fmt(d.rpm)} rpm<br/>Error Rate: ${fmt(d.errorRate)}%`;
          }
          return "";
        },
      },
      series: [{
        type: "graph",
        layout: "force",
        roam: true,
        force: {
          repulsion: 800,
          edgeLength: 200,
          layoutAnimation: false,
        },
        symbol: "circle",
        symbolSize: 48,
        label: {
          show: true,
          position: "bottom",
          distance: 8,
        },
        edgeSymbol: ["none", "arrow"],
        edgeSymbolSize: 8,
        data: data.nodes.map((n) => {
          const color = statusColor(n.status);
          return {
            name: n.id,
            status: n.status,
            rpm: n.rpm,
            p95: n.p95,
            errors: n.errors,
            itemStyle: {
              color,
              borderColor: color,
              borderWidth: 2.5,
              shadowBlur: n.status !== "healthy" ? 10 : 0,
              shadowColor: color,
            },
            label: {
              show: true,
              position: "bottom",
              distance: 8,
              formatter: `{name|${n.id}}\n{rpm|${fmt(n.rpm)} rpm}`,
              rich: {
                name: { fontSize: 11, color: cssVar("--foreground"), fontWeight: 500, align: "center" },
                rpm: { fontSize: 9, color: cssVar("--muted-foreground"), align: "center" },
              },
            },
          };
        }),
        edges: data.edges.map((e) => ({
          source: e.source,
          target: e.target,
          rpm: e.rpm,
          errorRate: e.errorRate,
          lineStyle: {
            width: Math.max(1.5, Math.min(5, e.rpm / 300)),
            color: e.errorRate > 3 ? COLORS.unhealthy : e.errorRate > 1 ? COLORS.degraded : cssVar("--border"),
            opacity: e.errorRate > 3 ? 0.8 : e.errorRate > 1 ? 0.7 : 0.6,
          },
        })),
      }],
    };
  }, [data]);

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
      <ReactECharts
        echarts={echarts}
        option={option}
        style={{ height }}
        opts={{ renderer: "svg" }}
        onEvents={onEvents}
      />
      <div className="mt-1 flex flex-wrap gap-4 px-1 justify-center">
        {[
          { color: COLORS.healthy, label: "Healthy" },
          { color: COLORS.degraded, label: "Degraded" },
          { color: COLORS.unhealthy, label: "Unhealthy" },
        ].map((item) => (
          <div key={item.label} className="flex items-center gap-1.5 text-xs">
            <span className="inline-block h-2.5 w-2.5 rounded-full" style={{ backgroundColor: item.color }} />
            <span className="text-muted-foreground">{item.label}</span>
          </div>
        ))}
      </div>
    </div>
  );
}
