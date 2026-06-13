import { Link, NavLink, useLocation, useNavigate } from "react-router";
import { useQuery } from "@tanstack/react-query";
import { Loader2, LogOut, Radio, RotateCcw, Settings } from "lucide-react";
import { useChatStore } from "@/stores/chat";
import { buildChatPath, buildDashboardPath } from "@/lib/chat-route";
import { useAuth } from "@/hooks/auth-context";
import { NamespacePicker } from "./namespace-picker";
import { api } from "@/api/client";
import type { AlertSummary } from "@/lib/types";

export function Nav() {
  const { pathname, search } = useLocation();
  const navigate = useNavigate();
  const token = new URLSearchParams(search).get("token") ?? undefined;
  const { user, logout, isAdmin } = useAuth();
  // Narrow selectors: subscribing to the whole store would re-render the nav on
  // every streamed chat token.
  const streaming = useChatStore((s) => s.streaming);
  const hasMessages = useChatStore((s) => s.messages.length > 0);
  const clear = useChatStore((s) => s.clear);
  const isChatRoute = pathname === "/chat";

  // Shares its cache entry with the alerts page (same key) — one poll, not two.
  const { data: summary, isError: summaryError } = useQuery({
    queryKey: ["alerts", "summary"],
    queryFn: () => api<AlertSummary>("/api/alerts/summary"),
    refetchInterval: 30_000,
  });
  const firingCount = summary?.firing ?? 0;

  return (
    <nav className="border-b border-border/50 px-6 h-12 flex items-center justify-between backdrop-blur-sm sticky top-0 z-50 bg-surface/80">
      <div className="flex items-center gap-6">
        <Link to={buildDashboardPath(token)} className="flex items-center gap-2.5 group">
          <Radio className="h-4 w-4 text-primary opacity-80 group-hover:opacity-100 transition-opacity" />
          <span className="text-sm font-semibold tracking-wide uppercase mono text-foreground/90 group-hover:text-foreground transition-colors">
            Fanout
          </span>
        </Link>
        <div className="flex items-center gap-4">
          <NavLink
            to={`/alerts${search}`}
            className={({ isActive }) =>
              `text-xs mono transition-colors flex items-center gap-1.5 ${
                isActive ? "text-foreground" : "text-zinc-400 hover:text-zinc-200"
              }`
            }
          >
            Alerts
            {summaryError ? (
              <span
                title="Alert status unavailable"
                className="inline-flex items-center justify-center min-w-[16px] h-[16px] rounded-full bg-surface-3 text-muted-foreground text-[9px] font-bold px-1"
              >
                !
              </span>
            ) : firingCount > 0 ? (
              <span className="inline-flex items-center justify-center min-w-[16px] h-[16px] rounded-full bg-unhealthy text-white text-[9px] font-bold px-1">
                {firingCount}
              </span>
            ) : null}
          </NavLink>
          <NavLink
            to={buildChatPath(undefined, token)}
            className={({ isActive }) =>
              `text-xs mono transition-colors ${
                isActive ? "text-foreground" : "text-zinc-400 hover:text-zinc-200"
              }`
            }
          >
            Chat
          </NavLink>
        </div>
      </div>
      <div className="flex items-center gap-3">
        <NamespacePicker />
        {streaming && (
          <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground mono">
            <Loader2 className="h-3 w-3 animate-spin" />
            Streaming
          </div>
        )}
        {hasMessages && isChatRoute && (
          <button
            onClick={clear}
            className="flex items-center gap-1.5 text-[11px] text-muted-foreground hover:text-foreground transition-colors px-2.5 py-1.5 rounded-md hover:bg-surface-2 mono"
          >
            <RotateCcw className="h-3 w-3" />
            <span className="hidden sm:inline">New chat</span>
          </button>
        )}
        {user ? (
          <>
            {isAdmin && (
              <NavLink
                to="/settings"
                title="Settings"
                className={({ isActive }) =>
                  `flex items-center text-[11px] transition-colors ${
                    isActive ? "text-foreground" : "text-zinc-400 hover:text-zinc-200"
                  }`
                }
              >
                <Settings className="h-3.5 w-3.5" />
              </NavLink>
            )}
            <button
              onClick={async () => {
                await logout();
                navigate("/login");
              }}
              className="flex items-center gap-1.5 text-[11px] text-muted-foreground hover:text-foreground transition-colors mono"
            >
              <span className="hidden sm:inline">{user.email}</span>
              <LogOut className="h-3 w-3" />
            </button>
          </>
        ) : (
          <Link to="/login" className="text-xs text-zinc-400 hover:text-zinc-200 transition-colors mono">
            Sign in
          </Link>
        )}
      </div>
    </nav>
  );
}
