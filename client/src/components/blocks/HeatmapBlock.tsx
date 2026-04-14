import { useMemo } from "react";
import echarts, { ReactECharts, tooltipStyle, axisLine, axisLabel, esc } from "@/lib/echarts";
import type { HeatmapBlockData } from "@/lib/types";
import { COLORS } from "@/lib/theme";

export function HeatmapBlock({ data }: { data: HeatmapBlockData }) {
  const option = useMemo(() => {
    const flatData: [number, number, number][] = [];
    let vMin = Infinity;
    let vMax = -Infinity;
    for (let ti = 0; ti < data.values.length; ti++) {
      for (let bi = 0; bi < data.values[ti].length; bi++) {
        const v = data.values[ti][bi];
        flatData.push([ti, bi, v]);
        if (v < vMin) vMin = v;
        if (v > vMax) vMax = v;
      }
    }
    if (!isFinite(vMin)) vMin = 0;
    if (!isFinite(vMax)) vMax = 1;

    return {
      animation: false,
      grid: { left: 60, right: 16, top: 8, bottom: 40 },
      tooltip: {
        ...tooltipStyle(),
        formatter: (params: any) => {
          const [ti, bi, v] = params.data;
          return `${esc(data.times[ti])}<br/>${esc(data.buckets[bi])}: <b>${esc(v)}</b>`;
        },
      },
      xAxis: {
        type: "category" as const,
        data: data.times,
        axisLine: axisLine(),
        axisLabel: { ...axisLabel(10), interval: Math.max(0, Math.ceil(data.times.length / 10) - 1) },
      },
      yAxis: {
        type: "category" as const,
        data: data.buckets.map(String),
        axisLine: axisLine(),
        axisLabel: axisLabel(10),
      },
      visualMap: {
        min: vMin,
        max: vMax,
        show: false,
        inRange: { color: [COLORS.surface2, COLORS.accent, COLORS.unhealthy] },
      },
      series: [{
        type: "heatmap" as const,
        data: flatData,
        itemStyle: { borderRadius: 1 },
      }],
    };
  }, [data]);

  return (
    <div className="block-card">
      <h3 className="block-title">{data.title}</h3>
      <ReactECharts echarts={echarts} option={option} style={{ height: Math.max(200, data.buckets.length * 24 + 48) }} opts={{ renderer: "svg" }} />
    </div>
  );
}
