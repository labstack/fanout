import { CheckCircle2, Loader2 } from "lucide-react";
import type { ToolCall } from "@/stores/chat";

export function ToolStatus({ toolCall }: { toolCall: ToolCall }) {
  return (
    <div
      className={`inline-flex items-center gap-1.5 px-2.5 py-1 rounded-md text-[11px] border mono ${
        toolCall.done
          ? "bg-healthy/10 border-healthy/20 text-healthy"
          : "bg-surface-2 border-border/50 text-muted-foreground"
      }`}
    >
      {toolCall.done ? (
        <CheckCircle2 className="h-3 w-3" />
      ) : (
        <Loader2 className="h-3 w-3 animate-spin" />
      )}
      <span>{toolCall.name}</span>
    </div>
  );
}
