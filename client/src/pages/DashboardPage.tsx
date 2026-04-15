import { useEffect, useMemo, useState } from "react";
import { Link, useLocation, useNavigate } from "react-router";
import {
  ArrowRight,
  BellRing,
  Bot,
  ChevronRight,
  Database,
  HeartPulse,
  Radar,
  Sparkles,
} from "lucide-react";
import { api, setApiToken } from "@/api/client";
import type { Bookmark } from "@/lib/types";
import { buildChatPath } from "@/lib/chat-route";
import { AIActionBar, type AIAction } from "@/components/ai/AIActionBar";

interface HealthCheck {
  status: string;
  latency_ms?: number;
  error?: string;
  detail?: string;
  updated_at?: string;
}

interface HealthResponse {
  status: string;
  checks: Record<string, HealthCheck>;
}

const FALLBACK_SUGGESTIONS = [
  "Give me a 30-second brief on current runtime health.",
  "What should I investigate first right now?",
  "Explain whether anything looks stale or degraded in the platform.",
  "Review the riskiest service and tell me the next action.",
];

function statusClasses(status: string) {
  switch (status) {
    case "ok":
    case "ready":
      return "border-emerald-500/20 bg-emerald-500/10 text-emerald-300";
    case "degraded":
      return "border-amber-500/20 bg-amber-500/10 text-amber-200";
    case "unhealthy":
      return "border-red-500/20 bg-red-500/10 text-red-300";
    default:
      return "border-border bg-surface-2 text-muted-foreground";
  }
}

function formatStatus(status: string) {
  if (!status) return "Unknown";
  return status[0].toUpperCase() + status.slice(1);
}

function formatBookmarkTime(value: string) {
  const date = new Date(value);
  if (Number.isNaN(date.getTime())) return value;
  return date.toLocaleString("en-US", {
    month: "short",
    day: "numeric",
    hour: "2-digit",
    minute: "2-digit",
  });
}

export function DashboardPage() {
  const navigate = useNavigate();
  const { search } = useLocation();
  const token = useMemo(
    () => new URLSearchParams(search).get("token") ?? undefined,
    [search],
  );

  const [health, setHealth] = useState<HealthResponse | null>(null);
  const [healthError, setHealthError] = useState<string | null>(null);
  const [suggestions, setSuggestions] = useState<string[]>(FALLBACK_SUGGESTIONS);
  const [bookmarks, setBookmarks] = useState<Bookmark[]>([]);
  const [loading, setLoading] = useState(true);

  useEffect(() => {
    if (token) setApiToken(token);
  }, [token]);

  useEffect(() => {
    let cancelled = false;

    async function load() {
      setLoading(true);
      const [healthRes, suggestionsRes, bookmarksRes] = await Promise.allSettled([
        api<HealthResponse>("/api/health"),
        api<string[]>("/api/suggestions"),
        api<Bookmark[]>("/api/bookmarks"),
      ]);

      if (cancelled) return;

      if (healthRes.status === "fulfilled") {
        setHealth(healthRes.value);
        setHealthError(null);
      } else {
        setHealth(null);
        setHealthError(healthRes.reason instanceof Error ? healthRes.reason.message : "Failed to load health");
      }

      if (suggestionsRes.status === "fulfilled" && suggestionsRes.value.length > 0) {
        setSuggestions(suggestionsRes.value);
      } else {
        setSuggestions(FALLBACK_SUGGESTIONS);
      }

      if (bookmarksRes.status === "fulfilled") {
        setBookmarks(bookmarksRes.value.slice(0, 4));
      } else {
        setBookmarks([]);
      }

      setLoading(false);
    }

    load();
    return () => {
      cancelled = true;
    };
  }, [token]);

  const openPrompt = (prompt: string) => {
    navigate(buildChatPath(prompt, token));
  };

  const quickActions = useMemo<AIAction[]>(() => {
    const healthStatus = health?.status ?? "unknown";
    return [
      {
        label: "30-second brief",
        prompt: "Give me a 30-second brief on current system health and the top priority.",
        kind: "explain",
      },
      {
        label: "Find biggest risk",
        prompt:
          healthStatus === "unhealthy"
            ? "Explain the unhealthy runtime checks and tell me the first thing to fix."
            : "What is the biggest operational risk right now, even if the platform looks healthy?",
        kind: "drill",
      },
      {
        label: "Alert review",
        prompt: "Recommend the next alert I should create based on the current system state.",
        kind: "alert",
      },
    ];
  }, [health?.status]);

  const spotlightCheck = useMemo(() => {
    if (!health) return null;
    return Object.entries(health.checks).find(([, check]) => check.status !== "ok") ?? null;
  }, [health]);

  return (
    <div className="px-4 py-6 sm:px-6">
      <div className="mx-auto max-w-6xl space-y-6">
        <section className="overflow-hidden rounded-[28px] border border-primary/15 bg-[radial-gradient(circle_at_top_left,rgba(96,165,250,0.18),transparent_28%),linear-gradient(180deg,rgba(18,18,21,0.98),rgba(9,9,11,0.98))] px-6 py-7 shadow-[0_22px_80px_-40px_rgba(96,165,250,0.45)] fade-up">
          <div className="grid gap-6 lg:grid-cols-[1.6fr_1fr] lg:items-end">
            <div className="space-y-4">
              <div className="inline-flex items-center gap-2 rounded-full border border-primary/20 bg-primary/10 px-3 py-1 text-[11px] uppercase tracking-[0.22em] text-primary/85">
                <Radar className="h-3.5 w-3.5" />
                AI Command Center
              </div>
              <div className="space-y-3">
                <h1 className="max-w-3xl font-heading text-3xl font-semibold tracking-tight text-foreground sm:text-4xl">
                  Move from chat-only triage to a dashboard that suggests the next investigation.
                </h1>
                <p className="max-w-2xl text-sm leading-7 text-muted-foreground sm:text-[15px]">
                  The dashboard now gives you a runtime health snapshot, AI launch prompts, and visible
                  per-block actions that route straight into investigation chat.
                </p>
              </div>
              <div className="flex flex-wrap gap-3">
                <Link
                  to={buildChatPath(undefined, token)}
                  className="btn-primary inline-flex items-center gap-2"
                >
                  <Bot className="h-4 w-4" />
                  Open chat
                </Link>
                <button
                  type="button"
                  onClick={() => openPrompt("Give me a 30-second brief on current runtime health.")}
                  className="btn-ghost inline-flex items-center gap-2"
                >
                  <Sparkles className="h-4 w-4" />
                  Brief me
                </button>
              </div>
              <AIActionBar actions={quickActions} onAction={openPrompt} className="max-w-3xl bg-black/10" />
            </div>

            <div className="rounded-[24px] border border-white/6 bg-black/20 p-5 backdrop-blur-sm">
              <div className="mb-4 flex items-center justify-between">
                <span className="mono text-[11px] uppercase tracking-[0.18em] text-muted-foreground">
                  Runtime status
                </span>
                <span className={`inline-flex rounded-full border px-2.5 py-1 text-[11px] ${statusClasses(health?.status ?? "unknown")}`}>
                  {formatStatus(health?.status ?? "unknown")}
                </span>
              </div>
              {loading ? (
                <div className="space-y-3">
                  <div className="h-8 w-24 shimmer" />
                  <div className="h-16 shimmer" />
                  <div className="h-16 shimmer" />
                </div>
              ) : healthError ? (
                <div className="rounded-xl border border-red-500/15 bg-red-500/8 p-4 text-sm text-red-200">
                  {healthError}
                </div>
              ) : (
                <div className="space-y-3">
                  {Object.entries(health?.checks ?? {}).map(([name, check]) => (
                    <button
                      key={name}
                      type="button"
                      onClick={() =>
                        openPrompt(
                          `Explain the ${name} runtime check, its current ${check.status} state, and whether I should act on it now.`,
                        )
                      }
                      className="w-full rounded-xl border border-white/6 bg-surface/55 p-3 text-left transition-colors hover:border-primary/20 hover:bg-surface-1"
                    >
                      <div className="mb-1 flex items-center justify-between gap-3">
                        <span className="font-heading text-sm font-medium text-foreground">{name}</span>
                        <span className={`inline-flex rounded-full border px-2 py-0.5 text-[10px] ${statusClasses(check.status)}`}>
                          {check.status}
                        </span>
                      </div>
                      <div className="flex flex-wrap items-center gap-x-3 gap-y-1 text-[12px] text-muted-foreground">
                        {check.detail && <span>{check.detail}</span>}
                        {typeof check.latency_ms === "number" && <span>{check.latency_ms}ms</span>}
                        {check.error && <span className="text-red-300">{check.error}</span>}
                      </div>
                    </button>
                  ))}
                </div>
              )}
            </div>
          </div>
        </section>

        <div className="grid gap-6 xl:grid-cols-[1.3fr_1fr]">
          <section className="rounded-[24px] border border-border/60 bg-surface-1/80 p-5">
            <div className="mb-5 flex items-start justify-between gap-4">
              <div>
                <h2 className="font-heading text-xl font-semibold text-foreground">Suggested investigations</h2>
                <p className="mt-1 text-sm text-muted-foreground">
                  Start from a useful prompt instead of a blank box.
                </p>
              </div>
              <Link
                to={buildChatPath(undefined, token)}
                className="inline-flex items-center gap-2 rounded-full border border-border/70 px-3 py-1.5 text-[11px] uppercase tracking-[0.18em] text-muted-foreground transition-colors hover:border-primary/20 hover:text-foreground"
              >
                Full chat
                <ArrowRight className="h-3.5 w-3.5" />
              </Link>
            </div>
            <div className="grid gap-3 md:grid-cols-2">
              {suggestions.map((prompt, index) => (
                <button
                  key={`${prompt}:${index}`}
                  type="button"
                  onClick={() => openPrompt(prompt)}
                  className="group rounded-2xl border border-border/60 bg-surface px-4 py-4 text-left transition-colors hover:border-primary/20 hover:bg-surface-2"
                >
                  <div className="mb-4 flex items-center justify-between">
                    <span className="mono text-[10px] uppercase tracking-[0.18em] text-muted-foreground">
                      Prompt {index + 1}
                    </span>
                    <ChevronRight className="h-4 w-4 text-muted-foreground/50 transition-transform group-hover:translate-x-0.5 group-hover:text-primary" />
                  </div>
                  <p className="text-sm leading-6 text-foreground/90">{prompt}</p>
                </button>
              ))}
            </div>
          </section>

          <section className="space-y-6">
            <div className="rounded-[24px] border border-border/60 bg-surface-1/80 p-5">
              <div className="mb-4 flex items-center gap-2">
                <HeartPulse className="h-4 w-4 text-primary" />
                <h2 className="font-heading text-lg font-semibold text-foreground">Watchtower</h2>
              </div>
              {spotlightCheck ? (
                <button
                  type="button"
                  onClick={() =>
                    openPrompt(
                      `Explain why the ${spotlightCheck[0]} check is ${spotlightCheck[1].status}, what it means, and what to do next.`,
                    )
                  }
                  className="w-full rounded-2xl border border-primary/15 bg-primary/8 p-4 text-left transition-colors hover:bg-primary/12"
                >
                  <div className="mb-2 flex items-center justify-between gap-3">
                    <span className="font-heading text-base font-medium text-foreground">
                      {spotlightCheck[0]}
                    </span>
                    <span className={`inline-flex rounded-full border px-2 py-0.5 text-[10px] ${statusClasses(spotlightCheck[1].status)}`}>
                      {spotlightCheck[1].status}
                    </span>
                  </div>
                  <p className="text-sm leading-6 text-muted-foreground">
                    {spotlightCheck[1].detail ?? spotlightCheck[1].error ?? "This check deserves immediate explanation."}
                  </p>
                </button>
              ) : (
                <div className="rounded-2xl border border-emerald-500/10 bg-emerald-500/6 p-4 text-sm text-emerald-100/90">
                  Nothing is screaming right now. Use the AI actions below the charts to keep the next drill-down tight.
                </div>
              )}
            </div>

            <div className="rounded-[24px] border border-border/60 bg-surface-1/80 p-5">
              <div className="mb-4 flex items-center gap-2">
                <Database className="h-4 w-4 text-primary" />
                <h2 className="font-heading text-lg font-semibold text-foreground">Recent investigations</h2>
              </div>
              {bookmarks.length === 0 ? (
                <div className="rounded-2xl border border-border/60 bg-surface px-4 py-4 text-sm text-muted-foreground">
                  No saved investigations yet. Use the dashboard and chat actions to build the first set.
                </div>
              ) : (
                <div className="space-y-3">
                  {bookmarks.map((bookmark) => (
                    <button
                      key={bookmark.id}
                      type="button"
                      onClick={() => openPrompt(bookmark.question)}
                      className="w-full rounded-2xl border border-border/60 bg-surface px-4 py-3 text-left transition-colors hover:border-primary/20 hover:bg-surface-2"
                    >
                      <div className="mb-1 flex items-center justify-between gap-3">
                        <span className="font-heading text-sm font-medium text-foreground">{bookmark.question}</span>
                        <BellRing className="h-3.5 w-3.5 text-muted-foreground/50" />
                      </div>
                      <div className="text-[12px] text-muted-foreground">{formatBookmarkTime(bookmark.created_at)}</div>
                    </button>
                  ))}
                </div>
              )}
            </div>
          </section>
        </div>
      </div>
    </div>
  );
}
