import { useEffect } from "react";
import { useChatStore } from "@/stores/chat";
import { MessageList } from "@/components/chat/MessageList";
import { ChatInput } from "@/components/chat/ChatInput";
import { EmptyState } from "@/components/chat/EmptyState";
import { Radio, RotateCcw, Loader2 } from "lucide-react";

export function ChatPage() {
  const { init, streaming, messages, clear } = useChatStore();

  useEffect(() => {
    const token =
      new URLSearchParams(location.search).get("token") ?? undefined;
    init(token);
  }, [init]);

  const hasMessages = messages.length > 0;

  return (
    <div className="flex flex-col h-screen">
      {/* Header */}
      <header className="flex items-center justify-between px-5 h-12 border-b border-border/50 shrink-0">
        <div className="flex items-center gap-2.5">
          <Radio className="h-4.5 w-4.5 text-primary" />
          <span className="font-sans text-sm font-bold tracking-wide text-foreground">
            fanout
          </span>
        </div>
        <div className="flex items-center gap-3">
          {streaming && (
            <div className="flex items-center gap-1.5 text-xs text-muted-foreground font-mono">
              <Loader2 className="h-3 w-3 animate-spin" />
              Streaming
            </div>
          )}
          {hasMessages && (
            <button
              onClick={clear}
              className="flex items-center gap-1.5 text-xs text-muted-foreground hover:text-foreground transition-colors px-2 py-1 rounded-md hover:bg-muted/50"
            >
              <RotateCcw className="h-3 w-3" />
              <span className="hidden sm:inline">New</span>
            </button>
          )}
        </div>
      </header>

      {/* Main content */}
      <div className="flex-1 overflow-hidden">
        {hasMessages ? <MessageList /> : <EmptyState />}
      </div>

      {/* Input */}
      <ChatInput />
    </div>
  );
}
