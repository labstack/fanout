import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { useLocation } from "react-router";
import { api, ApiError, setApiToken } from "@/api/client";
import type { AlertRule, AlertInstance, AlertSummary } from "@/lib/types";
import { FiringAlerts } from "@/components/alerts/FiringAlerts";
import { AlertRulesTable } from "@/components/alerts/AlertRulesTable";
import { CreateRuleInput } from "@/components/alerts/CreateRuleInput";

const REFRESH_INTERVAL = 15_000;

export function AlertsPage() {
  const { search } = useLocation();
  const token = useMemo(
    () => new URLSearchParams(search).get("token") ?? undefined,
    [search],
  );

  const [alerts, setAlerts] = useState<AlertInstance[]>([]);
  const [rules, setRules] = useState<AlertRule[]>([]);
  const [summary, setSummary] = useState<AlertSummary>({ firing: 0, pending: 0, resolved: 0 });
  const [loading, setLoading] = useState(true);
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [staleSeconds, setStaleSeconds] = useState(0);
  const lastFetch = useRef(0);

  useEffect(() => {
    if (token) setApiToken(token);
  }, [token]);

  const load = useCallback(async () => {
    try {
      const [alertsRes, rulesRes, summaryRes] = await Promise.all([
        api<AlertInstance[]>("/api/alerts"),
        api<AlertRule[]>("/api/rules"),
        api<AlertSummary>("/api/alerts/summary"),
      ]);
      setAlerts(alertsRes ?? []);
      setRules(rulesRes ?? []);
      setSummary(summaryRes ?? { firing: 0, pending: 0, resolved: 0 });
      setLoading(false);
      setFetchError(null);
      lastFetch.current = Date.now();
      setStaleSeconds(0);
    } catch (err) {
      setLoading(false);
      setFetchError(
        err instanceof ApiError ? `Failed to load: ${err.message}` : "Failed to load alerts",
      );
      console.error("Alerts fetch failed:", err);
    }
  }, []);

  useEffect(() => {
    load();
    const interval = setInterval(load, REFRESH_INTERVAL);
    const staleTick = setInterval(() => {
      if (lastFetch.current > 0) {
        setStaleSeconds(Math.floor((Date.now() - lastFetch.current) / 1000));
      }
    }, 5_000);
    return () => {
      clearInterval(interval);
      clearInterval(staleTick);
    };
  }, [load]);

  if (loading && rules.length === 0) {
    return (
      <div className="px-4 py-6 sm:px-6">
        <div className="mx-auto max-w-5xl space-y-4">
          <div className="h-10 shimmer rounded-lg" />
          <div className="h-24 shimmer rounded-lg" />
          <div className="h-40 shimmer rounded-lg" />
        </div>
      </div>
    );
  }

  const isStale = staleSeconds >= 30;

  return (
    <div className="px-4 py-6 sm:px-6">
      <div className="mx-auto max-w-5xl space-y-4 fade-up">
        {/* Header */}
        <div className="flex items-center justify-between">
          <div className="flex items-center gap-3">
            <span className="font-heading text-xl font-bold text-foreground">Alerts</span>
            {summary.firing > 0 && (
              <span className="inline-flex rounded-full border border-unhealthy/20 bg-unhealthy/10 px-2.5 py-0.5 text-[10px] font-bold text-unhealthy">
                {summary.firing} firing
              </span>
            )}
            {summary.pending > 0 && (
              <span className="inline-flex rounded-full border border-degraded/20 bg-degraded/10 px-2.5 py-0.5 text-[10px] font-bold text-degraded">
                {summary.pending} pending
              </span>
            )}
          </div>
          <div className={`flex items-center gap-1.5 text-[11px] mono ${isStale ? "text-degraded/70" : "text-muted-foreground/50"}`}>
            {!isStale && <span className="inline-block w-1.5 h-1.5 rounded-full bg-healthy/60" />}
            {staleSeconds < 10 ? "Live" : staleSeconds < 30 ? `${staleSeconds}s ago` : `${Math.floor(staleSeconds / 60)}m ago`}
          </div>
        </div>

        {/* Error banner */}
        {fetchError && (
          <div className="rounded-lg border border-unhealthy/20 bg-unhealthy/5 px-4 py-3 text-sm text-unhealthy/90 mono">
            {fetchError}
          </div>
        )}

        {/* Firing alerts */}
        <FiringAlerts alerts={alerts} rules={rules} />

        {/* Rules */}
        <div className="space-y-3">
          <div className="flex items-center justify-between">
            <div className="detail-label">Rules ({rules.length})</div>
          </div>
          <CreateRuleInput onCreated={load} />
          <AlertRulesTable rules={rules} alerts={alerts} onRefresh={load} />
        </div>

        {/* Recent history */}
        {alerts.filter((a) => a.state === "resolved").length > 0 && (
          <div className="text-[11px] text-muted-foreground mono">
            {alerts.filter((a) => a.state === "resolved").length} resolved recently
          </div>
        )}
      </div>
    </div>
  );
}
