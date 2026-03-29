import { useMemo } from "react";
import echarts, { ReactECharts, cssVar } from "@/lib/echarts";
import type { TimeseriesBlockData } from "@/lib/types";

const DEFAULT_COLORS = ["#8884d8", "#82ca9d", "#ffc658", "#ff7f50", "#00bcd4"];

export function TimeseriesBlock({ data }: { data: TimeseriesBlockData }) {
  const option = useMemo(() => ({
    animation: false,
    grid: { left: 56, right: 16, top: 32, bottom: 32, containLabel: false },
    tooltip: {
      trigger: "axis",
      backgroundColor: cssVar("--popover"),
      borderColor: cssVar("--border"),
      textStyle: { color: cssVar("--popover-foreground"), fontSize: 12 },
    },
    legend: {
      data: data.series.map((s) => s.label),
      textStyle: { color: cssVar("--foreground"), fontSize: 12 },
      top: 0,
    },
    xAxis: {
      type: "category" as const,
      data: data.labels,
      axisLine: { lineStyle: { color: cssVar("--border") } },
      axisLabel: { color: cssVar("--foreground"), fontSize: 12 },
    },
    yAxis: {
      type: "value" as const,
      name: data.yLabel,
      nameTextStyle: { color: cssVar("--muted-foreground"), fontSize: 12 },
      axisLine: { lineStyle: { color: cssVar("--border") } },
      axisLabel: { color: cssVar("--foreground"), fontSize: 12 },
      splitLine: { lineStyle: { color: cssVar("--border"), type: "dashed" as const } },
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
