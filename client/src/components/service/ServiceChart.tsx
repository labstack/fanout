import type { ServiceBucket, ChangePoint } from "@/lib/types";

interface Props {
  title: string;
  buckets: ServiceBucket[];
  metric: "error_rate" | "p95_ms";
  color: string;
  changePoints?: ChangePoint[];
  baselineValue?: number;
}

function fmtVal(v: number, metric: string): string {
  if (metric === "error_rate") return `${(v * 100).toFixed(1)}%`;
  return v >= 1000 ? `${(v / 1000).toFixed(1)}s` : `${v.toFixed(0)}ms`;
}

export function ServiceChart({ title, buckets, metric, color, changePoints, baselineValue }: Props) {
  if (!buckets || buckets.length < 2) {
    return (
      <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
        <div className="detail-label mb-2">{title}</div>
        <div className="text-sm text-muted-foreground">No data</div>
      </div>
    );
  }

  const values = buckets.map((b) => (metric === "error_rate" ? b.error_rate : b.p95_ms));
  const min = Math.min(...values);
  const max = Math.max(...values);
  const range = max - min || 1;

  const W = 400;
  const H = 100;
  const padY = 8;
  const padX = 4;

  const points = values
    .map((v, i) => {
      const x = (i / (values.length - 1)) * (W - padX * 2) + padX;
      const y = H - padY - ((v - min) / range) * (H - padY * 2);
      return `${x},${y}`;
    })
    .join(" ");

  const cpLines: { x: number; label: string }[] = [];
  if (changePoints) {
    const metricName = metric === "error_rate" ? "error_rate" : "p95";
    for (const cp of changePoints) {
      if (!cp.metric.includes(metricName)) continue;
      const cpTime = new Date(cp.time).getTime();
      const startTime = new Date(buckets[0].time).getTime();
      const endTime = new Date(buckets[buckets.length - 1].time).getTime();
      const totalRange = endTime - startTime || 1;
      const frac = (cpTime - startTime) / totalRange;
      if (frac >= 0 && frac <= 1) {
        const x = frac * (W - padX * 2) + padX;
        const ratio = cp.before > 0 ? (cp.after / cp.before).toFixed(1) : "n/a";
        cpLines.push({ x, label: `${ratio}x` });
      }
    }
  }

  let baselineY: number | null = null;
  if (baselineValue !== undefined && baselineValue > 0) {
    const clampedBaseline = Math.max(min, Math.min(max, baselineValue));
    baselineY = H - padY - ((clampedBaseline - min) / range) * (H - padY * 2);
  }

  const lastVal = values[values.length - 1];

  return (
    <div className="rounded-lg border border-border/60 bg-surface-1/80 p-4">
      <div className="flex items-center justify-between mb-2">
        <div className="detail-label">{title}</div>
        <div className="text-sm mono" style={{ color }}>
          {fmtVal(lastVal, metric)}
        </div>
      </div>
      <svg width="100%" viewBox={`0 0 ${W} ${H}`} preserveAspectRatio="none" className="overflow-visible">
        {baselineY !== null && (
          <line
            x1={padX} y1={baselineY} x2={W - padX} y2={baselineY}
            stroke={color} strokeWidth={0.5} strokeDasharray="4,4" opacity={0.3}
          />
        )}
        <polyline
          points={points} fill="none" stroke={color} strokeWidth={2}
          strokeLinecap="round" strokeLinejoin="round"
        />
        {cpLines.map((cp, i) => (
          <g key={i}>
            <line
              x1={cp.x} y1={0} x2={cp.x} y2={H}
              stroke="var(--primary)" strokeWidth={1} strokeDasharray="3,3" opacity={0.5}
            />
            <text x={cp.x + 3} y={12} fill="var(--primary)" fontSize={9} fontFamily="monospace">
              {cp.label}
            </text>
          </g>
        ))}
      </svg>
      <div className="flex justify-between text-[10px] text-muted-foreground mono mt-1">
        <span>-{buckets.length}m</span>
        <span>now</span>
      </div>
    </div>
  );
}
