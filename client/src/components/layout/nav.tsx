import { useEffect, useState } from "react";
import { Link, NavLink, useLocation, useNavigate } from "react-router";
import { Loader2, LogOut, Radio, RotateCcw } from "lucide-react";
import { useChatStore } from "@/stores/chat";
import { buildChatPath, buildDashboardPath } from "@/lib/chat-route";
import { useAuth } from "@/hooks/use-auth";
import { NamespacePicker } from "./namespace-picker";
import { api } from "@/api/client";
import type { AlertSummary } from "@/lib/types";

export function Nav() {
  const { pathname, search } = useLocation();
  const navigate = useNavigate();
  const token = new URLSearchParams(search).get("token") ?? undefined;
  const { user, logout } = useAuth();
  const { streaming, messages, clear } = useChatStore();
  const hasMessages = messages.length > 0;
  const isChatRoute = pathname === "/chat";
  const [firingCount, setFiringCount] = useState(0);

  useEffect(() => {
    let cancelled = false;
    async function load() {
      try {
        const s = await api<AlertSummary>("/api/alerts/summary");
        if (!cancelled) setFiringCount(s.firing);
      } catch { /* ignore */ }
    }
    load();
    const interval = setInterval(load, 30_000);
    return () => { cancelled = true; clearInterval(interval); };
  }, []);

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
            to={buildDashboardPath(token)}
            end
            className={({ isActive }) =>
              `text-xs mono transition-colors ${
                isActive || pathname.startsWith("/service/")
                  ? "text-foreground"
                  : "text-zinc-400 hover:text-zinc-200"
              }`
            }
          >
            Home
          </NavLink>
          <NavLink
            to={`/alerts${search}`}
            className={({ isActive }) =>
              `text-xs mono transition-colors flex items-center gap-1.5 ${
                isActive ? "text-foreground" : "text-zinc-400 hover:text-zinc-200"
              }`
            }
          >
            Alerts
            {firingCount > 0 && (
              <span className="inline-flex items-center justify-center min-w-[16px] h-[16px] rounded-full bg-unhealthy text-white text-[9px] font-bold px-1">
                {firingCount}
              </span>
            )}
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
        ) : (
          <Link to="/login" className="text-xs text-zinc-400 hover:text-zinc-200 transition-colors mono">
            Sign in
          </Link>
        )}
      </div>
    </nav>
  );
}
