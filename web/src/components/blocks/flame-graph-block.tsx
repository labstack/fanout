import { useRef, useEffect, useState, useCallback, useMemo } from "react";
import { scaleOrdinal, schemeTableau10 } from "d3";
import type { FlameGraphData, FlameFrame } from "@/lib/types";

const ROW_HEIGHT = 20;
const PAD_X = 8;
const PAD_Y = 4;
const AXIS_HEIGHT = 20;

export function FlameGraphBlock({ data }: { data: FlameGraphData }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(800);
  const [tooltip, setTooltip] = useState<{
    x: number;
    y: number;
    frame: FlameFrame;
  } | null>(null);

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

  const services = useMemo(
    () => Array.from(new Set(data.frames.map((f) => f.service))),
    [data.frames],
  );

  const serviceColor = useMemo(
    () => scaleOrdinal(schemeTableau10).domain(services),
    [services],
  );

  const maxDepth = useMemo(
    () =>
      data.frames.length > 0
        ? Math.max(...data.frames.map((f) => f.depth))
        : 0,
    [data.frames],
  );

  const barAreaWidth = width - PAD_X * 2;
  const svgHeight = PAD_Y * 2 + (maxDepth + 1) * (ROW_HEIGHT + 1) + AXIS_HEIGHT;

  if (data.frames.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-muted/50 p-4 text-sm text-muted-foreground">
        No flame graph data to display.
      </div>
    );
  }

  return (
    <div ref={containerRef} className="relative block-card">
      <h3 className="block-title">Flame Graph</h3>
      <svg
        width={width}
        height={svgHeight}
        className="block select-none"
        role="img"
        aria-label="Flame graph of span self-time across services"
        onMouseLeave={() => setTooltip(null)}
      >
        {/* Frames */}
        {data.frames.map((frame, i) => {
          const x = PAD_X + frame.x * barAreaWidth;
          const w = frame.w * barAreaWidth;
          const y = PAD_Y + frame.depth * (ROW_HEIGHT + 1);
          const color = serviceColor(frame.service);
          const minLabelW = 35;
          const label = w > minLabelW ? frame.name : "";
          const maxChars = Math.floor((w - 8) / 6);
          const truncated =
            label.length > maxChars
              ? label.slice(0, Math.max(0, maxChars - 1)) + "\u2026"
              : label;

          return (
            <g
              key={i}
              className="cursor-pointer"
              onMouseEnter={(e) => {
                const rect = containerRef.current?.getBoundingClientRect();
                if (rect) {
                  setTooltip({
                    x: e.clientX - rect.left,
                    y: e.clientY - rect.top,
                    frame,
                  });
                }
              }}
              onMouseLeave={() => setTooltip(null)}
            >
              <rect
                x={x}
                y={y}
                width={Math.max(w - 0.5, 1)}
                height={ROW_HEIGHT}
                fill={color}
                rx={2}
                ry={2}
                opacity={0.85}
              />
              {truncated && (
                <text
                  x={x + 4}
                  y={y + ROW_HEIGHT / 2 + 3}
                  className="fill-white text-[9px] pointer-events-none"
                  style={{ textShadow: "0 1px 1px rgba(0,0,0,0.3)" }}
                >
                  {truncated}
                </text>
              )}
            </g>
          );
        })}

        {/* Percentage axis */}
        {[0, 25, 50, 75, 100].map((pct) => {
          const x = PAD_X + (pct / 100) * barAreaWidth;
          const axisY = svgHeight - 6;
          return (
            <g key={pct}>
              <line
                x1={x}
                y1={PAD_Y}
                x2={x}
                y2={axisY - 10}
                stroke="hsl(var(--border))"
                strokeDasharray="2 3"
                strokeWidth={0.5}
              />
              <text
                x={x}
                y={axisY}
                textAnchor="middle"
                className="fill-muted-foreground text-[9px]"
              >
                {pct}%
              </text>
            </g>
          );
        })}
      </svg>

      {/* Service legend */}
      <div className="mt-2 flex flex-wrap gap-3 px-1">
        {services.map((svc) => (
          <div key={svc} className="flex items-center gap-1.5 text-xs">
            <span
              className="inline-block h-2.5 w-2.5 rounded-sm"
              style={{ backgroundColor: serviceColor(svc) }}
            />
            <span className="text-muted-foreground">{svc}</span>
          </div>
        ))}
      </div>

      {/* Tooltip */}
      {tooltip && (
        <div
          className="pointer-events-none absolute z-50 rounded-md border border-border bg-popover px-3 py-2 text-xs text-popover-foreground shadow-md"
          style={{
            left: Math.min(tooltip.x + 12, width - 220),
            top: tooltip.y - 8,
          }}
        >
          <div className="mb-1 font-medium">
            {tooltip.frame.service}: {tooltip.frame.name}
          </div>
          <div className="space-y-0.5 text-muted-foreground">
            <div className="flex justify-between gap-4">
              <span>Total</span>
              <span className="font-mono text-foreground">
                {tooltip.frame.total.toFixed(1)}ms
              </span>
            </div>
            <div className="flex justify-between gap-4">
              <span>Self</span>
              <span className="font-mono text-foreground">
                {tooltip.frame.self.toFixed(1)}ms
              </span>
            </div>
            <div className="flex justify-between gap-4">
              <span>Service</span>
              <span className="font-mono text-foreground">
                {tooltip.frame.service}
              </span>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
