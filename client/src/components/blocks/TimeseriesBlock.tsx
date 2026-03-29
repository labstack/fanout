import { useMemo } from "react";
import echarts, { ReactECharts, tooltipStyle, axisLine, axisLabel, splitLine, cssVar } from "@/lib/echarts";
import type { TimeseriesBlockData } from "@/lib/types";

const DEFAULT_COLORS = ["#8884d8", "#82ca9d", "#ffc658", "#ff7f50", "#00bcd4"];

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
    series: data.series.map((s, i) => ({
      name: s.label,
      type: "line" as const,
      data: s.values,
      smooth: true,
      symbol: "none",
      lineStyle: { width: 2 },
      itemStyle: { color: s.color ?? DEFAULT_COLORS[i % DEFAULT_COLORS.length] },
    })),
  }), [data]);

  return (
    <div>
      <h3 className="mb-2 text-sm font-semibold text-foreground">{data.title}</h3>
      <ReactECharts echarts={echarts} option={option} style={{ height: 300 }} opts={{ renderer: "svg" }} />
    </div>
  );
}
