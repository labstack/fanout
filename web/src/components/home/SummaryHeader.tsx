import type { OverviewHealth } from "@/lib/types";

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
  health: OverviewHealth;
}

export function SummaryHeader({ health }: Props) {
  const unhealthy = health.by_status.unhealthy ?? 0;
  const degraded = health.by_status.degraded ?? 0;

  const parts: { label: string; cls: string; icon: string }[] = [];
  if (unhealthy > 0)
    parts.push({ label: `${unhealthy} unhealthy`, cls: "text-unhealthy", icon: "\u2716" });
  if (degraded > 0)
    parts.push({ label: `${degraded} degraded`, cls: "text-degraded", icon: "\u25D0" });
  if (parts.length === 0)
    parts.push({ label: "All healthy", cls: "text-healthy", icon: "\u25CF" });

  const worstCls = parts[0].cls;

  return (
    <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-3 rounded-lg border border-border/60 bg-surface-1/80 px-5 py-3">
      <div className="flex items-center gap-3 flex-wrap">
        <span className={`text-base ${worstCls}`}>{parts[0].icon}</span>
        <div className="flex items-center gap-2 flex-wrap">
          {parts.map((p) => (
            <span key={p.label} className={`font-heading text-sm font-semibold ${p.cls}`}>
              {p.label}
            </span>
          ))}
        </div>
        <span className="text-xs text-muted-foreground mono">
          {health.total_services} services
        </span>
      </div>
      <div className="flex items-center gap-5">
        <div className="text-right">
          <div className="detail-label">Traffic</div>
          <div className="text-sm mono text-foreground">
            {fmtTraffic(health.throughput_per_min)} <span className="text-muted-foreground text-[10px]">spans/min</span>
          </div>
        </div>
        <div className="text-right">
          <div className="detail-label">Errors</div>
          <div className={`text-sm mono ${health.global_error_rate > 0.05 ? "text-unhealthy" : health.global_error_rate > 0.01 ? "text-degraded" : "text-foreground"}`}>
            {fmtPercent(health.global_error_rate)}
          </div>
        </div>
        <div className="text-right">
          <div className="detail-label">P95</div>
          <div className={`text-sm mono ${health.global_p95_ms > 5000 ? "text-unhealthy" : health.global_p95_ms > 1000 ? "text-degraded" : "text-foreground"}`}>
            {fmtMs(health.global_p95_ms)}
          </div>
        </div>
      </div>
    </div>
  );
}
