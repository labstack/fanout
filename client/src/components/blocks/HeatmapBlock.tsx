import { useMemo } from "react";
import echarts, { ReactECharts, cssVar } from "@/lib/echarts";
import type { HeatmapBlockData } from "@/lib/types";

export function HeatmapBlock({ data }: { data: HeatmapBlockData }) {
  const option = useMemo(() => {
    // Flatten 2D values to [timeIdx, bucketIdx, value] triples
    const flatData: [number, number, number][] = [];
    let vMin = Infinity, vMax = -Infinity;
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
        backgroundColor: cssVar("--popover"),
        borderColor: cssVar("--border"),
        textStyle: { color: cssVar("--popover-foreground"), fontSize: 12 },
        formatter: (params: any) => {
          const [ti, bi, v] = params.data;
          return `${data.times[ti]}<br/>${data.buckets[bi]}: <b>${v}</b>`;
        },
      },
      xAxis: {
        type: "category" as const,
        data: data.times,
        axisLine: { lineStyle: { color: cssVar("--border") } },
        axisLabel: { color: cssVar("--foreground"), fontSize: 10, interval: Math.max(0, Math.ceil(data.times.length / 10) - 1) },
      },
      yAxis: {
        type: "category" as const,
        data: data.buckets.map(String),
        axisLine: { lineStyle: { color: cssVar("--border") } },
        axisLabel: { color: cssVar("--foreground"), fontSize: 10 },
      },
      visualMap: {
        min: vMin,
        max: vMax,
        show: false,
        inRange: { color: ["#ffffb2", "#fd8d3c", "#bd0026"] },
      },
      series: [{
        type: "heatmap" as const,
        data: flatData,
        itemStyle: { borderRadius: 1 },
      }],
    };
  }, [data]);

  return (
    <div>
      <h3 className="mb-2 text-sm font-semibold text-foreground">{data.title}</h3>
      <ReactECharts echarts={echarts} option={option} style={{ height: Math.max(200, data.buckets.length * 24 + 48) }} opts={{ renderer: "svg" }} />
    </div>
  );
}
