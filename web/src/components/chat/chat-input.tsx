import { useRef, useState } from "react";
import { useChatStore } from "@/stores/chat";
import { ArrowUp, Square } from "lucide-react";

export function ChatInput() {
  const [text, setText] = useState("");
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  // Select narrowly: subscribing to the whole store (incl. `messages`) would
  // re-render this input on every streamed token. Deriving the boolean inside
  // the selector means we only re-render when the loading state actually flips.
  const sendMessage = useChatStore((s) => s.sendMessage);
  const cancel = useChatStore((s) => s.cancel);
  const isLoading = useChatStore(
    (s) => s.messages[s.messages.length - 1]?.loading ?? false,
  );

  const handleSend = () => {
    const trimmed = text.trim();
    if (!trimmed || isLoading) return;
    sendMessage(trimmed);
    setText("");
    if (textareaRef.current) {
      textareaRef.current.style.height = "auto";
    }
  };

  const handleChange = (e: React.ChangeEvent<HTMLTextAreaElement>) => {
    setText(e.target.value);
    const el = e.target;
    el.style.height = "auto";
    el.style.height = `${Math.min(el.scrollHeight, 120)}px`;
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  return (
    <div className="px-4 pb-4 pt-2 shrink-0">
      <div className="input-glow max-w-4xl mx-auto">
        <div className="flex items-end gap-2 rounded-xl border border-border/80 bg-surface-1 px-4 py-3">
          <textarea
            ref={textareaRef}
            value={text}
            onChange={handleChange}
            onKeyDown={handleKeyDown}
            placeholder="Ask about your services..."
            className="flex-1 resize-none bg-transparent text-[14px] text-foreground placeholder:text-muted-foreground/60 focus:outline-none min-h-[24px] max-h-[120px] leading-6"
            rows={1}
          />
          {isLoading ? (
            <button
              onClick={cancel}
              className="flex items-center justify-center h-9 w-9 rounded-lg bg-surface-3 hover:bg-surface-3/80 transition-colors shrink-0"
              aria-label="Cancel"
            >
              <Square className="h-3.5 w-3.5 text-foreground fill-current" />
            </button>
          ) : (
            <button
              onClick={handleSend}
              disabled={!text.trim()}
              className="flex items-center justify-center h-9 w-9 rounded-lg bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-20 disabled:hover:bg-primary transition-colors shrink-0"
              aria-label="Send"
            >
              <ArrowUp className="h-4 w-4" strokeWidth={2.5} />
            </button>
          )}
        </div>
      </div>
    </div>
  );
}
