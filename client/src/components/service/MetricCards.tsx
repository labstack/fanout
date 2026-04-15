import type { BaselineComparison } from "@/lib/types";

function fmtVal(v: number, unit: string): string {
  if (unit === "pct") return `${(v * 100).toFixed(1)}%`;
  if (unit === "ms") return v >= 1000 ? `${(v / 1000).toFixed(1)}s` : `${v.toFixed(0)}ms`;
  if (unit === "count") return v >= 1000 ? `${(v / 1000).toFixed(1)}k` : v.toFixed(0);
  return v.toFixed(1);
}

function colorCls(label: string, v: number): string {
  if (label === "Error Rate") {
    if (v > 0.1) return "text-unhealthy";
    if (v > 0.01) return "text-degraded";
  }
  if (label === "P95" || label === "P50") {
    if (v > 5000) return "text-unhealthy";
    if (v > 1000) return "text-degraded";
  }
  return "text-foreground";
}

interface Props {
  errorRate: number;
  p95Ms: number;
  p50Ms: number;
  spanCount: number;
  windowMinutes: number;
  baseline?: BaselineComparison;
}

export function MetricCards({ errorRate, p95Ms, p50Ms, spanCount, windowMinutes, baseline }: Props) {
  const cards = [
    { label: "Error Rate", value: errorRate, unit: "pct" as const },
    { label: "P95", value: p95Ms, unit: "ms" as const },
    { label: "P50", value: p50Ms, unit: "ms" as const },
    { label: "Traffic", value: spanCount / windowMinutes, unit: "count" as const, suffix: "spans/min" },
  ];

  return (
    <div className="grid grid-cols-2 sm:grid-cols-4 gap-3">
      {cards.map((c) => (
        <div key={c.label} className="rounded-lg border border-border/60 bg-surface-1/80 p-3">
          <div className="detail-label mb-1">{c.label}</div>
          <div className={`font-heading text-xl font-bold ${colorCls(c.label, c.value)}`}>
            {fmtVal(c.value, c.unit)}
          </div>
          {c.suffix && (
            <div className="text-[10px] text-muted-foreground mono">{c.suffix}</div>
          )}
          {c.label === "P95" && baseline && baseline.baseline_p95_ms > 0 && (
            <div className="text-[10px] text-muted-foreground mono mt-1">
              baseline {fmtVal(baseline.baseline_p95_ms, "ms")}
            </div>
          )}
        </div>
      ))}
    </div>
  );
}
