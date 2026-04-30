import { useCallback, useEffect, useMemo, useState } from "react";
import { useLocation } from "react-router";
import { api, ApiError, setApiToken } from "@/api/client";
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

  const [alerts, setAlerts] = useState<Alert[]>([]);
  const [rules, setRules] = useState<Rule[]>([]);
  const [summary, setSummary] = useState<AlertSummary>({
    firing: 0,
    pending: 0,
    resolved: 0,
  });
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [staleSeconds, setStaleSeconds] = useState(0);
  const [lastFetch, setLastFetch] = useState(0);
  const loading = lastFetch === 0 && fetchError === null;

  useEffect(() => {
    if (token) setApiToken(token);
  }, [token]);

  const load = useCallback(async () => {
    try {
      const [alertsRes, rulesRes, summaryRes] = await Promise.all([
        api<Alert[]>("/api/alerts"),
        api<Rule[]>("/api/rules"),
        api<AlertSummary>("/api/alerts/summary"),
      ]);
      setAlerts(alertsRes ?? []);
      setRules(rulesRes ?? []);
      setSummary(summaryRes ?? { firing: 0, pending: 0, resolved: 0 });
      setFetchError(null);
      setLastFetch(Date.now());
      setStaleSeconds(0);
    } catch (err) {
      setFetchError(
        err instanceof ApiError
          ? `Failed to load: ${err.message}`
          : "Failed to load alerts",
      );
      console.error("Alerts fetch failed:", err);
    }
  }, []);

  useEffect(() => {
    // load() is async — every setState happens after an `await`. The
    // react-hooks/set-state-in-effect rule can't trace async functions, so
    // it flags this; a proper fix is migrating to TanStack Query's useQuery.
    // eslint-disable-next-line react-hooks/set-state-in-effect
    load();
    const interval = setInterval(load, REFRESH_INTERVAL);
    const staleTick = setInterval(() => {
      if (lastFetch > 0) {
        setStaleSeconds(Math.floor((Date.now() - lastFetch) / 1000));
      }
    }, 5_000);
    return () => {
      clearInterval(interval);
      clearInterval(staleTick);
    };
  }, [load, lastFetch]);

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
          <CreateRuleInput onCreated={load} />
          <RulesTable rules={rules} alerts={alerts} onRefresh={load} />
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
