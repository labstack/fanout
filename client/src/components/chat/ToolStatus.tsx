import { CheckCircle2, Loader2 } from "lucide-react";
import type { ToolCall } from "@/stores/chat";

export function ToolStatus({ toolCall }: { toolCall: ToolCall }) {
  return (
    <div
      className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-full text-xs border ${
        toolCall.done
          ? "bg-emerald-500/10 border-emerald-500/20 text-emerald-400"
          : "bg-muted/50 border-border/50 text-muted-foreground"
      }`}
    >
      {toolCall.done ? (
        <CheckCircle2 className="h-3 w-3" />
      ) : (
        <Loader2 className="h-3 w-3 animate-spin" />
      )}
      <span className="font-mono text-[11px]">{toolCall.name}</span>
    </div>
  );
}
