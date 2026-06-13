import { useEffect, useRef } from "react";
import { useChatStore } from "@/stores/chat";
import { ChatMessage } from "./chat-message";

export function MessageList() {
  const messages = useChatStore((s) => s.messages);
  const endRef = useRef<HTMLDivElement>(null);

  // Scroll when a message is added (not on every streamed token, which would
  // spam smooth-scroll for each character).
  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages.length]);

  return (
    <div className="relative h-full">
      <div className="absolute top-0 left-0 right-0 h-6 bg-gradient-to-b from-surface to-transparent z-10 pointer-events-none" />

      <div className="h-full overflow-y-auto">
        <div className="max-w-4xl mx-auto px-6 py-6 space-y-6">
          {messages.map((msg, i) => (
            <div
              key={msg.id}
              className="animate-fade-up"
              style={{ animationDelay: `${Math.min(i * 50, 200)}ms` }}
            >
              <ChatMessage message={msg} />
            </div>
          ))}
          <div ref={endRef} />
        </div>
      </div>

      <div className="absolute bottom-0 left-0 right-0 h-6 bg-gradient-to-t from-surface to-transparent z-10 pointer-events-none" />
    </div>
  );
}
