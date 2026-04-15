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

/** Truncate long error messages (gRPC stack traces, etc.) */
function truncateError(msg: string, max = 80): string {
  if (msg.length <= max) return msg;
  return msg.slice(0, max) + "\u2026";
}

/** Identify the primary reason a service is unhealthy/degraded */
function primaryIssue(incident: HomeIncident): string {
  const highErr = incident.error_rate > 0.01;
  const highLat = incident.p95_ms > 1000;
  if (highErr && highLat) return "high errors + latency";
  if (highErr) return "high error rate";
  if (highLat) return "high latency";
  return "degraded";
}

interface Props {
  incident: HomeIncident;
  onInvestigate: (prompt: string) => void;
  compact?: boolean;
}

export function IncidentCard({ incident, onInvestigate, compact = false }: Props) {
  const isUnhealthy = incident.health === "unhealthy";
  const borderCls = isUnhealthy ? "border-unhealthy/20" : "border-degraded/20";
  const bgCls = isUnhealthy ? "bg-unhealthy/5" : "bg-degraded/5";
  const statusCls = isUnhealthy ? "text-unhealthy" : "text-degraded";

  const prompt = `Investigate ${incident.service} — ${primaryIssue(incident)}: error rate ${fmtPercent(incident.error_rate)}, p95 ${fmtMs(incident.p95_ms)}${incident.started_at ? `, started ${timeAgo(incident.started_at)}` : ""}. What's the root cause?`;

  // Compact mode for degraded services — single row
  if (compact) {
    return (
      <button
        type="button"
        onClick={() => onInvestigate(prompt)}
        className={`w-full flex items-center gap-4 px-4 py-2.5 rounded-lg text-left transition-colors hover:bg-surface-2 group border ${borderCls} ${bgCls}`}
      >
        <span className={`text-sm ${statusCls}`}>{"\u25B2"}</span>
        <span className="font-heading text-sm text-foreground/90 group-hover:text-foreground w-48 truncate">
          {incident.service}
        </span>
        <span className={`mono text-xs ${statusCls} w-20`}>
          {fmtPercent(incident.error_rate)}
        </span>
        <span className="mono text-xs text-foreground/70 w-16">
          {fmtMs(incident.p95_ms)}
        </span>
        <Sparkline
          values={incident.sparkline_error_rate}
          width={64}
          height={20}
          color="var(--degraded)"
          className="opacity-50 group-hover:opacity-80 transition-opacity"
        />
        <span className={`ml-auto text-[10px] uppercase tracking-wider mono ${statusCls}`}>
          {primaryIssue(incident)}
        </span>
      </button>
    );
  }

  // Full card for unhealthy services
  return (
    <div className={`rounded-xl border ${borderCls} ${bgCls} p-4 fade-up`}>
      <div className="flex items-start justify-between gap-3 mb-3">
        <div className="flex items-center gap-2.5">
          <span className={`text-sm ${statusCls}`}>{"\u2716"}</span>
          <span className="font-heading text-sm font-semibold text-foreground">
            {incident.service}
          </span>
          <span className={`text-[10px] mono ${statusCls} opacity-70`}>
            {primaryIssue(incident)}
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
            <span className={`mono font-medium ${incident.error_rate > 0.01 ? statusCls : "text-foreground"}`}>
              {fmtPercent(incident.error_rate)}
            </span>
          </div>
          <div>
            <span className="text-muted-foreground">P95: </span>
            <span className={`mono ${incident.p95_ms > 1000 ? statusCls : "text-foreground"}`}>
              {fmtMs(incident.p95_ms)}
            </span>
          </div>
          <div>
            <span className="text-muted-foreground">Traffic: </span>
            <span className="mono text-foreground">
              {fmtTraffic(incident.traffic_per_min)} <span className="text-[10px] text-muted-foreground">spans/min</span>
            </span>
          </div>
        </div>

        {incident.sparkline_error_rate?.length > 1 && (
          <div className="flex items-center gap-2">
            <Sparkline
              values={incident.sparkline_error_rate}
              width={140}
              height={32}
              color={isUnhealthy ? "var(--unhealthy)" : "var(--degraded)"}
            />
            {incident.started_at && (
              <span className="text-[11px] text-muted-foreground mono">
                {timeAgo(incident.started_at)}
              </span>
            )}
          </div>
        )}

        {incident.related && incident.related.length > 0 && (
          <div className="text-[12px] text-muted-foreground">
            Related: {incident.related.join(", ")}
          </div>
        )}
      </div>

      {incident.top_errors && incident.top_errors.length > 0 && (
        <div className="mb-3 space-y-1">
          <div className="detail-label">Top errors</div>
          {incident.top_errors.slice(0, 3).map((err) => (
            <div
              key={err.message}
              className="flex items-center justify-between text-[12px] gap-3"
            >
              <span className="text-foreground/70 truncate mono" title={err.message}>
                {truncateError(err.message)}
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
