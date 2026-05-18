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
import { axisLine, axisTick, chartColors, gridStroke, tooltipBox } from "@/lib/chart-theme";
import type { CorrelationData } from "@/lib/types";

const PANEL_H = 100;

export function CorrelationBlock({ data }: { data: CorrelationData }) {
  const c = chartColors();

  if (data.panels.length === 0 || data.times.length < 2) {
    return (
      <div className="rounded-lg border border-border bg-muted/50 p-4 text-sm text-muted-foreground">
        No correlation data to display.
      </div>
    );
  }

  return (
    <div className="block-card">
      <h3 className="block-title">Correlation</h3>
      <div className="flex flex-col gap-4">
        {data.panels.map((panel, i) => {
          const rows = data.times.map((t, ti) => ({ t, v: panel.values[ti] ?? 0 }));
          const isLast = i === data.panels.length - 1;
          const gradId = `corr-${i}`;
          return (
            <ResponsiveContainer key={i} width="100%" height={PANEL_H}>
              <AreaChart data={rows} margin={{ top: 4, right: 12, bottom: isLast ? 16 : 0, left: 44 }}>
                <defs>
                  <linearGradient id={gradId} x1="0" y1="0" x2="0" y2="1">
                    <stop offset="0%" stopColor={panel.color} stopOpacity={0.2} />
                    <stop offset="100%" stopColor={panel.color} stopOpacity={0} />
                  </linearGradient>
                </defs>
                <CartesianGrid stroke={gridStroke()} strokeDasharray="3 3" vertical={false} />
                <XAxis
                  dataKey="t"
                  tick={isLast ? { ...axisTick(9), fill: c.mutedForeground } : false}
                  tickLine={false}
                  axisLine={axisLine()}
                  height={isLast ? 16 : 1}
                />
                <YAxis
                  tick={{ ...axisTick(8), fill: c.mutedForeground }}
                  tickLine={false}
                  axisLine={axisLine()}
                  width={44}
                  label={{
                    value: panel.label,
                    angle: -90,
                    position: "insideLeft",
                    style: { fill: c.foreground, fontSize: 9 },
                  }}
                />
                <Tooltip
                  cursor={{ stroke: c.border, strokeDasharray: "3 3" }}
                  content={(props) => {
                    const raw = props.payload?.[0]?.value;
                    if (!props.active || typeof raw !== "number") return null;
                    return (
                      <div style={tooltipBox(c)}>
                        <div>{props.label}</div>
                        <div>
                          {panel.label}: <b>{raw}</b>
                        </div>
                      </div>
                    );
                  }}
                />
                {panel.baseline !== undefined && (
                  <ReferenceLine
                    y={panel.baseline}
                    stroke={panel.color}
                    strokeDasharray="3 3"
                    strokeWidth={0.5}
                    strokeOpacity={0.4}
                  />
                )}
                {panel.markers?.map((m, mi) => {
                  const markerColor = m.severity === "critical" ? c.destructive : c.warning;
                  const label = m.label.length > 12 ? m.label.slice(0, 11) + "…" : m.label;
                  return (
                    <ReferenceLine
                      key={`m-${mi}`}
                      x={m.t}
                      stroke={markerColor}
                      strokeDasharray="3 3"
                      strokeWidth={0.75}
                      strokeOpacity={0.5}
                      label={{
                        value: label,
                        position: "insideTopLeft",
                        fill: markerColor,
                        fontSize: 8,
                        fontFamily: "monospace",
                      }}
                    />
                  );
                })}
                <Area
                  type="monotone"
                  dataKey="v"
                  stroke={panel.color}
                  strokeWidth={1.5}
                  fill={`url(#${gradId})`}
                  isAnimationActive={false}
                />
              </AreaChart>
            </ResponsiveContainer>
          );
        })}
      </div>
    </div>
  );
}
