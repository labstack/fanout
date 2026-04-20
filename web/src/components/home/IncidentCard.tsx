import { Search } from "lucide-react";
import type { OverviewIncident } from "@/lib/types";
import { Sparkline } from "./Sparkline";

function timeAgo(iso?: string): string {
  if (!iso) return "";
  const diff = Date.now() - new Date(iso).getTime();
  const mins = Math.floor(diff / 60_000);
  if (mins < 1) return "just now";
  if (mins < 60) return `~${mins}m ago`;
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

function truncateError(msg: string, max = 72): string {
  if (msg.length <= max) return msg;
  return msg.slice(0, max) + "\u2026";
}

function primaryIssue(incident: OverviewIncident): string {
  const highErr = incident.error_rate > 0.01;
  const highLat = incident.p95_ms > 1000;
  if (highErr && highLat) return "high errors + latency";
  if (highErr) return "high error rate";
  if (highLat) return "high latency";
  return "degraded";
}

interface Props {
  incident: OverviewIncident;
  onInvestigate: (prompt: string) => void;
  compact?: boolean;
  primary?: boolean;
}

export function IncidentCard({ incident, onInvestigate, compact = false, primary = false }: Props) {
  const isUnhealthy = incident.status === "unhealthy";
  const borderCls = isUnhealthy ? "border-unhealthy/20" : "border-degraded/20";
  const bgCls = isUnhealthy ? "bg-unhealthy/5" : "bg-degraded/5";
  const statusCls = isUnhealthy ? "text-unhealthy" : "text-degraded";
  const started = timeAgo(incident.started_at);

  const prompt = `Investigate ${incident.service} — ${primaryIssue(incident)}: error rate ${fmtPercent(incident.error_rate)}, p95 ${fmtMs(incident.p95_ms)}${incident.started_at ? `, started ${started}` : ""}. What's the root cause?`;

  // Compact mode — single clickable row
  if (compact) {
    return (
      <button
        type="button"
        onClick={() => onInvestigate(prompt)}
        className={`w-full flex items-center gap-4 px-4 py-2.5 rounded-lg text-left transition-colors hover:bg-surface-2 group border ${borderCls} ${bgCls}`}
      >
        <span className={`text-sm ${statusCls}`}>
          {isUnhealthy ? "\u2716" : "\u25B2"}
        </span>
        <span className="font-heading text-sm text-foreground/90 group-hover:text-foreground min-w-0 flex-1 truncate">
          {incident.service}
        </span>
        {started && (
          <span className="text-[10px] text-muted-foreground mono hidden sm:inline shrink-0">
            {started}
          </span>
        )}
        <span className={`mono text-xs ${statusCls} w-16 text-right shrink-0`}>
          {fmtPercent(incident.error_rate)}
        </span>
        <span className="mono text-xs text-foreground/70 w-14 text-right shrink-0">
          {fmtMs(incident.p95_ms)}
        </span>
        <Sparkline
          values={incident.sparkline_error_rate}
          width={56}
          height={18}
          color={isUnhealthy ? "var(--unhealthy)" : "var(--degraded)"}
          className="opacity-70 group-hover:opacity-100 transition-opacity shrink-0 hidden sm:block"
        />
      </button>
    );
  }

  // Full expanded card — 2-column on desktop
  return (
    <div className={`rounded-xl border ${borderCls} ${bgCls} p-4 fade-up`}>
      {/* Header: service name, started time, badge */}
      <div className="flex flex-col sm:flex-row sm:items-center justify-between gap-1 sm:gap-3 mb-3">
        <div className="flex items-center gap-2.5 min-w-0">
          <span className={`text-sm shrink-0 ${statusCls}`}>{"\u2716"}</span>
          <span className="font-heading text-sm font-semibold text-foreground truncate">
            {incident.service}
          </span>
          {started && (
            <span className="text-[11px] text-muted-foreground mono shrink-0">
              {started}
            </span>
          )}
        </div>
        <div className="flex items-center gap-2 pl-6 sm:pl-0">
          <span className={`text-[10px] mono ${statusCls} opacity-70`}>
            {primaryIssue(incident)}
          </span>
          <span className={`inline-flex rounded-full border ${borderCls} ${bgCls} px-2.5 py-0.5 text-[10px] font-bold uppercase tracking-wider ${statusCls}`}>
            {incident.status}
          </span>
        </div>
      </div>

      {/* Body: 2-column on desktop */}
      <div className="grid grid-cols-1 sm:grid-cols-[1fr_auto] gap-3 mb-3">
        {/* Left: metrics */}
        <div className="space-y-2">
          <div className="flex items-center gap-4 text-sm flex-wrap">
            <div>
              <span className="text-muted-foreground">Errors: </span>
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

          {incident.related && incident.related.length > 0 && (
            <div className="text-[12px] text-muted-foreground">
              Related: {incident.related.join(", ")}
            </div>
          )}
        </div>

        {/* Right: sparkline */}
        {incident.sparkline_error_rate?.length > 1 && (
          <div className="flex items-center">
            <Sparkline
              values={incident.sparkline_error_rate}
              width={120}
              height={36}
              color={isUnhealthy ? "var(--unhealthy)" : "var(--degraded)"}
            />
          </div>
        )}
      </div>

      {/* Top errors — compact 2-column */}
      {incident.top_errors && incident.top_errors.length > 0 && (
        <div className="mb-3">
          <div className="detail-label mb-1">Top errors</div>
          <div className="space-y-0.5">
            {incident.top_errors.slice(0, 3).map((err) => (
              <div
                key={err.message}
                className="flex items-baseline gap-2 text-[12px]"
              >
                <span className="mono text-muted-foreground tabular-nums shrink-0 w-10 text-right">
                  {err.count.toLocaleString()}
                </span>
                <span className="text-foreground/70 mono truncate" title={err.message}>
                  {truncateError(err.message)}
                </span>
              </div>
            ))}
          </div>
        </div>
      )}

      <button
        type="button"
        onClick={() => onInvestigate(prompt)}
        className={`${primary ? "btn-primary" : "btn-ghost"} w-full sm:w-auto inline-flex items-center justify-center gap-1.5 text-xs`}
      >
        <Search className="h-3 w-3" />
        Investigate
      </button>
    </div>
  );
}
