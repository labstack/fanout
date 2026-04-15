import type { HomeSummary } from "@/lib/types";

function healthIndicator(summary: HomeSummary) {
  if (summary.unhealthy > 0)
    return { icon: "\u2716", label: `${summary.unhealthy} unhealthy`, cls: "text-unhealthy" };
  if (summary.degraded > 0)
    return { icon: "\u25D0", label: `${summary.degraded} degraded`, cls: "text-degraded" };
  return { icon: "\u25CF", label: "All healthy", cls: "text-healthy" };
}

function fmtTraffic(v: number): string {
  if (v >= 1000) return `${(v / 1000).toFixed(1)}k`;
  return v.toFixed(0);
}

function fmtPercent(v: number): string {
  return `${(v * 100).toFixed(1)}%`;
}

function fmtMs(v: number): string {
  if (v >= 1000) return `${(v / 1000).toFixed(1)}s`;
  return `${v.toFixed(0)}ms`;
}

interface Props {
  summary: HomeSummary;
}

export function SummaryHeader({ summary }: Props) {
  const h = healthIndicator(summary);

  return (
    <div className="flex items-center justify-between gap-4 rounded-lg border border-border/60 bg-surface-1/80 px-5 py-3">
      <div className="flex items-center gap-3">
        <span className={`text-base ${h.cls}`}>{h.icon}</span>
        <span className={`font-heading text-sm font-semibold ${h.cls}`}>{h.label}</span>
        <span className="text-xs text-muted-foreground mono">
          {summary.total_services} services
        </span>
      </div>
      <div className="flex items-center gap-5">
        <div className="text-right">
          <div className="detail-label">Traffic</div>
          <div className="text-sm mono text-foreground">
            {fmtTraffic(summary.traffic_per_min)} <span className="text-muted-foreground text-[10px]">spans/min</span>
          </div>
        </div>
        <div className="text-right">
          <div className="detail-label">Errors</div>
          <div className={`text-sm mono ${summary.error_rate > 0.05 ? "text-unhealthy" : summary.error_rate > 0.01 ? "text-degraded" : "text-foreground"}`}>
            {fmtPercent(summary.error_rate)}
          </div>
        </div>
        <div className="text-right">
          <div className="detail-label">P95</div>
          <div className={`text-sm mono ${summary.p95_ms > 5000 ? "text-unhealthy" : summary.p95_ms > 1000 ? "text-degraded" : "text-foreground"}`}>
            {fmtMs(summary.p95_ms)}
          </div>
        </div>
      </div>
    </div>
  );
}
