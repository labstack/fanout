import { useMemo } from "react";
import {
  Area,
  AreaChart,
  CartesianGrid,
  ReferenceLine,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { axisLine, axisTick, chartColors, gridStroke } from "@/lib/chart-theme";
import type { ChangePoint, ServiceBucket } from "@/lib/types";

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
  const data = useMemo(() => {
    if (!buckets || buckets.length < 2) return [];
    return buckets.map((b) => ({
      time: fmtTime(b.time),
      value: metric === "error_rate" ? b.error_rate : b.p95_ms,
    }));
  }, [buckets, metric]);

  const cpMarkers = useMemo(() => {
    if (!changePoints) return [];
    const metricName = metric === "error_rate" ? "error_rate" : "p95";
    return changePoints
      .filter((cp) => cp.metric.includes(metricName))
      .map((cp) => ({
        x: fmtTime(cp.time),
        ratio: cp.before > 0 ? (cp.after / cp.before).toFixed(1) : "?",
      }));
  }, [changePoints, metric]);

  if (data.length === 0) {
    return (
      <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
        <div className="detail-label mb-2">{title}</div>
        <div className="text-sm text-muted-foreground">No data</div>
      </div>
    );
  }

  const gradId = `service-chart-${metric}-${color.replace(/[^a-zA-Z0-9]/g, "")}`;
  const tickInterval = Math.max(0, Math.floor(data.length / 6) - 1);
  const yAxisName = metric === "error_rate" ? "Error %" : "ms";

  return (
    <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
      <div className="detail-label mb-1">{title}</div>
      <ResponsiveContainer width="100%" height={200}>
        <AreaChart data={data} margin={{ top: 16, right: 16, bottom: 4, left: 4 }}>
          <defs>
            <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={color} stopOpacity={0.2} />
              <stop offset="100%" stopColor={color} stopOpacity={0} />
            </linearGradient>
          </defs>
          <CartesianGrid stroke={gridStroke()} strokeDasharray="3 3" vertical={false} />
          <XAxis
            dataKey="time"
            interval={tickInterval}
            tick={axisTick(10)}
            tickLine={false}
            axisLine={axisLine()}
          />
          <YAxis
            width={52}
            label={{
              value: yAxisName,
              angle: -90,
              position: "insideLeft",
              style: { fill: chartColors().mutedForeground, fontSize: 10 },
            }}
            tick={axisTick(10)}
            tickLine={false}
            axisLine={axisLine()}
            tickFormatter={(v: number) => fmtVal(v, metric)}
          />
          <Tooltip
            cursor={{ stroke: chartColors().border, strokeDasharray: "3 3" }}
            content={(props) => {
              const raw = props.payload?.[0]?.value;
              const v = typeof raw === "number" ? raw : Number(raw);
              if (!props.active || !Number.isFinite(v)) return null;
              const c = chartColors();
              return (
                <div
                  style={{
                    background: c.popover,
                    border: `1px solid ${c.border}`,
                    color: c.popoverForeground,
                    fontSize: 12,
                    padding: "6px 8px",
                    borderRadius: 4,
                  }}
                >
                  <div>{props.label}</div>
                  <div>{fmtVal(v, metric)}</div>
                </div>
              );
            }}
          />
          {baselineValue !== undefined && baselineValue > 0 && (
            <ReferenceLine
              y={baselineValue}
              stroke={color}
              strokeOpacity={0.4}
              strokeDasharray="3 3"
              strokeWidth={0.5}
              label={{
                value: `baseline ${fmtVal(baselineValue, metric)}`,
                position: "insideTopRight",
                fill: chartColors().mutedForeground,
                fontSize: 9,
                fontFamily: "monospace",
              }}
            />
          )}
          {cpMarkers.map((cp) => (
            <ReferenceLine
              key={`${cp.x}-${cp.ratio}`}
              x={cp.x}
              stroke={chartColors().primary}
              strokeDasharray="3 3"
              strokeWidth={1}
              label={{
                value: `${cp.ratio}x`,
                position: "insideTopLeft",
                fill: chartColors().primary,
                fontSize: 10,
                fontFamily: "monospace",
              }}
            />
          ))}
          <Area
            type="monotone"
            dataKey="value"
            stroke={color}
            strokeWidth={2}
            fill={`url(#${gradId})`}
            isAnimationActive={false}
          />
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}
