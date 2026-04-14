import { Link } from "react-router";
import { Radio, RotateCcw, Loader2 } from "lucide-react";
import { useChatStore } from "@/stores/chat";

export function Nav() {
  const { streaming, messages, clear } = useChatStore();
  const hasMessages = messages.length > 0;

  return (
    <nav className="border-b border-border/50 px-6 h-12 flex items-center justify-between backdrop-blur-sm sticky top-0 z-50 bg-surface/90">
      <div className="flex items-center gap-6">
        <Link to="/" className="flex items-center gap-2.5 group">
          <Radio className="h-4 w-4 text-primary opacity-80 group-hover:opacity-100 transition-opacity" />
          <span className="font-heading text-[13px] font-semibold tracking-wide text-foreground/90 group-hover:text-foreground transition-colors">
            fanout
          </span>
        </Link>
      </div>
      <div className="flex items-center gap-3">
        {streaming && (
          <div className="flex items-center gap-1.5 text-[11px] text-muted-foreground mono">
            <Loader2 className="h-3 w-3 animate-spin" />
            Streaming
          </div>
        )}
        {hasMessages && (
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
