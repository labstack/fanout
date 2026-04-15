import type { HomeService } from "@/lib/types";
import { Sparkline } from "./Sparkline";

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
  service: HomeService;
  onClick: (service: string) => void;
}

export function ServiceRow({ service, onClick }: Props) {
  return (
    <button
      type="button"
      onClick={() => onClick(service.name)}
      className="w-full flex items-center gap-4 px-4 py-2.5 rounded-lg text-left transition-colors hover:bg-surface-2 group"
    >
      <span className="text-sm text-healthy">{"\u25CF"}</span>
      <span className="font-heading text-sm text-foreground/90 group-hover:text-foreground w-48 truncate">
        {service.name}
      </span>
      <Sparkline
        values={service.sparkline_traffic}
        width={64}
        height={20}
        color="var(--primary)"
        className="opacity-40 group-hover:opacity-70 transition-opacity"
      />
      <span className="mono text-xs text-muted-foreground w-24 text-right">
        {fmtTraffic(service.traffic_per_min)} <span className="text-[10px]">spans/min</span>
      </span>
      <span className="mono text-xs text-foreground/70 w-14 text-right">
        {fmtPercent(service.error_rate)}
      </span>
      <span className="mono text-xs text-foreground/70 w-16 text-right">
        {fmtMs(service.p95_ms)}
      </span>
    </button>
  );
}
