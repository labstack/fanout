import { useMemo, type ReactElement } from "react";
import {
  CartesianGrid,
  ResponsiveContainer,
  Scatter,
  ScatterChart,
  Tooltip,
  XAxis,
  YAxis,
  ZAxis,
} from "recharts";
import { axisLine, axisTick, chartColors, cssVar, tooltipBox } from "@/lib/chart-theme";
import type { HeatmapBlockData } from "@/lib/types";

interface Cell {
  ti: number;
  bi: number;
  v: number;
  time: string;
  bucket: string;
}

function hexToRgb(hex: string): [number, number, number] {
  const clean = hex.replace("#", "");
  return [parseInt(clean.slice(0, 2), 16), parseInt(clean.slice(2, 4), 16), parseInt(clean.slice(4, 6), 16)];
}

function lerp(a: number, b: number, t: number): number {
  return a + (b - a) * t;
}

function interpolate3(t: number, c0: string, c1: string, c2: string): string {
  const [r0, g0, b0] = hexToRgb(c0);
  const [r1, g1, b1] = hexToRgb(c1);
  const [r2, g2, b2] = hexToRgb(c2);
  if (t < 0.5) {
    const k = t * 2;
    return `rgb(${lerp(r0, r1, k) | 0}, ${lerp(g0, g1, k) | 0}, ${lerp(b0, b1, k) | 0})`;
  }
  const k = (t - 0.5) * 2;
  return `rgb(${lerp(r1, r2, k) | 0}, ${lerp(g1, g2, k) | 0}, ${lerp(b1, b2, k) | 0})`;
}

export function HeatmapBlock({ data }: { data: HeatmapBlockData }) {
  const c = chartColors();

  const { cells, vMin, vMax } = useMemo(() => {
    const out: Cell[] = [];
    let mn = Infinity;
    let mx = -Infinity;
    for (let ti = 0; ti < data.values.length; ti++) {
      for (let bi = 0; bi < data.values[ti].length; bi++) {
        const v = data.values[ti][bi];
        out.push({
          ti,
          bi,
          v,
          time: data.times[ti] ?? "",
          bucket: String(data.buckets[bi] ?? ""),
        });
        if (v < mn) mn = v;
        if (v > mx) mx = v;
      }
    }
    if (!Number.isFinite(mn)) mn = 0;
    if (!Number.isFinite(mx)) mx = 1;
    return { cells: out, vMin: mn, vMax: mx };
  }, [data]);

  if (data.times.length === 0 || data.buckets.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-muted/50 p-4 text-sm text-muted-foreground">
        No heatmap data to display.
      </div>
    );
  }

  const height = Math.max(200, data.buckets.length * 24 + 48);
  const low = c.popover;
  const mid = cssVar("--accent") || c.primary;
  const high = c.destructive;

  return (
    <div className="block-card">
      <h3 className="block-title">{data.title}</h3>
      <ResponsiveContainer width="100%" height={height}>
        <ScatterChart margin={{ top: 8, right: 16, bottom: 8, left: 16 }}>
          <CartesianGrid stroke="transparent" />
          <XAxis
            type="number"
            dataKey="ti"
            domain={[-0.5, data.times.length - 0.5]}
            ticks={data.times.map((_, i) => i).filter((i) => i % Math.max(1, Math.ceil(data.times.length / 10)) === 0)}
            tickFormatter={(i: number) => data.times[i] ?? ""}
            tick={axisTick(10)}
            tickLine={false}
            axisLine={axisLine()}
            interval={0}
          />
          <YAxis
            type="number"
            dataKey="bi"
            domain={[-0.5, data.buckets.length - 0.5]}
            ticks={data.buckets.map((_, i) => i)}
            tickFormatter={(i: number) => String(data.buckets[i] ?? "")}
            tick={axisTick(10)}
            tickLine={false}
            axisLine={axisLine()}
            interval={0}
            width={60}
          />
          <ZAxis range={[0, 0]} />
          <Tooltip
            cursor={{ fill: c.border, fillOpacity: 0.1 }}
            content={(props) => {
              const cell = props.payload?.[0]?.payload as Cell | undefined;
              if (!props.active || !cell) return null;
              return (
                <div style={tooltipBox(c)}>
                  <div>{cell.time}</div>
                  <div>
                    {cell.bucket}: <b>{cell.v}</b>
                  </div>
                </div>
              );
            }}
          />
          <Scatter
            data={cells}
            isAnimationActive={false}
            shape={(props: unknown): ReactElement<SVGElement> => {
              const p = props as { cx?: number; cy?: number; xAxis?: { width: number; scale: (n: number) => number }; yAxis?: { height: number; scale: (n: number) => number }; payload?: Cell };
              const cell = p.payload;
              if (!cell || p.cx === undefined || p.cy === undefined || !p.xAxis || !p.yAxis) {
                return <g />;
              }
              const cellW = Math.abs(p.xAxis.scale(1) - p.xAxis.scale(0));
              const cellH = Math.abs(p.yAxis.scale(1) - p.yAxis.scale(0));
              const t = vMax > vMin ? (cell.v - vMin) / (vMax - vMin) : 0;
              const fill = interpolate3(t, low, mid, high);
              return (
                <rect
                  x={p.cx - cellW / 2}
                  y={p.cy - cellH / 2}
                  width={cellW}
                  height={cellH}
                  fill={fill}
                  rx={1}
                />
              );
            }}
          />
        </ScatterChart>
      </ResponsiveContainer>
    </div>
  );
}
