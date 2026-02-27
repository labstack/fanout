import type { Message } from "@/stores/chat";
import { BlockRenderer } from "@/components/blocks/BlockRenderer";
import { ToolStatus } from "./ToolStatus";
import { Markdown } from "@/components/Markdown";

function SkeletonShimmer() {
  return (
    <div className="space-y-3 py-1">
      <div className="skeleton-bar w-3/4" />
      <div className="skeleton-bar w-full" />
      <div className="skeleton-bar w-5/8" />
      <div className="skeleton-bar w-2/5" />
    </div>
  );
}

export function ChatMessage({ message }: { message: Message }) {
  if (message.role === "user") {
    return (
      <div className="flex justify-end">
        <div className="bg-primary/15 text-foreground rounded-2xl rounded-br-md px-4 py-2.5 max-w-[80%] text-base leading-relaxed">
          {message.content}
        </div>
      </div>
    );
  }

  const hasTools = message.toolCalls.length > 0;

  // Assistant message
  return (
    <div className="space-y-3">
      {/* Tool calls */}
      {hasTools && (
        <div className="flex flex-wrap gap-1.5">
          {message.toolCalls.map((tc, i) => (
            <ToolStatus key={i} toolCall={tc} />
          ))}
        </div>
      )}

      {/* Content: blocks (if done + blocks exist) or streaming markdown */}
      {message.loading ? (
        message.content ? (
          <div className="prose dark:prose-invert max-w-none prose-p:leading-relaxed prose-headings:text-foreground prose-strong:text-foreground streaming-cursor">
            <Markdown>{message.content}</Markdown>
          </div>
        ) : hasTools ? (
          <SkeletonShimmer />
        ) : null
      ) : message.blocks?.length ? (
        // Done with blocks: render each block
        <div className="space-y-4">
          {message.blocks.map((block, i) => (
            <BlockRenderer key={i} block={block} />
          ))}
        </div>
      ) : message.content ? (
        // Done without blocks (legacy/text-only): render as markdown
        <div className="prose dark:prose-invert max-w-none prose-p:leading-relaxed prose-headings:text-foreground prose-strong:text-foreground">
          <Markdown>{message.content}</Markdown>
        </div>
      ) : null}

      {/* Error */}
      {message.error && (
        <div className="text-sm text-destructive bg-destructive/10 border border-destructive/20 rounded-xl px-4 py-2.5">
          {message.error}
        </div>
      )}

      {/* Loading indicator — initial state before any tools or text */}
      {message.loading &&
        !message.content &&
        !hasTools && (
          <div className="flex items-center gap-2.5">
            <div className="flex gap-1">
              <span
                className="h-1.5 w-1.5 rounded-full bg-primary/60 animate-bounce"
                style={{ animationDelay: "0ms" }}
              />
              <span
                className="h-1.5 w-1.5 rounded-full bg-primary/60 animate-bounce"
                style={{ animationDelay: "150ms" }}
              />
              <span
                className="h-1.5 w-1.5 rounded-full bg-primary/60 animate-bounce"
                style={{ animationDelay: "300ms" }}
              />
            </div>
            <span className="text-sm text-muted-foreground">Thinking</span>
          </div>
        )}
    </div>
  );
}
