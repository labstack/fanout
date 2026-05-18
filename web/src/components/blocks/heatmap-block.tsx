import { useMemo, useState } from "react";
import {
  ResponsiveContainer,
  ScatterChart,
  XAxis,
  YAxis,
  useChartHeight,
  useChartWidth,
  useMargin,
} from "recharts";
import { axisLine, axisTick, chartColors, cssVar, tooltipBox } from "@/lib/chart-theme";
import type { HeatmapBlockData } from "@/lib/types";

interface Hover {
  ti: number;
  bi: number;
  v: number;
}

function hexToRgb(hex: string): [number, number, number] {
  const clean = hex.replace("#", "");
  return [parseInt(clean.slice(0, 2), 16), parseInt(clean.slice(2, 4), 16), parseInt(clean.slice(4, 6), 16)];
}

function lerp(a: number, b: number, t: number): number {
  return a + (b - a) * t;
}

function clamp01(t: number): number {
  if (!Number.isFinite(t)) return 0;
  return t < 0 ? 0 : t > 1 ? 1 : t;
}

function interpolate3(t: number, c0: string, c1: string, c2: string): string {
  const k01 = clamp01(t);
  const [r0, g0, b0] = hexToRgb(c0);
  const [r1, g1, b1] = hexToRgb(c1);
  const [r2, g2, b2] = hexToRgb(c2);
  if (k01 < 0.5) {
    const k = k01 * 2;
    return `rgb(${lerp(r0, r1, k) | 0}, ${lerp(g0, g1, k) | 0}, ${lerp(b0, b1, k) | 0})`;
  }
  const k = (k01 - 0.5) * 2;
  return `rgb(${lerp(r1, r2, k) | 0}, ${lerp(g1, g2, k) | 0}, ${lerp(b1, b2, k) | 0})`;
}

function HeatmapCells({
  data,
  vMin,
  vMax,
  low,
  mid,
  high,
  onHover,
}: {
  data: HeatmapBlockData;
  vMin: number;
  vMax: number;
  low: string;
  mid: string;
  high: string;
  onHover: (h: Hover | null) => void;
}) {
  const chartW = useChartWidth() ?? 0;
  const chartH = useChartHeight() ?? 0;
  const margin = (useMargin() ?? {}) as { left?: number; right?: number; top?: number; bottom?: number };
  const left = margin.left ?? 0;
  const right = margin.right ?? 0;
  const top = margin.top ?? 0;
  const bottom = margin.bottom ?? 0;

  const innerW = Math.max(0, chartW - left - right);
  const innerH = Math.max(0, chartH - top - bottom);
  const nT = data.times.length;
  const nB = data.buckets.length;
  if (nT === 0 || nB === 0 || innerW === 0 || innerH === 0) return null;

  const cellW = innerW / nT;
  const cellH = innerH / nB;
  const range = vMax > vMin ? vMax - vMin : 1;

  return (
    <g>
      {data.values.map((row, ti) =>
        row.map((v, bi) => {
          const t = clamp01((v - vMin) / range);
          const fill = interpolate3(t, low, mid, high);
          const x = left + ti * cellW;
          const y = top + (nB - 1 - bi) * cellH;
          return (
            <rect
              key={`${ti}-${bi}`}
              x={x}
              y={y}
              width={cellW}
              height={cellH}
              fill={fill}
              rx={1}
              onMouseEnter={() => onHover({ ti, bi, v })}
              onMouseMove={() => onHover({ ti, bi, v })}
              onMouseLeave={() => onHover(null)}
            />
          );
        }),
      )}
    </g>
  );
}

export function HeatmapBlock({ data }: { data: HeatmapBlockData }) {
  const c = chartColors();
  const [hover, setHover] = useState<Hover | null>(null);

  const { vMin, vMax } = useMemo(() => {
    let mn = Infinity;
    let mx = -Infinity;
    let any = false;
    for (const row of data.values) {
      for (const v of row) {
        if (!Number.isFinite(v)) continue;
        any = true;
        if (v < mn) mn = v;
        if (v > mx) mx = v;
      }
    }
    if (!any) return { vMin: 0, vMax: 1 };
    return { vMin: mn, vMax: mx };
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
      <div style={{ position: "relative" }}>
        <ResponsiveContainer width="100%" height={height}>
          <ScatterChart margin={{ top: 8, right: 16, bottom: 32, left: 60 }}>
            <XAxis
              type="number"
              domain={[0, data.times.length]}
              ticks={data.times
                .map((_, i) => i)
                .filter((i) => i % Math.max(1, Math.ceil(data.times.length / 10)) === 0)}
              tickFormatter={(i: number) => data.times[i] ?? ""}
              tick={axisTick(10)}
              tickLine={false}
              axisLine={axisLine()}
              interval={0}
              hide={false}
            />
            <YAxis
              type="number"
              domain={[0, data.buckets.length]}
              ticks={data.buckets.map((_, i) => i + 0.5)}
              tickFormatter={(t: number) => String(data.buckets[Math.floor(t)] ?? "")}
              tick={axisTick(10)}
              tickLine={false}
              axisLine={axisLine()}
              interval={0}
              width={60}
            />
            <HeatmapCells
              data={data}
              vMin={vMin}
              vMax={vMax}
              low={low}
              mid={mid}
              high={high}
              onHover={setHover}
            />
          </ScatterChart>
        </ResponsiveContainer>
        {hover && (
          <div
            style={{
              ...tooltipBox(c),
              position: "absolute",
              left: 12,
              top: 12,
              pointerEvents: "none",
              zIndex: 50,
            }}
          >
            <div>{data.times[hover.ti]}</div>
            <div>
              {String(data.buckets[hover.bi])}: <b>{hover.v}</b>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
