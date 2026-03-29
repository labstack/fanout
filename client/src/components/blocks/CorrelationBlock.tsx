import { useMemo } from "react";
import echarts, { ReactECharts, cssVar } from "@/lib/echarts";
import type { CorrelationData } from "@/lib/types";

const PANEL_H = 80;
const GAP = 40;

export function CorrelationBlock({ data }: { data: CorrelationData }) {
  const option = useMemo(() => {
    if (data.panels.length === 0 || data.times.length < 2) return {};

    const grids: any[] = [];
    const xAxes: any[] = [];
    const yAxes: any[] = [];
    const series: any[] = [];

    data.panels.forEach((panel, i) => {
      const top = i * (PANEL_H + GAP) + 24;
      grids.push({ left: 56, right: 12, top, height: PANEL_H });

      xAxes.push({
        type: "category",
        data: data.times,
        gridIndex: i,
        show: i === data.panels.length - 1,
        axisLine: { lineStyle: { color: cssVar("--border") } },
        axisLabel: { color: cssVar("--muted-foreground"), fontSize: 9, interval: Math.max(0, Math.ceil(data.times.length / 10) - 1) },
      });

      const maxVal = Math.max(...panel.values) * 1.2 || 1;
      yAxes.push({
        type: "value",
        gridIndex: i,
        name: panel.label,
        nameTextStyle: { color: cssVar("--foreground"), fontSize: 9 },
        nameGap: 40,
        max: maxVal,
        axisLine: { lineStyle: { color: cssVar("--border") } },
        axisLabel: { color: cssVar("--muted-foreground"), fontSize: 8 },
        splitLine: { lineStyle: { color: cssVar("--border"), type: "dashed" } },
      });

      // Area + line series
      series.push({
        type: "line",
        data: panel.values,
        xAxisIndex: i,
        yAxisIndex: i,
        symbol: "none",
        lineStyle: { width: 1.5, color: panel.color },
        areaStyle: { color: panel.color, opacity: 0.1 },
        markLine: panel.baseline !== undefined ? {
          silent: true,
          symbol: "none",
          lineStyle: { color: panel.color, type: "dashed", width: 0.5, opacity: 0.4 },
          data: [{ yAxis: panel.baseline }],
          label: { show: false },
        } : undefined,
        markPoint: panel.markers?.length ? {
          symbol: "circle",
          symbolSize: 7,
          data: panel.markers.map((m) => ({
            coord: [m.t, panel.values[data.times.indexOf(m.t)] ?? 0],
            itemStyle: { color: m.severity === "critical" ? "#ef4444" : "#f59e0b" },
            name: m.label,
          })),
        } : undefined,
      });
    });

    return {
      animation: false,
      grid: grids,
      xAxis: xAxes,
      yAxis: yAxes,
      series,
      tooltip: {
        trigger: "axis",
        backgroundColor: cssVar("--popover"),
        borderColor: cssVar("--border"),
        textStyle: { color: cssVar("--popover-foreground"), fontSize: 12 },
      },
    };
  }, [data]);

  const totalH = data.panels.length * (PANEL_H + GAP) + 40;

  if (data.panels.length === 0 || data.times.length < 2) {
    return (
      <div className="rounded-lg border border-border bg-muted/50 p-4 text-sm text-muted-foreground">
        No correlation data to display.
      </div>
    );
  }

  return (
    <div>
      <ReactECharts echarts={echarts} option={option} style={{ height: totalH }} opts={{ renderer: "svg" }} />
    </div>
  );
}
