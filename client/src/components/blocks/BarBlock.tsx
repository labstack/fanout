import {
  BarChart,
  Bar,
  XAxis,
  YAxis,
  Tooltip,
  ResponsiveContainer,
  CartesianGrid,
  Cell,
} from "recharts";
import type { BarBlockData } from "@/lib/types";

const DEFAULT_COLOR = "#8884d8";

export function BarBlock({ data }: { data: BarBlockData }) {
  const chartData = data.bars.map((b) => ({
    label: b.label,
    value: b.value,
  }));

  const hasIndividualColors = data.bars.some((b) => b.color);

  return (
    <div>
      <h3 className="mb-2 text-sm font-semibold text-foreground">
        {data.title}
      </h3>
      <ResponsiveContainer width="100%" height={300}>
        <BarChart
          data={chartData}
          layout={data.horizontal ? "vertical" : "horizontal"}
        >
          <CartesianGrid strokeDasharray="3 3" stroke="hsl(var(--border))" />
          {data.horizontal ? (
            <>
              <XAxis
                type="number"
                tick={{ fill: "hsl(var(--foreground))", fontSize: 12 }}
                stroke="hsl(var(--border))"
              />
              <YAxis
                type="category"
                dataKey="label"
                tick={{ fill: "hsl(var(--foreground))", fontSize: 12 }}
                stroke="hsl(var(--border))"
                width={100}
              />
            </>
          ) : (
            <>
              <XAxis
                dataKey="label"
                tick={{ fill: "hsl(var(--foreground))", fontSize: 12 }}
                stroke="hsl(var(--border))"
              />
              <YAxis
                tick={{ fill: "hsl(var(--foreground))", fontSize: 12 }}
                stroke="hsl(var(--border))"
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
              />
            </>
          )}
          <Tooltip
            contentStyle={{
              backgroundColor: "hsl(var(--popover))",
              border: "1px solid hsl(var(--border))",
              borderRadius: "6px",
              color: "hsl(var(--popover-foreground))",
            }}
          />
          <Bar dataKey="value" fill={DEFAULT_COLOR} radius={[4, 4, 0, 0]}>
            {hasIndividualColors &&
              data.bars.map((b, i) => (
                <Cell key={i} fill={b.color ?? DEFAULT_COLOR} />
              ))}
          </Bar>
        </BarChart>
      </ResponsiveContainer>
    </div>
  );
}
