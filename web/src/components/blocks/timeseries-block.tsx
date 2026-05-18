import { useMemo } from "react";
import {
  Area,
  AreaChart,
  CartesianGrid,
  Legend,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import {
  axisLine,
  axisTick,
  chartColors,
  gridStroke,
  seriesPalette,
  tooltipBox,
} from "@/lib/chart-theme";
import type { TimeseriesBlockData } from "@/lib/types";

export function TimeseriesBlock({ data }: { data: TimeseriesBlockData }) {
  const c = chartColors();
  const palette = seriesPalette();

  const rows = useMemo(() => {
    return data.labels.map((label, i) => {
      const row: Record<string, number | string> = { label };
      for (const s of data.series) {
        row[s.label] = s.values[i] ?? 0;
      }
      return row;
    });
  }, [data]);

  const seriesMeta = data.series.map((s, i) => ({
    label: s.label,
    color: s.color ?? palette[i % palette.length],
    gradId: `ts-${s.label.replace(/[^a-zA-Z0-9]/g, "")}-${i}`,
  }));

  return (
    <div className="block-card">
      <h3 className="block-title">{data.title}</h3>
      <ResponsiveContainer width="100%" height={300}>
        <AreaChart data={rows} margin={{ top: 32, right: 16, bottom: 8, left: 4 }}>
          <defs>
            {seriesMeta.map((s) => (
              <linearGradient key={s.gradId} id={s.gradId} x1="0" y1="0" x2="0" y2="1">
                <stop offset="0%" stopColor={s.color} stopOpacity={0.2} />
                <stop offset="100%" stopColor={s.color} stopOpacity={0} />
              </linearGradient>
            ))}
          </defs>
          <CartesianGrid stroke={gridStroke()} strokeDasharray="3 3" vertical={false} />
          <XAxis dataKey="label" tick={axisTick()} tickLine={false} axisLine={axisLine()} />
          <YAxis
            tick={axisTick()}
            tickLine={false}
            axisLine={axisLine()}
            label={{
              value: data.yLabel,
              angle: -90,
              position: "insideLeft",
              style: { fill: c.mutedForeground, fontSize: 12 },
            }}
          />
          <Tooltip
            cursor={{ stroke: c.border, strokeDasharray: "3 3" }}
            content={(props) => {
              if (!props.active || !props.payload?.length) return null;
              return (
                <div style={tooltipBox(c)}>
                  <div style={{ fontWeight: 500 }}>{props.label}</div>
                  {props.payload.map((p) => (
                    <div key={String(p.dataKey)} style={{ color: p.color }}>
                      {p.name}: {p.value}
                    </div>
                  ))}
                </div>
              );
            }}
          />
          <Legend
            verticalAlign="top"
            align="center"
            wrapperStyle={{ fontSize: 12, color: c.foreground }}
          />
          {seriesMeta.map((s) => (
            <Area
              key={s.label}
              type="monotone"
              dataKey={s.label}
              stroke={s.color}
              strokeWidth={2}
              fill={`url(#${s.gradId})`}
              isAnimationActive={false}
            />
          ))}
        </AreaChart>
      </ResponsiveContainer>
    </div>
  );
}
