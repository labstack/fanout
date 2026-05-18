import { useNavigate, useLocation } from "react-router";
import type { OverviewService } from "@/lib/types";
import { Sparkline } from "./sparkline";

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
  service: OverviewService;
}

export function ServiceRow({ service }: Props) {
  const navigate = useNavigate();
  const { search } = useLocation();

  return (
    <button
      type="button"
      onClick={() => navigate(`/service/${encodeURIComponent(service.service)}${search}`)}
      className="w-full flex items-center gap-4 px-4 py-2 rounded-lg text-left transition-colors hover:bg-surface-2 group"
    >
      <span className="text-xs text-healthy">{"\u25CF"}</span>
      <span className="font-heading text-[13px] text-foreground/90 group-hover:text-foreground min-w-0 flex-1 truncate">
        {service.service}
      </span>
      <Sparkline
        values={service.sparkline_traffic}
        width={72}
        height={24}
        color="var(--primary)"
        className="opacity-60 group-hover:opacity-90 transition-opacity shrink-0"
      />
      <span className="mono text-xs text-muted-foreground w-24 text-right shrink-0">
        {fmtTraffic(service.traffic_per_min)} <span className="text-[10px]">spans/min</span>
      </span>
      <span className="mono text-xs text-foreground/70 w-14 text-right shrink-0">
        {fmtPercent(service.error_rate)}
      </span>
      <span className="mono text-xs text-foreground/70 w-16 text-right shrink-0">
        {fmtMs(service.p95_ms)}
      </span>
    </button>
  );
}
