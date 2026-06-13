import { useId, useMemo } from "react";

/** Resolve a CSS variable to its computed hex value for SVG gradient stops */
function resolveColor(color: string): string {
  if (!color.startsWith("var(")) return color;
  const varName = color.slice(4, -1).trim();
  return getComputedStyle(document.documentElement).getPropertyValue(varName).trim() || color;
}

/** Downsample values to ~maxPoints by averaging buckets */
function downsample(values: number[], maxPoints: number): number[] {
  if (values.length <= maxPoints) return values;
  const bucketSize = values.length / maxPoints;
  const out: number[] = [];
  for (let i = 0; i < maxPoints; i++) {
    const start = Math.floor(i * bucketSize);
    const end = Math.floor((i + 1) * bucketSize);
    let sum = 0;
    for (let j = start; j < end; j++) sum += values[j];
    const count = end - start;
    // Empty bucket (possible for certain values.length/maxPoints ratios) would
    // make sum/count === 0/0 === NaN and break the polyline — fall back to the
    // boundary sample instead.
    out.push(count > 0 ? sum / count : (values[start] ?? values[values.length - 1] ?? 0));
  }
  return out;
}

interface SparklineProps {
  values: number[];
  width?: number;
  height?: number;
  color?: string;
  className?: string;
  maxPoints?: number;
}

export function Sparkline({
  values,
  width = 80,
  height = 24,
  color = "var(--primary)",
  className = "",
  maxPoints = 16,
}: SparklineProps) {
  const gradientId = useId();
  const resolvedColor = useMemo(() => resolveColor(color), [color]);

  if (!values || values.length < 2) {
    return <svg width={width} height={height} className={className} />;
  }

  const sampled = downsample(values, maxPoints);
  const min = Math.min(...sampled);
  const max = Math.max(...sampled);
  const range = max - min || 1;
  const padding = 1;

  const coords = sampled.map((v, i) => {
    const x = (i / (sampled.length - 1)) * (width - padding * 2) + padding;
    const y = height - padding - ((v - min) / range) * (height - padding * 2);
    return { x, y };
  });

  const linePoints = coords.map((c) => `${c.x},${c.y}`).join(" ");
  const areaPoints = `${padding},${height} ${linePoints} ${width - padding},${height}`;

  return (
    <svg
      width={width}
      height={height}
      viewBox={`0 0 ${width} ${height}`}
      className={className}
    >
      <defs>
        <linearGradient id={gradientId} x1="0" y1="0" x2="0" y2="1">
          <stop offset="0%" stopColor={resolvedColor} stopOpacity={0.5} />
          <stop offset="100%" stopColor={resolvedColor} stopOpacity={0.05} />
        </linearGradient>
      </defs>
      <polygon points={areaPoints} fill={`url(#${gradientId})`} />
      <polyline
        points={linePoints}
        fill="none"
        stroke={color}
        strokeWidth={1.5}
        strokeLinecap="round"
        strokeLinejoin="round"
      />
    </svg>
  );
}
