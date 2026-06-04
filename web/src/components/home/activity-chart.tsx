import { useMemo } from "react";
import {
  Area,
  CartesianGrid,
  ComposedChart,
  Line,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { axisLine, axisTick, chartColors, gridStroke } from "@/lib/chart-theme";
import type { ActivityBucket } from "@/lib/types";

function fmtTime(iso: string): string {
  const d = new Date(iso);
  return d.toLocaleTimeString("en-US", {
    hour: "2-digit",
    minute: "2-digit",
    hour12: false,
  });
}

function fmtSpans(v: number): string {
  if (v >= 1000) return `${(v / 1000).toFixed(1)}k`;
  return v.toFixed(0);
}

interface Props {
  buckets: ActivityBucket[];
}

export function ActivityChart({ buckets }: Props) {
  const data = useMemo(
    () =>
      (buckets ?? []).map((b) => ({
        time: fmtTime(b.t),
        spans: b.spans,
        err: b.error_rate * 100,
      })),
    [buckets],
  );

  const c = chartColors();

  if (data.length < 2) {
    return (
      <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
        <div className="detail-label mb-1">System activity</div>
        <div className="text-sm text-muted-foreground">Not enough data yet</div>
      </div>
    );
  }

  const tickInterval = Math.max(0, Math.floor(data.length / 6) - 1);

  return (
    <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
      <div className="mb-1 flex items-center justify-between">
        <div className="detail-label">System activity</div>
        <div className="flex items-center gap-3 font-mono text-[11px] text-muted-foreground">
          <span className="flex items-center gap-1.5">
            <span
              className="inline-block size-2 rounded-sm"
              style={{ background: c.primary }}
            />
            throughput
          </span>
          <span className="flex items-center gap-1.5">
            <span
              className="inline-block size-2 rounded-sm"
              style={{ background: c.destructive }}
            />
            error rate
          </span>
        </div>
      </div>
      <ResponsiveContainer width="100%" height={150}>
        <ComposedChart data={data} margin={{ top: 12, right: 8, bottom: 4, left: 4 }}>
          <defs>
            <linearGradient id="activity-throughput" x1="0" y1="0" x2="0" y2="1">
              <stop offset="0%" stopColor={c.primary} stopOpacity={0.25} />
              <stop offset="100%" stopColor={c.primary} stopOpacity={0} />
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
            yAxisId="tp"
            width={40}
            tick={axisTick(10)}
            tickLine={false}
            axisLine={axisLine()}
            tickFormatter={fmtSpans}
          />
          <YAxis
            yAxisId="err"
            orientation="right"
            width={36}
            tick={axisTick(10)}
            tickLine={false}
            axisLine={axisLine()}
            tickFormatter={(v: number) => `${v.toFixed(0)}%`}
          />
          <Tooltip
            cursor={{ stroke: c.border, strokeDasharray: "3 3" }}
            content={(props) => {
              if (!props.active || !props.payload?.length) return null;
              const sp = props.payload.find((p) => p.dataKey === "spans")?.value;
              const er = props.payload.find((p) => p.dataKey === "err")?.value;
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
                  {typeof sp === "number" && <div>{fmtSpans(sp)} spans/min</div>}
                  {typeof er === "number" && <div>{er.toFixed(2)}% errors</div>}
                </div>
              );
            }}
          />
          <Area
            yAxisId="tp"
            type="monotone"
            dataKey="spans"
            stroke={c.primary}
            strokeWidth={2}
            fill="url(#activity-throughput)"
            isAnimationActive={false}
          />
          <Line
            yAxisId="err"
            type="monotone"
            dataKey="err"
            stroke={c.destructive}
            strokeWidth={2}
            dot={false}
            isAnimationActive={false}
          />
        </ComposedChart>
      </ResponsiveContainer>
    </div>
  );
}
