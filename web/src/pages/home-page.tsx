import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useLocation } from "react-router";
import { api, ApiError, setApiToken } from "@/api/client";
import type { OverviewResponse } from "@/lib/types";
import { buildChatPath } from "@/lib/chat-route";
import { EmptyState } from "@/components/home/empty-state";
import { SummaryHeader } from "@/components/home/summary-header";
import { IncidentCard } from "@/components/home/incident-card";
import { ServiceRow } from "@/components/home/service-row";
import { PageContainer } from "@/components/layout/page-container";
import { ErrorState } from "@/components/states/error-state";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

const REFRESH_INTERVAL = 30_000;
const MAX_EXPANDED_CARDS = 2;

function freshnessLabel(seconds: number): string {
  if (seconds < 10) return "Live";
  if (seconds < 60) return `${seconds}s ago`;
  return `${Math.floor(seconds / 60)}m ago`;
}

export function HomePage() {
  const navigate = useNavigate();
  const { search } = useLocation();
  const token = useMemo(
    () => new URLSearchParams(search).get("token") ?? undefined,
    [search],
  );

  const [data, setData] = useState<OverviewResponse | null>(null);
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
        const params = new URLSearchParams();
        params.set("window", String(timeWindow));
        const ns = new URLSearchParams(search).get("namespace");
        if (ns) params.set("namespace", ns);
        const result = await api<OverviewResponse>(`/api/overview?${params}`);
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
          const message =
            err instanceof ApiError
              ? `Failed to load: ${err.message}`
              : "Failed to load overview data";
          setFetchError(message);
          console.error("Overview fetch failed:", err);
        }
      }
    }

    load();
    const interval = setInterval(load, REFRESH_INTERVAL);

    const staleTick = setInterval(() => {
      if (lastFetch.current > 0) {
        setStaleSeconds(Math.floor((Date.now() - lastFetch.current) / 1000));
      }
    }, 5_000);

    return () => {
      cancelled = true;
      clearInterval(interval);
      clearInterval(staleTick);
    };
  }, [timeWindow, search]);

  const investigate = (prompt: string) => {
    navigate(buildChatPath(prompt, token));
  };

  const windowOptions = [
    { label: "15m", value: 15 },
    { label: "1h", value: 60 },
    { label: "6h", value: 360 },
    { label: "24h", value: 1440 },
  ];

  if (!loading && !data && fetchError) {
    return (
      <PageContainer>
        <div className="mx-auto max-w-md py-12">
          <ErrorState
            error={fetchError}
            resetErrorBoundary={() => globalThis.location.reload()}
          />
        </div>
      </PageContainer>
    );
  }

  if (!loading && (!data || data.health.total_services === 0)) {
    return <EmptyState />;
  }

  if (loading && !data) {
    return (
      <PageContainer>
        <div className="mx-auto max-w-5xl space-y-4">
          <Skeleton className="h-14" />
          <Skeleton className="h-40 rounded-xl" />
          <Skeleton className="h-10" />
          <Skeleton className="h-10" />
        </div>
      </PageContainer>
    );
  }

  if (!data) return null;

  // Defense-in-depth defaults. The server contract (no `omitempty` on these
  // fields in internal/service/types.go + handler-level non-nil init in
  // internal/api/ui.go) guarantees these always arrive as arrays, and the TS
  // type declares them required — but we keep the guard so a future regression
  // (someone reintroduces `omitempty`, or a refactor drops the handler init)
  // can't crash the home page on `.length`/`.filter`.
  const { incidents = [], services = [], alerts = [] } = data;

  const unhealthy = incidents.filter((i) => i.status === "unhealthy");
  const degraded = incidents.filter((i) => i.status === "degraded");
  const incidentNames = new Set(incidents.map((i) => i.service));
  const healthyServices = services.filter(
    (s) => !incidentNames.has(s.service),
  );
  const healthyCount = healthyServices.length;
  const hasIncidents = incidents.length > 0;

  const expandedUnhealthy = unhealthy.slice(0, MAX_EXPANDED_CARDS);
  const collapsedUnhealthy = unhealthy.slice(MAX_EXPANDED_CARDS);

  const isStale = staleSeconds >= 60;

  return (
    <PageContainer>
      <div className="mx-auto max-w-5xl space-y-4">
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-1 rounded-full border border-border/60 bg-surface-1/70 p-1">
            {windowOptions.map((opt) => (
              <Button
                key={opt.value}
                type="button"
                variant="ghost"
                size="sm"
                onClick={() => setTimeWindow(opt.value)}
                className={cn(
                  "h-auto rounded-full px-3 py-1 font-mono text-[11px]",
                  timeWindow === opt.value
                    ? "bg-primary/15 text-foreground hover:bg-primary/15"
                    : "text-muted-foreground hover:text-foreground",
                )}
              >
                {opt.label}
              </Button>
            ))}
          </div>
          <div
            className={cn(
              "flex items-center gap-1.5 font-mono text-[11px]",
              isStale ? "text-warning/70" : "text-muted-foreground/50",
            )}
          >
            {!isStale && (
              <span
                aria-hidden="true"
                className="inline-block size-1.5 rounded-full bg-success/60"
              />
            )}
            {freshnessLabel(staleSeconds)}
          </div>
        </div>

        {fetchError && <ErrorState error={fetchError} />}

        <SummaryHeader health={data.health} />

        {expandedUnhealthy.length > 0 && (
          <div className="space-y-3">
            {expandedUnhealthy.map((inc, i) => (
              <IncidentCard
                key={inc.service}
                incident={inc}
                onInvestigate={investigate}
                primary={i === 0}
              />
            ))}
          </div>
        )}

        {(collapsedUnhealthy.length > 0 || degraded.length > 0) && (
          <div className="space-y-1.5">
            {collapsedUnhealthy.length > 0 && (
              <>
                <div className="px-1 font-mono text-[11px] text-muted-foreground">
                  {collapsedUnhealthy.length} more unhealthy
                </div>
                {collapsedUnhealthy.map((inc) => (
                  <IncidentCard
                    key={inc.service}
                    incident={inc}
                    onInvestigate={investigate}
                    compact
                  />
                ))}
              </>
            )}
            {degraded.length > 0 && (
              <>
                <div className="px-1 font-mono text-[11px] text-muted-foreground">
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
              </>
            )}
          </div>
        )}

        {healthyCount > 0 && (
          <div className="rounded-xl border border-border/60 bg-surface-1/80 py-1">
            <div className="flex items-center gap-4 px-4 py-2">
              <span className="font-mono text-[11px] text-muted-foreground">
                {hasIncidents
                  ? `${healthyCount} service${healthyCount !== 1 ? "s" : ""} healthy`
                  : `${healthyCount} services`}
              </span>
              <span className="ml-auto flex items-center gap-4 font-mono text-[10px] uppercase tracking-wider text-muted-foreground/60">
                <span className="w-24 text-right">traffic</span>
                <span className="w-14 text-right">errors</span>
                <span className="w-16 text-right">p95</span>
              </span>
            </div>
            {healthyServices.map((svc) => (
              <ServiceRow key={svc.service} service={svc} />
            ))}
          </div>
        )}

        {alerts.length > 0 && (
          <div className="rounded-lg border border-danger/15 bg-danger/5 px-4 py-3">
            <div className="flex items-center gap-2 text-xs">
              <span className="font-mono text-danger">
                {alerts.length} alert
                {alerts.length !== 1 ? "s" : ""} firing
              </span>
              <span className="font-mono text-muted-foreground">
                {alerts.map((a) => `${a.rule} (${a.service})`).join(", ")}
              </span>
            </div>
          </div>
        )}
      </div>
    </PageContainer>
  );
}
