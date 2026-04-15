import { useEffect, useMemo, useState } from "react";
import { useNavigate, useLocation } from "react-router";
import { RefreshCw } from "lucide-react";
import { api, setApiToken } from "@/api/client";
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
  const [window, setWindow] = useState(60);

  useEffect(() => {
    if (token) setApiToken(token);
  }, [token]);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      try {
        const result = await api<HomeResponse>(`/api/home?window=${window}`);
        if (!cancelled) {
          setData(result);
          setLoading(false);
        }
      } catch {
        if (!cancelled) setLoading(false);
      }
    }

    load();
    const interval = setInterval(load, REFRESH_INTERVAL);
    return () => {
      cancelled = true;
      clearInterval(interval);
    };
  }, [window]);

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

  const healthyCount = data.services.length;
  const incidentCount = data.incidents.length;

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
                onClick={() => setWindow(opt.value)}
                className={`rounded-full px-3 py-1 text-[11px] mono transition-colors ${
                  window === opt.value
                    ? "bg-primary/12 text-foreground"
                    : "text-muted-foreground hover:text-foreground"
                }`}
              >
                {opt.label}
              </button>
            ))}
          </div>
          <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground mono">
            <RefreshCw className="h-3 w-3" />
            Auto-refresh
          </div>
        </div>

        {/* Summary header */}
        <SummaryHeader summary={data.summary} />

        {/* Incident cards */}
        {incidentCount > 0 && (
          <div className="space-y-3">
            {data.incidents.map((inc) => (
              <IncidentCard
                key={inc.service}
                incident={inc}
                onInvestigate={investigate}
              />
            ))}
          </div>
        )}

        {/* Healthy services */}
        {healthyCount > 0 && (
          <div className="rounded-xl border border-border/60 bg-surface-1/80 py-1">
            {incidentCount > 0 && (
              <div className="px-4 py-2 text-[11px] text-muted-foreground mono">
                {healthyCount} service{healthyCount !== 1 ? "s" : ""} healthy
              </div>
            )}
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
