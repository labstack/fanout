import { useMemo } from "react";
import echarts, { ReactECharts, tooltipStyle, axisLine, splitLine, cssVar } from "@/lib/echarts";
import type { CorrelationData } from "@/lib/types";
import { COLORS } from "@/lib/theme";

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
        axisLine: axisLine(),
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
        axisLine: axisLine(),
        axisLabel: { color: cssVar("--muted-foreground"), fontSize: 8 },
        splitLine: splitLine(),
      });

      series.push({
        type: "line",
        data: panel.values,
        xAxisIndex: i,
        yAxisIndex: i,
        symbol: "none",
        lineStyle: { width: 1.5, color: panel.color },
        areaStyle: { color: panel.color, opacity: 0.1 },
        markLine: (() => {
          const lines: any[] = [];
          // Baseline
          if (panel.baseline !== undefined) {
            lines.push({ yAxis: panel.baseline, lineStyle: { color: panel.color, type: "dashed", width: 0.5, opacity: 0.4 }, label: { show: false } });
          }
          // Vertical marker lines at event positions
          if (panel.markers?.length) {
            for (const m of panel.markers) {
              const color = m.severity === "critical" ? COLORS.unhealthy : COLORS.degraded;
              lines.push({ xAxis: m.t, lineStyle: { color, type: "dashed", width: 0.75, opacity: 0.5 }, label: { show: true, formatter: m.label.length > 12 ? m.label.slice(0, 11) + "\u2026" : m.label, fontSize: 7, color, position: "start" } });
            }
          }
          return lines.length > 0 ? { silent: true, symbol: "none", data: lines } : undefined;
        })(),
        markPoint: panel.markers?.length ? {
          symbol: "circle",
          symbolSize: 7,
          data: panel.markers.map((m) => ({
            coord: [m.t, panel.values[data.times.indexOf(m.t)] ?? 0],
            itemStyle: { color: m.severity === "critical" ? COLORS.unhealthy : COLORS.degraded },
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
      tooltip: { trigger: "axis", ...tooltipStyle() },
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
    <div className="block-card">
      <h3 className="block-title">Correlation</h3>
      <ReactECharts echarts={echarts} option={option} style={{ height: totalH }} opts={{ renderer: "svg" }} />
    </div>
  );
}
