import { useEffect, useMemo, useRef, useState } from "react";
import { useNavigate, useLocation, useParams } from "react-router";
import { api, ApiError, setApiToken } from "@/api/client";
import type { ServiceDetailResponse } from "@/lib/types";
import { buildChatPath } from "@/lib/chat-route";
import { ServiceHeader } from "@/components/service/ServiceHeader";
import { MetricCards } from "@/components/service/MetricCards";
import { ServiceChart } from "@/components/service/ServiceChart";
import { EndpointTable } from "@/components/service/EndpointTable";
import { ErrorList } from "@/components/service/ErrorList";
import { DependencyList } from "@/components/service/DependencyList";
import { ChangePointList } from "@/components/service/ChangePoints";

const REFRESH_INTERVAL = 30_000;

export function ServicePage() {
  const { name } = useParams<{ name: string }>();
  const navigate = useNavigate();
  const { search } = useLocation();
  const token = useMemo(
    () => new URLSearchParams(search).get("token") ?? undefined,
    [search],
  );

  const [data, setData] = useState<ServiceDetailResponse | null>(null);
  const [loading, setLoading] = useState(true);
  const [timeWindow, setTimeWindow] = useState(60);
  const [fetchError, setFetchError] = useState<string | null>(null);
  const [staleSeconds, setStaleSeconds] = useState(0);
  const lastFetch = useRef(0);

  useEffect(() => {
    if (token) setApiToken(token);
  }, [token]);

  useEffect(() => {
    if (!name) return;
    let cancelled = false;
    setLoading(true);

    async function load() {
      try {
        const result = await api<ServiceDetailResponse>(
          `/api/service/${encodeURIComponent(name!)}?window=${timeWindow}`,
        );
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
          setFetchError(
            err instanceof ApiError ? `Failed to load: ${err.message}` : "Failed to load service data",
          );
          console.error("Service detail fetch failed:", err);
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
  }, [name, timeWindow]);

  const openChat = (prompt: string) => navigate(buildChatPath(prompt, token));

  const windowOptions = [
    { label: "15m", value: 15 },
    { label: "1h", value: 60 },
    { label: "6h", value: 360 },
    { label: "24h", value: 1440 },
  ];

  if (loading && !data) {
    return (
      <div className="px-4 py-6 sm:px-6">
        <div className="mx-auto max-w-5xl space-y-4">
          <div className="h-10 shimmer rounded-lg" />
          <div className="h-24 shimmer rounded-lg" />
          <div className="h-40 shimmer rounded-lg" />
          <div className="h-60 shimmer rounded-lg" />
        </div>
      </div>
    );
  }

  if (!data && fetchError) {
    return (
      <div className="flex items-center justify-center min-h-[60vh]">
        <div className="text-center max-w-md space-y-4 fade-up">
          <div className="rounded-lg border border-unhealthy/20 bg-unhealthy/5 px-5 py-4 text-sm text-unhealthy/90 mono">
            {fetchError}
          </div>
          <button type="button" onClick={() => globalThis.location.reload()} className="btn-ghost text-xs">
            Retry
          </button>
        </div>
      </div>
    );
  }

  if (!data) return null;

  const d = data.diagnose;
  const isStale = staleSeconds >= 60;

  return (
    <div className="px-4 py-6 sm:px-6">
      <div className="mx-auto max-w-5xl space-y-4 fade-up">
        {/* Time range + freshness */}
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
          <div className={`flex items-center gap-1.5 text-[11px] mono ${isStale ? "text-degraded/70" : "text-muted-foreground/50"}`}>
            {!isStale && <span className="inline-block w-1.5 h-1.5 rounded-full bg-healthy/60" />}
            {staleSeconds < 10 ? "Live" : staleSeconds < 60 ? `${staleSeconds}s ago` : `${Math.floor(staleSeconds / 60)}m ago`}
          </div>
        </div>

        {fetchError && (
          <div className="rounded-lg border border-unhealthy/20 bg-unhealthy/5 px-4 py-3 text-sm text-unhealthy/90 mono">
            {fetchError}
          </div>
        )}

        <ServiceHeader
          name={d.service}
          status={d.status}
          symptom={d.symptom_detected}
          onInvestigate={() =>
            openChat(`Investigate ${d.service} — ${d.status}, error rate ${(d.error_rate * 100).toFixed(1)}%, p95 ${d.p95_ms.toFixed(0)}ms. What's the root cause?`)
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

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
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
            openChat(`Investigate the ${op} endpoint on ${d.service} — show me error traces and latency breakdown.`)
          }
        />

        <div className="grid grid-cols-1 sm:grid-cols-2 gap-4">
          <ErrorList
            errors={d.top_errors}
            onClickTrace={(id) => openChat(`Show me trace ${id} for ${d.service}.`)}
          />
          <DependencyList dependencies={d.dependencies} windowMinutes={d.window_minutes} />
        </div>

        {d.change_points && d.change_points.length > 0 && (
          <ChangePointList changePoints={d.change_points} />
        )}
      </div>
    </div>
  );
}
