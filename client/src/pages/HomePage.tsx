import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useLocation } from "react-router";
import { api, ApiError, setApiToken } from "@/api/client";
import type { HomeResponse } from "@/lib/types";
import { buildChatPath } from "@/lib/chat-route";
import { EmptyState } from "@/components/home/EmptyState";
import { SummaryHeader } from "@/components/home/SummaryHeader";
import { IncidentCard } from "@/components/home/IncidentCard";
import { ServiceRow } from "@/components/home/ServiceRow";

const REFRESH_INTERVAL = 30_000;

export function HomePage() {
  const navigate = useNavigate();
  const { search } = useLocation();
  const token = useMemo(
    () => new URLSearchParams(search).get("token") ?? undefined,
    [search],
  );

  const [data, setData] = useState<HomeResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [timeWindow, setTimeWindow] = useState(60);
  const [staleSeconds, setStaleSeconds] = useState(0);
  const [fetchError, setFetchError] = useState<string | null>(null);
  const lastFetch = useRef(0);

  useEffect(() => {
    if (token) setApiToken(token);
  }, [token]);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        const result = await api<HomeResponse>(`/api/home?window=${timeWindow}`);
        if (!cancelled) {
          setData(result);
          setLoading(false);
          setFetchError(null);
          lastFetch.current = Date.now();
          setStaleSeconds(0);
        }
      } catch (err) {
        if (!cancelled) {
          setLoading(false);
          const message = err instanceof ApiError
            ? `Failed to load: ${err.message}`
            : "Failed to load home data";
          setFetchError(message);
          console.error("Home fetch failed:", err);
        }
      }
    }

    load();
    const interval = setInterval(load, REFRESH_INTERVAL);

    // Tick staleness counter every 10s
    const staleTick = setInterval(() => {
      if (lastFetch.current > 0) {
        setStaleSeconds(Math.floor((Date.now() - lastFetch.current) / 1000));
      }
    }, 10_000);

    return () => {
      cancelled = true;
      clearInterval(interval);
      clearInterval(staleTick);
    };
  }, [timeWindow]);

  const investigate = (prompt: string) => {
    navigate(buildChatPath(prompt, token));
  };

  const windowOptions = [
    { label: "15m", value: 15 },
    { label: "1h", value: 60 },
    { label: "6h", value: 360 },
    { label: "24h", value: 1440 },
  ];

  // Empty state: no data or no services
  if (!loading && (!data || data.summary.total_services === 0)) {
    return <EmptyState />;
  }

  // Loading skeleton
  if (loading && !data) {
    return (
      <div className="px-4 py-6 sm:px-6">
        <div className="mx-auto max-w-5xl space-y-4">
          <div className="h-14 shimmer rounded-lg" />
          <div className="h-40 shimmer rounded-xl" />
          <div className="h-10 shimmer rounded-lg" />
          <div className="h-10 shimmer rounded-lg" />
        </div>
      </div>
    );
  }

  if (!data) return null;

  const unhealthy = data.incidents.filter((i) => i.health === "unhealthy");
  const degraded = data.incidents.filter((i) => i.health === "degraded");
  const healthyCount = data.services.length;
  const hasIncidents = data.incidents.length > 0;

  return (
    <div className="px-4 py-6 sm:px-6">
      <div className="mx-auto max-w-5xl space-y-4 fade-up">
        {/* Time range selector */}
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-1 rounded-full border border-border/60 bg-surface-1/70 p-1">
            {windowOptions.map((opt) => (
              <button
                key={opt.value}
                type="button"
                onClick={() => setTimeWindow(opt.value)}
                className={`rounded-full px-3 py-1 text-[11px] mono transition-colors ${
                  timeWindow === opt.value
                    ? "bg-primary/12 text-foreground"
                    : "text-muted-foreground hover:text-foreground"
                }`}
              >
                {opt.label}
              </button>
            ))}
          </div>
          {staleSeconds >= 60 && (
            <span className="text-[11px] text-muted-foreground/60 mono">
              updated {staleSeconds >= 120 ? `${Math.floor(staleSeconds / 60)}m` : `${staleSeconds}s`} ago
            </span>
          )}
        </div>

        {/* Error banner */}
        {fetchError && (
          <div className="rounded-lg border border-unhealthy/20 bg-unhealthy/5 px-4 py-3 text-sm text-unhealthy/90 mono">
            {fetchError}
          </div>
        )}

        {/* Summary header */}
        <SummaryHeader summary={data.summary} />

        {/* Unhealthy services — full expanded cards */}
        {unhealthy.length > 0 && (
          <div className="space-y-3">
            {unhealthy.map((inc) => (
              <IncidentCard
                key={inc.service}
                incident={inc}
                onInvestigate={investigate}
              />
            ))}
          </div>
        )}

        {/* Degraded services — compact rows */}
        {degraded.length > 0 && (
          <div className="space-y-1.5">
            <div className="px-1 text-[11px] text-muted-foreground mono">
              {degraded.length} degraded
            </div>
            {degraded.map((inc) => (
              <IncidentCard
                key={inc.service}
                incident={inc}
                onInvestigate={investigate}
                compact
              />
            ))}
          </div>
        )}

        {/* Healthy services */}
        {healthyCount > 0 && (
          <div className="rounded-xl border border-border/60 bg-surface-1/80 py-1">
            <div className="flex items-center gap-4 px-4 py-2">
              <span className="text-[11px] text-muted-foreground mono">
                {hasIncidents
                  ? `${healthyCount} service${healthyCount !== 1 ? "s" : ""} healthy`
                  : `${healthyCount} services`}
              </span>
              {/* Column headers */}
              <span className="ml-auto flex items-center gap-4 text-[10px] text-muted-foreground/60 mono uppercase tracking-wider">
                <span className="w-24 text-right">traffic</span>
                <span className="w-14 text-right">errors</span>
                <span className="w-16 text-right">p95</span>
              </span>
            </div>
            {data.services.map((svc) => (
              <ServiceRow
                key={svc.name}
                service={svc}
                onClick={(name) =>
                  investigate(`Give me an overview of ${name} — key metrics, endpoints, and any concerns.`)
                }
              />
            ))}
          </div>
        )}

        {/* Firing alerts footer */}
        {data.alerts.length > 0 && (
          <div className="rounded-lg border border-unhealthy/15 bg-unhealthy/5 px-4 py-3">
            <div className="flex items-center gap-2 text-xs">
              <span className="text-unhealthy mono">
                {data.alerts.length} alert{data.alerts.length !== 1 ? "s" : ""} firing
              </span>
              <span className="text-muted-foreground mono">
                {data.alerts.map((a) => `${a.rule} (${a.service})`).join(", ")}
              </span>
            </div>
          </div>
        )}
      </div>
    </div>
  );
}
