import { useRef, useState } from "react";
import { useChatStore } from "@/stores/chat";
import { ArrowUp, Square } from "lucide-react";

export function ChatInput() {
  const [text, setText] = useState("");
  const textareaRef = useRef<HTMLTextAreaElement>(null);
  const { sendMessage, cancel, messages } = useChatStore();
  const isLoading = messages[messages.length - 1]?.loading ?? false;

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
      <div className="input-glow max-w-3xl mx-auto">
        <div className="flex items-end gap-2 rounded-2xl border border-border/60 bg-card/80 backdrop-blur-sm px-4 py-3">
          <textarea
            ref={textareaRef}
            value={text}
            onChange={handleChange}
            onKeyDown={handleKeyDown}
            placeholder="Ask about your services..."
            className="flex-1 resize-none bg-transparent text-sm text-foreground placeholder:text-muted-foreground/60 focus:outline-none min-h-[24px] max-h-[120px] leading-6"
            rows={1}
          />
          {isLoading ? (
            <button
              onClick={cancel}
              className="flex items-center justify-center h-8 w-8 rounded-lg bg-muted hover:bg-muted/80 transition-colors shrink-0"
              aria-label="Cancel"
            >
              <Square className="h-3.5 w-3.5 text-foreground fill-current" />
            </button>
          ) : (
            <button
              onClick={handleSend}
              disabled={!text.trim()}
              className="flex items-center justify-center h-8 w-8 rounded-lg bg-primary text-primary-foreground hover:bg-primary/90 disabled:opacity-30 disabled:hover:bg-primary transition-colors shrink-0"
              aria-label="Send"
            >
              <ArrowUp className="h-4 w-4" strokeWidth={2.5} />
            </button>
          )}
        </div>
      </div>
      <p className="text-center text-[11px] text-muted-foreground/40 mt-2 max-w-3xl mx-auto">
        Fanout can make mistakes. Verify important data.
      </p>
    </div>
  );
}
