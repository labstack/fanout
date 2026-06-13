import { useEffect, useMemo, useState } from "react";
import { useNavigate, useLocation, useParams } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { api, ApiError, setApiToken } from "@/api/client";
import { useFreshness } from "@/hooks/use-freshness";
import type { ServiceDetailResponse } from "@/lib/types";
import { buildChatPath } from "@/lib/chat-route";
import { ServiceHeader } from "@/components/service/service-header";
import { MetricCards } from "@/components/service/metric-cards";
import { ServiceChart } from "@/components/service/service-chart";
import { EndpointTable } from "@/components/service/endpoint-table";
import { ErrorList } from "@/components/service/error-list";
import { DependencyList } from "@/components/service/dependency-list";
import { ChangePointList } from "@/components/service/change-points";
import { PageContainer } from "@/components/layout/page-container";
import { ErrorState } from "@/components/states/error-state";
import { Skeleton } from "@/components/ui/skeleton";
import { Button } from "@/components/ui/button";
import { cn } from "@/lib/utils";

const REFRESH_INTERVAL = 30_000;

const windowOptions = [
  { label: "15m", value: 15 },
  { label: "1h", value: 60 },
  { label: "6h", value: 360 },
  { label: "24h", value: 1440 },
];

export function ServicePage() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const { search } = useLocation();
  const token = useMemo(
    () => new URLSearchParams(search).get("token") ?? undefined,
    [search],
  );

  const [timeWindow, setTimeWindow] = useState(60);
  const namespace = useMemo(
    () => new URLSearchParams(search).get("namespace") ?? "",
    [search],
  );

  useEffect(() => {
    if (token) setApiToken(token);
  }, [token]);

  const { data, isPending, isError, error, dataUpdatedAt } = useQuery({
    queryKey: ["service", name, timeWindow, namespace],
    queryFn: () => {
      const params = new URLSearchParams();
      params.set("window", String(timeWindow));
      if (namespace) params.set("namespace", namespace);
      return api<ServiceDetailResponse>(
        `/api/services/${encodeURIComponent(name ?? "")}?${params}`,
      );
    },
    enabled: !!name,
    refetchInterval: REFRESH_INTERVAL,
  });

  const loading = isPending;
  const staleSeconds = useFreshness(dataUpdatedAt);
  const fetchError = isError
    ? error instanceof ApiError
      ? `Failed to load: ${error.message}`
      : "Failed to load service data"
    : null;

  const openChat = (prompt: string) => navigate(buildChatPath(prompt, token));

  if (loading && !data) {
    return (
      <PageContainer>
        <div className="mx-auto max-w-5xl space-y-4">
          <Skeleton className="h-10" />
          <Skeleton className="h-24" />
          <Skeleton className="h-40" />
          <Skeleton className="h-60" />
        </div>
      </PageContainer>
    );
  }

  if (!data && fetchError) {
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

  if (!data) return null;

  const d = data.diagnose;
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
            {staleSeconds < 10
              ? "Live"
              : staleSeconds < 60
                ? `${staleSeconds}s ago`
                : `${Math.floor(staleSeconds / 60)}m ago`}
          </div>
        </div>

        {fetchError && <ErrorState error={fetchError} />}

        <ServiceHeader
          name={d.service}
          status={d.status}
          symptom={d.symptom_detected}
          onInvestigate={() =>
            openChat(
              `Investigate ${d.service} — ${d.status}, error rate ${(d.error_rate * 100).toFixed(1)}%, p95 ${d.p95_ms.toFixed(0)}ms. What's the root cause?`,
            )
          }
        />

        <MetricCards
          errorRate={d.error_rate}
          p95Ms={d.p95_ms}
          p50Ms={d.p50_ms}
          spanCount={d.span_count}
          windowMinutes={d.window_minutes}
          baseline={d.comparison_to_baseline}
        />

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <ServiceChart
            title="Error Rate"
            buckets={data.buckets}
            metric="error_rate"
            color="var(--unhealthy)"
            changePoints={d.change_points}
          />
          <ServiceChart
            title="P95 Latency"
            buckets={data.buckets}
            metric="p95_ms"
            color="var(--degraded)"
            changePoints={d.change_points}
            baselineValue={d.comparison_to_baseline?.baseline_p95_ms}
          />
        </div>

        <EndpointTable
          endpoints={data.endpoints}
          windowMinutes={d.window_minutes}
          onClickEndpoint={(op) =>
            openChat(
              `Investigate the ${op} endpoint on ${d.service} — show me error traces and latency breakdown.`,
            )
          }
        />

        <div className="grid grid-cols-1 gap-4 sm:grid-cols-2">
          <ErrorList
            errors={d.top_errors}
            onClickTrace={(id) =>
              openChat(`Show me trace ${id} for ${d.service}.`)
            }
          />
          <DependencyList
            dependencies={d.dependencies}
            windowMinutes={d.window_minutes}
          />
        </div>

        {d.change_points && d.change_points.length > 0 && (
          <ChangePointList changePoints={d.change_points} />
        )}
      </div>
    </PageContainer>
  );
}
