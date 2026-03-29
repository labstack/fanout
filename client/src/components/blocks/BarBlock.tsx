import { useMemo } from "react";
import echarts, { ReactECharts, cssVar } from "@/lib/echarts";
import type { BarBlockData } from "@/lib/types";

const DEFAULT_COLOR = "#8884d8";

export function BarBlock({ data }: { data: BarBlockData }) {
  const option = useMemo(() => {
    const categories = data.bars.map((b) => b.label);
    const values = data.bars.map((b) => ({
      value: b.value,
      itemStyle: b.color ? { color: b.color } : undefined,
    }));

    const categoryAxis = {
      type: "category" as const,
      data: categories,
      axisLine: { lineStyle: { color: cssVar("--border") } },
      axisLabel: { color: cssVar("--foreground"), fontSize: 12 },
    };
    const valueAxis = {
      type: "value" as const,
      name: data.yLabel,
      nameTextStyle: { color: cssVar("--muted-foreground"), fontSize: 12 },
      axisLine: { lineStyle: { color: cssVar("--border") } },
      axisLabel: { color: cssVar("--foreground"), fontSize: 12 },
      splitLine: { lineStyle: { color: cssVar("--border"), type: "dashed" as const } },
    };

    return {
      animation: false,
      grid: { left: data.horizontal ? 110 : 56, right: 16, top: 16, bottom: 32, containLabel: false },
      tooltip: {
        trigger: "axis" as const,
        backgroundColor: cssVar("--popover"),
        borderColor: cssVar("--border"),
        textStyle: { color: cssVar("--popover-foreground"), fontSize: 12 },
      },
      xAxis: data.horizontal ? valueAxis : categoryAxis,
      yAxis: data.horizontal ? categoryAxis : valueAxis,
      series: [{
        type: "bar" as const,
        data: values,
        itemStyle: { color: DEFAULT_COLOR, borderRadius: data.horizontal ? [0, 4, 4, 0] : [4, 4, 0, 0] },
      }],
    };
  }, [data]);

  return (
    <div>
      <h3 className="mb-2 text-sm font-semibold text-foreground">{data.title}</h3>
      <ReactECharts echarts={echarts} option={option} style={{ height: 300 }} opts={{ renderer: "svg" }} />
    </div>
  );
}
