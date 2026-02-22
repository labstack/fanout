import { useRef, useEffect, useState, useCallback, useMemo } from "react";
import { scaleLinear } from "d3";
import type { CorrelationData } from "@/lib/types";

const PAD_L = 56;
const PAD_R = 12;
const PAD_T = 4;
const PANEL_H = 80;
const PANEL_GAP = 24;
const AXIS_BOTTOM = 20;

function formatValue(val: number, label: string): string {
  if (label.includes("ms")) return Math.round(val).toString();
  if (label.includes("%")) return val.toFixed(1);
  return Math.round(val).toString();
}

export function CorrelationBlock({ data }: { data: CorrelationData }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(800);

  const updateWidth = useCallback(() => {
    if (containerRef.current) {
      setWidth(containerRef.current.clientWidth);
    }
  }, []);

  useEffect(() => {
    updateWidth();
    const observer = new ResizeObserver(updateWidth);
    if (containerRef.current) {
      observer.observe(containerRef.current);
    }
    return () => observer.disconnect();
  }, [updateWidth]);

  const n = data.times.length;
  const chartW = width - PAD_L - PAD_R;
  const step = n > 1 ? chartW / (n - 1) : 0;
  const totalH =
    PAD_T + data.panels.length * (PANEL_H + PANEL_GAP) + AXIS_BOTTOM;

  // Pre-compute line paths for each panel
  const panelPaths = useMemo(() => {
    return data.panels.map((panel) => {
      const values = panel.values;
      const maxVal = Math.max(...values) * 1.2 || 1;
      const yScale = scaleLinear().domain([0, maxVal]).range([PANEL_H, 0]);

      // Build line path
      let linePath = "";
      let areaPath = "";
      for (let i = 0; i < values.length; i++) {
        const x = PAD_L + i * step;
        const y = yScale(values[i]);
        if (i === 0) {
          linePath += `M ${x} ${y}`;
          areaPath += `M ${x} ${PANEL_H} L ${x} ${y}`;
        } else {
          linePath += ` L ${x} ${y}`;
          areaPath += ` L ${x} ${y}`;
        }
      }
      areaPath += ` L ${PAD_L + (values.length - 1) * step} ${PANEL_H} Z`;

      return { linePath, areaPath, maxVal, yScale };
    });
  }, [data.panels, step]);

  // X-axis label spacing
  const xTickStep = Math.max(1, Math.ceil(n / 10));

  if (data.panels.length === 0 || n < 2) {
    return (
      <div className="rounded-lg border border-border bg-muted/50 p-4 text-sm text-muted-foreground">
        No correlation data to display.
      </div>
    );
  }

  return (
    <div ref={containerRef}>
      <svg width={width} height={totalH} className="block select-none">
        {/* X-axis labels at bottom */}
        {data.times.map((t, i) =>
          i % xTickStep === 0 ? (
            <text
              key={i}
              x={PAD_L + i * step}
              y={totalH - 4}
              textAnchor="middle"
              className="fill-muted-foreground text-[9px]"
            >
              {t}
            </text>
          ) : null,
        )}

        {data.panels.map((panel, pi) => {
          const yOff = PAD_T + pi * (PANEL_H + PANEL_GAP);
          const { linePath, areaPath, maxVal, yScale } = panelPaths[pi];
          const color = panel.color;

          return (
            <g key={pi} transform={`translate(0,${yOff})`}>
              {/* Panel label */}
              <text
                x={PAD_L - 6}
                y={10}
                textAnchor="end"
                className="fill-foreground text-[9px] font-medium"
              >
                {panel.label}
              </text>

              {/* Grid lines + Y-axis labels */}
              {[0, 1, 2, 3].map((g) => {
                const gy = PANEL_H - (g / 3) * PANEL_H;
                const gval = (g / 3) * maxVal;
                return (
                  <g key={g}>
                    <line
                      x1={PAD_L}
                      y1={gy}
                      x2={PAD_L + chartW}
                      y2={gy}
                      stroke="hsl(var(--border))"
                      strokeWidth={0.5}
                      strokeDasharray="2 3"
                    />
                    <text
                      x={PAD_L - 6}
                      y={gy + 3}
                      textAnchor="end"
                      className="fill-muted-foreground text-[8px]"
                    >
                      {formatValue(gval, panel.label)}
                    </text>
                  </g>
                );
              })}

              {/* Baseline */}
              {panel.baseline !== undefined && (
                <line
                  x1={PAD_L}
                  y1={yScale(panel.baseline)}
                  x2={PAD_L + chartW}
                  y2={yScale(panel.baseline)}
                  stroke={color}
                  strokeWidth={0.5}
                  strokeDasharray="4 3"
                  opacity={0.4}
                />
              )}

              {/* Area fill */}
              <path d={areaPath} fill={color} opacity={0.1} />

              {/* Line */}
              <path
                d={linePath}
                fill="none"
                stroke={color}
                strokeWidth={1.5}
                strokeLinecap="round"
                strokeLinejoin="round"
              />

              {/* Event markers */}
              {panel.markers?.map((m, mi) => {
                const tIdx = data.times.indexOf(m.t);
                if (tIdx < 0 || tIdx >= panel.values.length) return null;
                const mx = PAD_L + tIdx * step;
                const my = yScale(panel.values[tIdx]);
                const markerColor =
                  m.severity === "critical" ? "#ef4444" : "#f59e0b";

                return (
                  <g key={mi}>
                    {/* Vertical dashed line */}
                    <line
                      x1={mx}
                      y1={0}
                      x2={mx}
                      y2={PANEL_H}
                      stroke={markerColor}
                      strokeWidth={0.75}
                      strokeDasharray="3 2"
                      opacity={0.5}
                    />
                    {/* Marker dot */}
                    <circle
                      cx={mx}
                      cy={my}
                      r={3.5}
                      fill={markerColor}
                      stroke="hsl(var(--background))"
                      strokeWidth={1.5}
                    >
                      <title>
                        {m.label} ({m.severity}) at {m.t}
                      </title>
                    </circle>
                    {/* Marker label */}
                    <text
                      x={mx}
                      y={-4}
                      textAnchor="middle"
                      className="text-[7px] font-medium"
                      fill={markerColor}
                    >
                      {m.label.length > 12
                        ? m.label.slice(0, 11) + "\u2026"
                        : m.label}
                    </text>
                  </g>
                );
              })}
            </g>
          );
        })}
      </svg>
    </div>
  );
}
