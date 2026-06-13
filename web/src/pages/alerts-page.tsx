import { useEffect, useMemo } from "react";
import { useLocation } from "react-router";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { api, ApiError, setApiToken } from "@/api/client";
import { useFreshness } from "@/hooks/use-freshness";
import type { Rule, Alert, AlertSummary } from "@/lib/types";
import { FiringAlerts } from "@/components/alerts/firing-alerts";
import { RulesTable } from "@/components/alerts/rules-table";
import { CreateRuleInput } from "@/components/alerts/create-rule-input";
import { PageContainer } from "@/components/layout/page-container";
import { PageHeader } from "@/components/ui/page-header";
import { Badge } from "@/components/ui/badge";
import { Skeleton } from "@/components/ui/skeleton";
import { ErrorState } from "@/components/states/error-state";
import { cn } from "@/lib/utils";

const REFRESH_INTERVAL = 15_000;

export function AlertsPage() {
  const { search } = useLocation();
  const token = useMemo(
    () => new URLSearchParams(search).get("token") ?? undefined,
    [search],
  );

  useEffect(() => {
    if (token) setApiToken(token);
  }, [token]);

  const queryClient = useQueryClient();

  const alertsQuery = useQuery({
    queryKey: ["alerts"],
    queryFn: () => api<Alert[]>("/api/alerts"),
    refetchInterval: REFRESH_INTERVAL,
  });
  const rulesQuery = useQuery({
    queryKey: ["rules"],
    queryFn: () => api<Rule[]>("/api/rules"),
    refetchInterval: REFRESH_INTERVAL,
  });
  // Same key the nav badge polls — TanStack Query dedupes them into one request.
  const summaryQuery = useQuery({
    queryKey: ["alerts", "summary"],
    queryFn: () => api<AlertSummary>("/api/alerts/summary"),
    refetchInterval: REFRESH_INTERVAL,
  });

  const alerts = alertsQuery.data ?? [];
  const rules = rulesQuery.data ?? [];
  const summary = summaryQuery.data ?? { firing: 0, pending: 0, resolved: 0 };

  const loading =
    alertsQuery.isPending || rulesQuery.isPending || summaryQuery.isPending;
  const lastUpdated = Math.max(
    alertsQuery.dataUpdatedAt,
    rulesQuery.dataUpdatedAt,
    summaryQuery.dataUpdatedAt,
  );
  const staleSeconds = useFreshness(lastUpdated);
  const firstError = alertsQuery.error ?? rulesQuery.error ?? summaryQuery.error;
  const fetchError = firstError
    ? firstError instanceof ApiError
      ? `Failed to load: ${firstError.message}`
      : "Failed to load alerts"
    : null;

  // Invalidating the "alerts" prefix also refreshes ["alerts","summary"].
  const refresh = () => {
    queryClient.invalidateQueries({ queryKey: ["alerts"] });
    queryClient.invalidateQueries({ queryKey: ["rules"] });
  };

  if (loading && rules.length === 0) {
    return (
      <PageContainer>
        <div className="mx-auto max-w-5xl space-y-4">
          <Skeleton className="h-10" />
          <Skeleton className="h-24" />
          <Skeleton className="h-40" />
        </div>
      </PageContainer>
    );
  }

  const isStale = staleSeconds >= 30;

  const summaryActions = (
    <>
      {summary.firing > 0 && (
        <Badge variant="danger">{summary.firing} firing</Badge>
      )}
      {summary.pending > 0 && (
        <Badge variant="warning">{summary.pending} pending</Badge>
      )}
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
          : staleSeconds < 30
            ? `${staleSeconds}s ago`
            : `${Math.floor(staleSeconds / 60)}m ago`}
      </div>
    </>
  );

  return (
    <PageContainer>
      <div className="mx-auto max-w-5xl space-y-4">
        <PageHeader title="Alerts" actions={summaryActions} className="mb-2" />

        {fetchError && <ErrorState error={fetchError} />}

        <FiringAlerts alerts={alerts} rules={rules} />

        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <div className="detail-label">Rules ({rules.length})</div>
          </div>
          <CreateRuleInput onCreated={refresh} />
          <RulesTable rules={rules} alerts={alerts} onRefresh={refresh} />
        </div>

        {alerts.filter((a) => a.state === "resolved").length > 0 && (
          <div className="font-mono text-[11px] text-muted-foreground">
            {alerts.filter((a) => a.state === "resolved").length} resolved
            recently
          </div>
        )}
      </div>
    </PageContainer>
  );
}
