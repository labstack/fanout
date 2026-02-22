import { useRef, useEffect, useState, useCallback } from "react";
import { scaleLinear, scaleSequential, interpolateYlOrRd } from "d3";
import type { HeatmapBlockData } from "@/lib/types";

const MARGIN = { top: 8, right: 16, bottom: 40, left: 60 };
const ROW_HEIGHT = 24;

export function HeatmapBlock({ data }: { data: HeatmapBlockData }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(600);

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

  const numTimes = data.times.length;
  const numBuckets = data.buckets.length;

  const chartWidth = width - MARGIN.left - MARGIN.right;
  const chartHeight = numBuckets * ROW_HEIGHT;
  const svgHeight = chartHeight + MARGIN.top + MARGIN.bottom;

  const cellWidth = numTimes > 0 ? chartWidth / numTimes : 0;
  const cellHeight = ROW_HEIGHT;

  // Find min/max for color scale
  let vMin = Infinity;
  let vMax = -Infinity;
  for (const row of data.values) {
    for (const v of row) {
      if (v < vMin) vMin = v;
      if (v > vMax) vMax = v;
    }
  }
  if (!isFinite(vMin)) vMin = 0;
  if (!isFinite(vMax)) vMax = 1;
  if (vMin === vMax) vMax = vMin + 1;

  const colorScale = scaleSequential(interpolateYlOrRd).domain([vMin, vMax]);

  // Tick spacing for x-axis labels: show at most ~10 labels
  const xTickStep = Math.max(1, Math.ceil(numTimes / 10));

  // Y scale for bucket positions
  const yScale = scaleLinear()
    .domain([0, numBuckets])
    .range([chartHeight, 0]);

  return (
    <div ref={containerRef}>
      <h3 className="mb-2 text-sm font-semibold text-foreground">
        {data.title}
      </h3>
      <svg width={width} height={svgHeight} className="block">
        <g transform={`translate(${MARGIN.left},${MARGIN.top})`}>
          {/* Cells */}
          {data.values.map((row, ti) =>
            row.map((value, bi) => (
              <rect
                key={`${ti}-${bi}`}
                x={ti * cellWidth}
                y={yScale(bi + 1)}
                width={Math.max(cellWidth - 1, 1)}
                height={cellHeight - 1}
                fill={colorScale(value)}
                rx={1}
              >
                <title>
                  {`${data.times[ti]} | ${data.buckets[bi]}: ${value}`}
                </title>
              </rect>
            )),
          )}
          {/* X-axis labels */}
          {data.times.map((t, i) =>
            i % xTickStep === 0 ? (
              <text
                key={i}
                x={i * cellWidth + cellWidth / 2}
                y={chartHeight + 16}
                textAnchor="middle"
                className="fill-foreground text-[10px]"
              >
                {t}
              </text>
            ) : null,
          )}
          {/* Y-axis labels */}
          {data.buckets.map((b, i) => (
            <text
              key={i}
              x={-6}
              y={yScale(i + 1) + cellHeight / 2 + 1}
              textAnchor="end"
              dominantBaseline="middle"
              className="fill-foreground text-[10px]"
            >
              {b}
            </text>
          ))}
        </g>
      </svg>
    </div>
  );
}
