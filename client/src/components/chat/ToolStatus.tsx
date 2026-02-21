import { Loader2, CheckCircle2 } from "lucide-react";
import type { ToolCall } from "@/stores/chat";

export function ToolStatus({ toolCall }: { toolCall: ToolCall }) {
  return (
    <div className="flex items-center gap-2 text-sm text-muted-foreground">
      {toolCall.done ? (
        <CheckCircle2 className="h-3.5 w-3.5 text-emerald-500" />
      ) : (
        <Loader2 className="h-3.5 w-3.5 animate-spin" />
      )}
      <span>{toolCall.name}</span>
    </div>
  );
}
