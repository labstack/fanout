import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useLocation } from "react-router";
import { api, ApiError, setApiToken } from "@/api/client";
import type { OverviewResponse } from "@/lib/types";
import { buildChatPath } from "@/lib/chat-route";
import { EmptyState } from "@/components/home/empty-state";
import { SummaryHeader } from "@/components/home/summary-header";
import { IncidentCard } from "@/components/home/incident-card";
import { ActivityChart } from "@/components/home/activity-chart";
import { ServiceHeatmap } from "@/components/home/service-heatmap";
import { RecentErrors } from "@/components/home/recent-errors";
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
        <div className="mx-auto max-w-6xl space-y-4">
          <Skeleton className="h-14" />
          <Skeleton className="h-40 rounded-xl" />
          <Skeleton className="h-10" />
          <Skeleton className="h-10" />
        </div>
      </PageContainer>
    );
  }

  if (!data) return null;

  // Defense-in-depth: server guarantees these are present; defaults keep
  // .length/.filter safe if that contract ever regresses. The alerts default
  // is `unavailable` (not `disabled`) — "we can't tell you" is honest about
  // a server regression; `disabled` would be a positive assertion that hides it.
  const {
    incidents = [],
    services = [],
    alerts = { status: "unavailable" as const, items: [] },
    recent_errors: recentErrors = [],
    activity = { buckets: [] },
  } = data;

  const unhealthy = incidents.filter((i) => i.status === "unhealthy");
  const degraded = incidents.filter((i) => i.status === "degraded");

  const expandedUnhealthy = unhealthy.slice(0, MAX_EXPANDED_CARDS);
  const collapsedUnhealthy = unhealthy.slice(MAX_EXPANDED_CARDS);

  const isStale = staleSeconds >= 60;

  return (
    <PageContainer>
      <div className="mx-auto max-w-6xl space-y-4">
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

        <div className="grid grid-cols-1 gap-4 lg:grid-cols-[1fr_340px]">
          {/* Main column — activity, incidents, fleet heatmap */}
          <div className="min-w-0 space-y-4">
            <ActivityChart buckets={activity.buckets} />

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

            <ServiceHeatmap services={services} />
          </div>

          {/* Right rail — alerts + recent errors (sticky on desktop) */}
          <div className="space-y-4 lg:sticky lg:top-16 lg:self-start">
            {alerts.status === "unavailable" && (
              <div className="rounded-lg border border-warning/30 bg-warning/10 px-4 py-3 text-xs">
                <span className="font-mono text-warning">
                  <strong>Alerts data temporarily unavailable.</strong> Retrying
                  automatically.
                </span>
              </div>
            )}

            {alerts.status === "ok" && alerts.items.length > 0 && (
              <div className="rounded-xl border border-danger/15 bg-danger/5 p-4">
                <div className="font-mono text-xs text-danger">
                  {alerts.items.length} alert
                  {alerts.items.length !== 1 ? "s" : ""} firing
                </div>
                <div className="mt-1 space-y-0.5 font-mono text-[11px] text-muted-foreground">
                  {alerts.items.map((a) => (
                    <div key={`${a.rule}:${a.service}`} className="truncate">
                      {a.rule} ({a.service})
                    </div>
                  ))}
                </div>
              </div>
            )}

            {alerts.status === "ok" && alerts.items.length === 0 && (
              <div className="rounded-xl border border-border/60 bg-surface-1/80 p-4 text-[12px] text-muted-foreground">
                No alerts firing
              </div>
            )}

            <RecentErrors errors={recentErrors} />
          </div>
        </div>
      </div>
    </PageContainer>
  );
}
