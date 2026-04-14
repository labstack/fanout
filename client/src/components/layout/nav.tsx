import { Link } from "react-router";
import { Radio, RotateCcw, Loader2 } from "lucide-react";
import { useChatStore } from "@/stores/chat";

export function Nav() {
  const { streaming, messages, clear } = useChatStore();
  const hasMessages = messages.length > 0;

  return (
    <nav className="border-b border-border/50 px-6 py-3 flex items-center justify-between backdrop-blur-sm sticky top-0 z-50 bg-surface/80">
      <div className="flex items-center gap-6">
        <Link to="/" className="flex items-center gap-2.5 group">
          <Radio className="h-4.5 w-4.5 text-primary" />
          <span className="font-heading text-sm font-bold tracking-wide text-foreground">
            fanout
          </span>
        </Link>
      </div>
      <div className="flex items-center gap-3">
        {streaming && (
          <div className="flex items-center gap-1.5 text-xs text-muted-foreground mono">
            <Loader2 className="h-3 w-3 animate-spin" />
            Streaming
          </div>
        )}
        {hasMessages && (
          <button
            onClick={clear}
            className="flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors px-2 py-1 rounded-md hover:bg-surface-3"
          >
            <RotateCcw className="h-3 w-3" />
            <span className="hidden sm:inline">New</span>
          </button>
        )}
      </div>
    </nav>
  );
}
