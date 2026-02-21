import type { Message } from "@/stores/chat";
import { BlockRenderer } from "@/components/blocks/BlockRenderer";
import { ToolStatus } from "./ToolStatus";
import ReactMarkdown from "react-markdown";

export function ChatMessage({ message }: { message: Message }) {
  if (message.role === "user") {
    return (
      <div className="flex justify-end">
        <div className="bg-primary text-primary-foreground rounded-lg px-4 py-2 max-w-[80%]">
          {message.content}
        </div>
      </div>
    );
  }

  // Assistant message
  return (
    <div className="space-y-3">
      {/* Tool calls */}
      {message.toolCalls.length > 0 && (
        <div className="space-y-1">
          {message.toolCalls.map((tc, i) => (
            <ToolStatus key={i} toolCall={tc} />
          ))}
        </div>
      )}

      {/* Content: blocks (if done + blocks exist) or streaming markdown */}
      {message.loading ? (
        // Streaming: render accumulated tokens as markdown
        message.content ? (
          <div className="prose prose-sm dark:prose-invert max-w-none">
            <ReactMarkdown>{message.content}</ReactMarkdown>
          </div>
        ) : null
      ) : message.blocks?.length ? (
        // Done with blocks: render each block
        message.blocks.map((block, i) => (
          <BlockRenderer key={i} block={block} />
        ))
      ) : message.content ? (
        // Done without blocks (legacy/text-only): render as markdown
        <div className="prose prose-sm dark:prose-invert max-w-none">
          <ReactMarkdown>{message.content}</ReactMarkdown>
        </div>
      ) : null}

      {/* Error */}
      {message.error && (
        <div className="text-sm text-destructive bg-destructive/10 rounded-md px-3 py-2">
          {message.error}
        </div>
      )}

      {/* Loading indicator */}
      {message.loading &&
        !message.content &&
        message.toolCalls.length === 0 && (
          <div className="text-sm text-muted-foreground animate-pulse">
            Thinking...
          </div>
        )}
    </div>
  );
}
