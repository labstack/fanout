import { useState } from "react";
import { useChatStore } from "@/stores/chat";
import { Button } from "@/components/ui/button";

export function ChatInput() {
  const [text, setText] = useState("");
  const { sendMessage, cancel, messages } = useChatStore();
  const isLoading = messages[messages.length - 1]?.loading ?? false;

  const handleSend = () => {
    const trimmed = text.trim();
    if (!trimmed || isLoading) return;
    sendMessage(trimmed);
    setText("");
  };

  const handleKeyDown = (e: React.KeyboardEvent) => {
    if (e.key === "Enter" && !e.shiftKey) {
      e.preventDefault();
      handleSend();
    }
  };

  return (
    <div className="border-t p-4">
      <div className="flex gap-2 items-end max-w-3xl mx-auto">
        <textarea
          value={text}
          onChange={(e) => setText(e.target.value)}
          onKeyDown={handleKeyDown}
          placeholder="Ask about your services..."
          className="flex-1 resize-none rounded-lg border bg-background px-3 py-2 text-sm min-h-[40px] max-h-[120px]"
          rows={1}
        />
        {isLoading ? (
          <Button variant="outline" size="sm" onClick={cancel}>
            Cancel
          </Button>
        ) : (
          <Button size="sm" onClick={handleSend} disabled={!text.trim()}>
            Send
          </Button>
        )}
      </div>
    </div>
  );
}
