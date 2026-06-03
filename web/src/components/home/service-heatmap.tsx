import { useLocation, useNavigate } from "react-router";
import type { OverviewService } from "@/lib/types";
import { Sparkline } from "./sparkline";

function fmtTraffic(v: number): string {
  if (v >= 1000) return `${(v / 1000).toFixed(1)}k`;
  return v.toFixed(0);
}

function fmtPercent(v: number): string {
  return `${(v * 100).toFixed(1)}%`;
}

interface TileStyle {
  dot: string;
  tile: string;
  spark: string;
  err: string;
}

const STYLES: Record<string, TileStyle> = {
  unhealthy: {
    dot: "bg-unhealthy",
    tile: "border-unhealthy/30 bg-unhealthy/[0.07] hover:border-unhealthy/50",
    spark: "var(--unhealthy)",
    err: "text-unhealthy",
  },
  degraded: {
    dot: "bg-degraded",
    tile: "border-degraded/30 bg-degraded/[0.06] hover:border-degraded/50",
    spark: "var(--degraded)",
    err: "text-degraded",
  },
  healthy: {
    dot: "bg-healthy",
    tile: "border-border/60 bg-surface-1 hover:border-border",
    spark: "var(--primary)",
    err: "text-muted-foreground",
  },
};

function styleFor(status: string): TileStyle {
  return STYLES[status] ?? STYLES.healthy;
}

interface Props {
  // Services in severity order (worst first), as returned by /api/overview.
  services: OverviewService[];
}

export function ServiceHeatmap({ services }: Props) {
  const navigate = useNavigate();
  const { search } = useLocation();

  if (services.length === 0) return null;

  return (
    <div className="rounded-xl border border-border/60 bg-surface-1/80 p-3">
      <div className="flex items-center justify-between px-1 pb-2">
        <span className="detail-label">Services · {services.length}</span>
        <span className="font-mono text-[10px] uppercase tracking-wider text-muted-foreground/60">
          sorted by health
        </span>
      </div>
      <div className="grid grid-cols-2 gap-2 sm:grid-cols-3 lg:grid-cols-4">
        {services.map((svc) => {
          const st = styleFor(svc.status);
          return (
            <button
              key={svc.service}
              type="button"
              onClick={() =>
                navigate(`/service/${encodeURIComponent(svc.service)}${search}`)
              }
              className={`group flex flex-col gap-1.5 rounded-lg border px-3 py-2.5 text-left transition-transform hover:-translate-y-0.5 ${st.tile}`}
            >
              <div className="flex min-w-0 items-center gap-2">
                <span className={`size-1.5 shrink-0 rounded-full ${st.dot}`} />
                <span className="truncate font-heading text-[12px] text-foreground/90 group-hover:text-foreground">
                  {svc.service}
                </span>
              </div>
              <Sparkline
                values={svc.sparkline_traffic}
                width={200}
                height={20}
                color={st.spark}
                className="max-w-full opacity-50 transition-opacity group-hover:opacity-80"
              />
              <div className="flex items-center justify-between font-mono text-[10.5px] text-muted-foreground">
                <span>{fmtTraffic(svc.traffic_per_min)}/min</span>
                <span className={`font-medium ${st.err}`}>
                  {fmtPercent(svc.error_rate)}
                </span>
              </div>
            </button>
          );
        })}
      </div>
    </div>
  );
}
