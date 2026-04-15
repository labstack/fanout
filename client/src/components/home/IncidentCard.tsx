import { Search } from "lucide-react";
import type { HomeIncident } from "@/lib/types";
import { Sparkline } from "./Sparkline";

function timeAgo(iso?: string): string {
  if (!iso) return "";
  const diff = Date.now() - new Date(iso).getTime();
  const mins = Math.floor(diff / 60_000);
  if (mins < 1) return "just now";
  if (mins < 60) return `~${mins} min ago`;
  const hrs = Math.floor(mins / 60);
  return `~${hrs}h ago`;
}

function fmtPercent(v: number): string {
  return `${(v * 100).toFixed(1)}%`;
}

function fmtMs(v: number): string {
  if (v >= 1000) return `${(v / 1000).toFixed(1)}s`;
  return `${v.toFixed(0)}ms`;
}

function fmtTraffic(v: number): string {
  if (v >= 1000) return `${(v / 1000).toFixed(1)}k`;
  return v.toFixed(0);
}

interface Props {
  incident: HomeIncident;
  onInvestigate: (prompt: string) => void;
}

export function IncidentCard({ incident, onInvestigate }: Props) {
  const isUnhealthy = incident.health === "unhealthy";
  const borderCls = isUnhealthy
    ? "border-unhealthy/20"
    : "border-degraded/20";
  const bgCls = isUnhealthy ? "bg-unhealthy/5" : "bg-degraded/5";
  const statusCls = isUnhealthy ? "text-unhealthy" : "text-degraded";

  const prompt = isUnhealthy
    ? `Investigate ${incident.service} — error rate is ${fmtPercent(incident.error_rate)}${incident.started_at ? `, started ${timeAgo(incident.started_at)}` : ""}. What's the root cause?`
    : `Investigate ${incident.service} — p95 latency is ${fmtMs(incident.p95_ms)}. What's causing the degradation?`;

  return (
    <div className={`rounded-xl border ${borderCls} ${bgCls} p-4 fade-up`}>
      <div className="flex items-start justify-between gap-3 mb-3">
        <div className="flex items-center gap-2.5">
          <span className={`text-sm ${statusCls}`}>
            {isUnhealthy ? "\u2716" : "\u25B2"}
          </span>
          <span className="font-heading text-sm font-semibold text-foreground">
            {incident.service}
          </span>
        </div>
        <span
          className={`inline-flex rounded-full border ${borderCls} ${bgCls} px-2.5 py-0.5 text-[10px] font-bold uppercase tracking-wider ${statusCls}`}
        >
          {incident.health}
        </span>
      </div>

      <div className="space-y-2 mb-3">
        <div className="flex items-center gap-4 text-sm">
          <div>
            <span className="text-muted-foreground">Error rate: </span>
            <span className={`mono font-medium ${statusCls}`}>
              {fmtPercent(incident.error_rate)}
            </span>
          </div>
          <div>
            <span className="text-muted-foreground">P95: </span>
            <span className="mono text-foreground">{fmtMs(incident.p95_ms)}</span>
          </div>
          <div>
            <span className="text-muted-foreground">Traffic: </span>
            <span className="mono text-foreground">
              {fmtTraffic(incident.traffic_per_min)} spans/min
            </span>
          </div>
        </div>

        {incident.sparkline_error_rate?.length > 1 && (
          <div className="flex items-center gap-2">
            <Sparkline
              values={incident.sparkline_error_rate}
              width={120}
              height={28}
              color={isUnhealthy ? "var(--unhealthy)" : "var(--degraded)"}
            />
            {incident.started_at && (
              <span className="text-[11px] text-muted-foreground mono">
                started {timeAgo(incident.started_at)}
              </span>
            )}
          </div>
        )}

        {incident.related && incident.related.length > 0 && (
          <div className="text-[12px] text-muted-foreground">
            Related: {incident.related.join(", ")} also affected
          </div>
        )}
      </div>

      {incident.top_errors && incident.top_errors.length > 0 && (
        <div className="mb-3 space-y-1">
          <div className="detail-label">Top errors</div>
          {incident.top_errors.slice(0, 3).map((err) => (
            <div
              key={err.message}
              className="flex items-center justify-between text-[12px]"
            >
              <span className="text-foreground/70 truncate mr-3 mono">
                {err.message}
              </span>
              <span className="text-muted-foreground mono shrink-0">
                {err.count.toLocaleString()}
              </span>
            </div>
          ))}
        </div>
      )}

      <button
        type="button"
        onClick={() => onInvestigate(prompt)}
        className="btn-ghost inline-flex items-center gap-1.5 text-xs"
      >
        <Search className="h-3 w-3" />
        Investigate
      </button>
    </div>
  );
}
