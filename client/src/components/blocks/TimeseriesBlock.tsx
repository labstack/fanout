import {
  LineChart,
  Line,
  XAxis,
  YAxis,
  Tooltip,
  Legend,
  ResponsiveContainer,
  CartesianGrid,
} from "recharts";
import type { TimeseriesBlockData } from "@/lib/types";

const DEFAULT_COLORS = ["#8884d8", "#82ca9d", "#ffc658", "#ff7f50", "#00bcd4"];

export function TimeseriesBlock({ data }: { data: TimeseriesBlockData }) {
  const chartData = data.labels.map((label, i) => {
    const point: Record<string, unknown> = { time: label };
    data.series.forEach((s) => {
      point[s.label] = s.values[i];
    });
    return point;
  });

  return (
    <div>
      <h3 className="mb-2 text-sm font-semibold text-foreground">
        {data.title}
      </h3>
      <ResponsiveContainer width="100%" height={300}>
        <LineChart data={chartData}>
          <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" />
          <XAxis
            dataKey="time"
            tick={{ fill: "hsl(var(--foreground))", fontSize: 12 }}
            stroke="hsl(var(--border))"
          />
          <YAxis
            label={
              data.yLabel
                ? {
                    value: data.yLabel,
                    angle: -90,
                    position: "insideLeft",
                    fill: "hsl(var(--muted-foreground))",
                    fontSize: 12,
                  }
                : undefined
            }
            tick={{ fill: "hsl(var(--foreground))", fontSize: 12 }}
            stroke="hsl(var(--border))"
          />
          <Tooltip
            contentStyle={{
              backgroundColor: "hsl(var(--popover))",
              border: "1px solid hsl(var(--border))",
              borderRadius: "6px",
              color: "hsl(var(--popover-foreground))",
            }}
          />
          <Legend
            wrapperStyle={{ color: "hsl(var(--foreground))", fontSize: 12 }}
          />
          {data.series.map((s, i) => (
            <Line
              key={s.label}
              type="monotone"
              dataKey={s.label}
              stroke={s.color ?? DEFAULT_COLORS[i % DEFAULT_COLORS.length]}
              strokeWidth={2}
              dot={false}
              activeDot={{ r: 4 }}
            />
          ))}
        </LineChart>
      </ResponsiveContainer>
    </div>
  );
}
