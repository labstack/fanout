import { useMemo } from "react";
import echarts, { ReactECharts, LinearGradient, tooltipStyle, axisLine, axisLabel, splitLine, cssVar } from "@/lib/echarts";
import type { ServiceBucket, ChangePoint } from "@/lib/types";

interface Props {
  title: string;
  buckets: ServiceBucket[];
  metric: "error_rate" | "p95_ms";
  color: string;
  changePoints?: ChangePoint[];
  baselineValue?: number;
}

function fmtVal(v: number, metric: string): string {
  if (metric === "error_rate") return `${(v * 100).toFixed(1)}%`;
  return v >= 1000 ? `${(v / 1000).toFixed(1)}s` : `${v.toFixed(0)}ms`;
}

function fmtTime(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleTimeString("en-US", { hour: "2-digit", minute: "2-digit", hour12: false });
}

export function ServiceChart({ title, buckets, metric, color, changePoints, baselineValue }: Props) {
  const option = useMemo(() => {
    if (!buckets || buckets.length < 2) return null;

    const times = buckets.map((b) => fmtTime(b.time));
    const values = buckets.map((b) => (metric === "error_rate" ? b.error_rate : b.p95_ms));

    // Mark lines for change points
    const markLineData: Record<string, unknown>[] = [];

    if (changePoints) {
      const metricName = metric === "error_rate" ? "error_rate" : "p95";
      for (const cp of changePoints) {
        if (!cp.metric.includes(metricName)) continue;
        const cpTime = fmtTime(cp.time);
        const ratio = cp.before > 0 ? (cp.after / cp.before).toFixed(1) : "?";
        markLineData.push({
          xAxis: cpTime,
          label: {
            formatter: `${ratio}x`,
            position: "insideStartTop",
            color: cssVar("--primary"),
            fontSize: 10,
            fontFamily: "monospace",
          },
          lineStyle: {
            color: cssVar("--primary"),
            type: "dashed" as const,
            width: 1,
          },
        });
      }
    }

    // Baseline horizontal line
    if (baselineValue !== undefined && baselineValue > 0) {
      markLineData.push({
        yAxis: baselineValue,
        label: {
          formatter: `baseline ${fmtVal(baselineValue, metric)}`,
          position: "insideEndTop",
          color: cssVar("--muted-foreground"),
          fontSize: 9,
          fontFamily: "monospace",
        },
        lineStyle: {
          color,
          type: "dashed" as const,
          width: 0.5,
          opacity: 0.4,
        },
      });
    }

    const yAxisName = metric === "error_rate" ? "Error %" : "ms";

    return {
      animation: false,
      grid: { left: 52, right: 16, top: 16, bottom: 28, containLabel: false },
      tooltip: {
        trigger: "axis",
        ...tooltipStyle(),
        formatter: (params: { value: number; axisValue: string }[]) => {
          if (!Array.isArray(params) || !params[0]) return "";
          const p = params[0];
          return `${p.axisValue}<br/>${fmtVal(p.value, metric)}`;
        },
      },
      xAxis: {
        type: "category" as const,
        data: times,
        axisLine: axisLine(),
        axisLabel: { ...axisLabel(10), interval: Math.max(0, Math.floor(times.length / 6) - 1) },
      },
      yAxis: {
        type: "value" as const,
        name: yAxisName,
        nameTextStyle: { color: cssVar("--muted-foreground"), fontSize: 10 },
        axisLine: axisLine(),
        axisLabel: {
          ...axisLabel(10),
          formatter: (v: number) => fmtVal(v, metric),
        },
        splitLine: splitLine(),
      },
      series: [
        {
          type: "line" as const,
          data: values,
          smooth: true,
          symbol: "none",
          lineStyle: { width: 2, color },
          itemStyle: { color },
          areaStyle: {
            color: new LinearGradient(0, 0, 0, 1, [
              { offset: 0, color: color + "33" },
              { offset: 1, color: color + "00" },
            ]),
          },
          markLine: markLineData.length > 0
            ? { symbol: "none", data: markLineData }
            : undefined,
        },
      ],
    };
  }, [buckets, metric, color, changePoints, baselineValue]);

  if (!option) {
    return (
      <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
        <div className="detail-label mb-2">{title}</div>
        <div className="text-sm text-muted-foreground">No data</div>
      </div>
    );
  }

  return (
    <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
      <div className="detail-label mb-1">{title}</div>
      <ReactECharts echarts={echarts} option={option} style={{ height: 200 }} opts={{ renderer: "svg" }} />
    </div>
  );
}
