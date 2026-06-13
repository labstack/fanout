import {
  Bar,
  BarChart,
  CartesianGrid,
  Cell,
  ResponsiveContainer,
  Tooltip,
  XAxis,
  YAxis,
} from "recharts";
import { axisLine, axisTick, chartColors, gridStroke, tooltipBox } from "@/lib/chart-theme";
import type { BarBlockData } from "@/lib/types";

export function BarBlock({ data }: { data: BarBlockData }) {
  const c = chartColors();
  const horizontal = data.horizontal === true;
  const radius: [number, number, number, number] = horizontal ? [0, 4, 4, 0] : [4, 4, 0, 0];

  const items = data.bars.map((b) => ({
    label: b.label,
    value: b.value,
    fill: b.color ?? c.primary,
  }));

  return (
    <div className="block-card">
      <h3 className="block-title">{data.title}</h3>
      <ResponsiveContainer width="100%" height={300}>
        <BarChart
          data={items}
          layout={horizontal ? "vertical" : "horizontal"}
          margin={{ top: 16, right: 16, bottom: 8, left: horizontal ? 80 : 4 }}
        >
          <CartesianGrid stroke={gridStroke()} strokeDasharray="3 3" vertical={false} />
          {horizontal ? (
            <>
              <XAxis
                type="number"
                tick={axisTick()}
                tickLine={false}
                axisLine={axisLine()}
                label={{
                  value: data.yLabel,
                  position: "insideBottom",
                  offset: -4,
                  style: { fill: c.mutedForeground, fontSize: 12 },
                }}
              />
              <YAxis
                type="category"
                dataKey="label"
                width={100}
                tick={axisTick()}
                tickLine={false}
                axisLine={axisLine()}
              />
            </>
          ) : (
            <>
              <XAxis
                type="category"
                dataKey="label"
                tick={axisTick()}
                tickLine={false}
                axisLine={axisLine()}
              />
              <YAxis
                type="number"
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
            </>
          )}
          <Tooltip
            cursor={{ fill: c.border, fillOpacity: 0.2 }}
            content={(props) => {
              const item = props.payload?.[0];
              if (!props.active || !item) return null;
              return (
                <div style={tooltipBox(c)}>
                  <div>{props.label}</div>
                  <div>{item.value}</div>
                </div>
              );
            }}
          />
          <Bar dataKey="value" radius={radius} isAnimationActive={false}>
            {items.map((item) => (
              <Cell key={item.label} fill={item.fill} />
            ))}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}
