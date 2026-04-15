import { useId, useMemo } from "react";

/** Resolve a CSS variable to its computed hex value for SVG gradient stops */
function resolveColor(color: string): string {
  if (!color.startsWith("var(")) return color;
  const varName = color.slice(4, -1).trim();
  return getComputedStyle(document.documentElement).getPropertyValue(varName).trim() || color;
}

interface SparklineProps {
  values: number[];
  width?: number;
  height?: number;
  color?: string;
  className?: string;
}

export function Sparkline({
  values,
  width = 80,
  height = 24,
  color = "var(--primary)",
  className = "",
}: SparklineProps) {
  const gradientId = useId();
  const resolvedColor = useMemo(() => resolveColor(color), [color]);

  if (!values || values.length < 2) {
    return <svg width={width} height={height} className={className} />;
  }

  const min = Math.min(...values);
  const max = Math.max(...values);
  const range = max - min || 1;
  const padding = 1;

  const coords = values.map((v, i) => {
    const x = (i / (values.length - 1)) * (width - padding * 2) + padding;
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
