import { Link, NavLink, useLocation } from "react-router";
import { Loader2, Radio, RotateCcw } from "lucide-react";
import { useChatStore } from "@/stores/chat";
import { buildChatPath, buildDashboardPath } from "@/lib/chat-route";

export function Nav() {
  const { pathname, search } = useLocation();
  const token = new URLSearchParams(search).get("token") ?? undefined;
  const { streaming, messages, clear } = useChatStore();
  const hasMessages = messages.length > 0;
  const isChatRoute = pathname === "/chat";

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
            className={() =>
              `text-xs mono transition-colors ${
                pathname === "/" || pathname.startsWith("/service/")
                  ? "text-foreground"
                  : "text-zinc-400 hover:text-zinc-200"
              }`
            }
          >
            Home
          </NavLink>
          <NavLink
            to={buildChatPath(undefined, token)}
            className={({ isActive }) =>
              `text-xs mono transition-colors ${
                isActive ? "text-foreground" : "text-zinc-400 hover:text-zinc-200"
              }`
            }
          >
            Investigate
          </NavLink>
        </div>
      </div>
      <div className="flex items-center gap-3">
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
      </div>
    </nav>
  );
}
