import { useMemo } from "react";
import echarts, { ReactECharts, LinearGradient, tooltipStyle, axisLine, axisLabel, splitLine, cssVar } from "@/lib/echarts";
import type { TimeseriesBlockData } from "@/lib/types";
import { COLORS } from "@/lib/theme";

const DEFAULT_COLORS = [COLORS.accent, COLORS.healthy, COLORS.degraded, "#fb923c", "#a78bfa"];

/** Parse hex color to [r,g,b] or return null */
function hexToRgb(hex: string): [number, number, number] | null {
  const m = /^#?([0-9a-f]{2})([0-9a-f]{2})([0-9a-f]{2})$/i.exec(hex);
  if (!m) return null;
  return [parseInt(m[1], 16), parseInt(m[2], 16), parseInt(m[3], 16)];
}

/** Build a LinearGradient area fill for a series color */
function areaGradient(color: string) {
  const rgb = hexToRgb(color);
  if (!rgb) {
    // Fallback using hex + alpha suffix
    return new LinearGradient(0, 0, 0, 1, [
      { offset: 0, color: color + "33" },
      { offset: 1, color: color + "00" },
    ]);
  }
  return new LinearGradient(0, 0, 0, 1, [
    { offset: 0, color: `rgba(${rgb[0]},${rgb[1]},${rgb[2]},0.2)` },
    { offset: 1, color: `rgba(${rgb[0]},${rgb[1]},${rgb[2]},0)` },
  ]);
}

export function TimeseriesBlock({ data }: { data: TimeseriesBlockData }) {
  const option = useMemo(() => ({
    animation: false,
    grid: { left: 56, right: 16, top: 32, bottom: 32, containLabel: false },
    tooltip: { trigger: "axis", ...tooltipStyle() },
    legend: {
      data: data.series.map((s) => s.label),
      textStyle: { color: cssVar("--foreground"), fontSize: 12 },
      top: 0,
    },
    xAxis: {
      type: "category" as const,
      data: data.labels,
      axisLine: axisLine(),
      axisLabel: axisLabel(),
    },
    yAxis: {
      type: "value" as const,
      name: data.yLabel,
      nameTextStyle: { color: cssVar("--muted-foreground"), fontSize: 12 },
      axisLine: axisLine(),
      axisLabel: axisLabel(),
      splitLine: splitLine(),
    },
    series: data.series.map((s, i) => {
      const color = s.color ?? DEFAULT_COLORS[i % DEFAULT_COLORS.length];
      return {
        name: s.label,
        type: "line" as const,
        data: s.values,
        smooth: true,
        symbol: "none",
        lineStyle: { width: 2 },
        itemStyle: { color },
        areaStyle: {
          color: areaGradient(color),
        },
      };
    }),
  }), [data]);

  return (
    <div className="block-card">
      <h3 className="block-title">{data.title}</h3>
      <ReactECharts echarts={echarts} option={option} style={{ height: 300 }} opts={{ renderer: "svg" }} />
    </div>
  );
}
