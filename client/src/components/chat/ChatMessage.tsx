import type { Message } from "@/stores/chat";
import { useChatStore } from "@/stores/chat";
import { BlockRenderer } from "@/components/blocks/BlockRenderer";
import { ToolStatus } from "./ToolStatus";
import { Markdown } from "@/components/Markdown";
import { Radio } from "lucide-react";


export function ChatMessage({ message }: { message: Message }) {
  const sendMessage = useChatStore((s) => s.sendMessage);
  if (message.role === "user") {
    return (
      <div className="flex justify-end">
        <div className="bg-primary text-white rounded-[16px] rounded-br-[4px] px-4 py-2.5 max-w-[80%] text-base leading-relaxed">
          {message.content}
        </div>
      </div>
    );
  }

  const hasTools = message.toolCalls.length > 0;

  // Assistant message
  return (
    <div className="flex gap-3">
      {/* AI avatar */}
      <div className="shrink-0 mt-0.5 flex items-center justify-center h-7 w-7 rounded-lg bg-primary/15">
        <Radio className="h-3.5 w-3.5 text-primary" />
      </div>

      <div className="flex-1 space-y-3 min-w-0">
        {/* Tool calls (deduplicated by name, keeping latest) */}
        {hasTools && (
          <div className="flex flex-wrap gap-1.5">
            {[...new Map(message.toolCalls.map((tc) => [tc.name, tc])).values()].map((tc, i) => (
              <ToolStatus key={i} toolCall={tc} />
            ))}
          </div>
        )}

        {/* Content: blocks (if done + blocks exist) or markdown */}
        {!message.loading && message.blocks?.length ? (
          <div className="space-y-4">
            {message.blocks.map((block, i) => (
              <BlockRenderer key={i} block={block} onAction={(prompt) => sendMessage(prompt)} />
            ))}
          </div>
        ) : message.content ? (
          <div className={`prose-themed prose-p:leading-relaxed prose-headings:text-foreground prose-headings:font-semibold prose-h2:text-lg prose-h3:text-base prose-strong:text-foreground prose-blockquote:text-muted-foreground ${message.loading ? "shimmer-text" : ""}`}>
            <Markdown>{message.content}</Markdown>
          </div>
        ) : null}

        {/* Error */}
        {message.error && (
          <div className="text-sm text-destructive bg-destructive/10 border border-destructive/20 rounded-xl px-4 py-2.5">
            {message.error}
          </div>
        )}

        {/* Loading indicator — scaling dots */}
        {message.loading && (
          <div className="flex gap-[5px] items-center">
            <span className="h-1.5 w-1.5 rounded-full bg-primary" style={{ animation: "scale-dot 1.4s ease-in-out infinite" }} />
            <span className="h-1.5 w-1.5 rounded-full bg-primary" style={{ animation: "scale-dot 1.4s ease-in-out infinite 0.2s" }} />
            <span className="h-1.5 w-1.5 rounded-full bg-primary" style={{ animation: "scale-dot 1.4s ease-in-out infinite 0.4s" }} />
          </div>
        )}
      </div>
    </div>
  );
}
