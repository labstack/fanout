import { Link, NavLink, useLocation } from "react-router";
import { LayoutGrid, Loader2, MessageSquareText, Radio, RotateCcw } from "lucide-react";
import { useChatStore } from "@/stores/chat";
import { buildChatPath, buildDashboardPath } from "@/lib/chat-route";

export function Nav() {
  const { pathname, search } = useLocation();
  const token = new URLSearchParams(search).get("token") ?? undefined;
  const { streaming, messages, clear } = useChatStore();
  const hasMessages = messages.length > 0;
  const isChatRoute = pathname === "/chat";

  return (
    <nav className="border-b border-border/50 px-6 h-12 flex items-center justify-between backdrop-blur-sm sticky top-0 z-50 bg-surface/90">
      <div className="flex items-center gap-6">
        <Link to={buildDashboardPath(token)} className="flex items-center gap-2.5 group">
          <Radio className="h-4 w-4 text-primary opacity-80 group-hover:opacity-100 transition-opacity" />
          <span className="font-heading text-[13px] font-semibold tracking-wide text-foreground/90 group-hover:text-foreground transition-colors">
            fanout
          </span>
        </Link>
        <div className="flex items-center gap-1 rounded-full border border-border/60 bg-surface-1/70 p-1">
          <NavLink
            to={buildDashboardPath(token)}
            end
            className={({ isActive }) =>
              `inline-flex items-center gap-1.5 rounded-full px-3 py-1.5 text-[11px] transition-colors ${
                isActive
                  ? "bg-primary/12 text-foreground"
                  : "text-muted-foreground hover:text-foreground"
              }`
            }
          >
            <LayoutGrid className="h-3.5 w-3.5" />
            Dashboard
          </NavLink>
          <NavLink
            to={buildChatPath(undefined, token)}
            className={({ isActive }) =>
              `inline-flex items-center gap-1.5 rounded-full px-3 py-1.5 text-[11px] transition-colors ${
                isActive
                  ? "bg-primary/12 text-foreground"
                  : "text-muted-foreground hover:text-foreground"
              }`
            }
          >
            <MessageSquareText className="h-3.5 w-3.5" />
            Chat
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
