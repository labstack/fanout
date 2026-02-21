import { useEffect, useRef } from "react";
import { useChatStore } from "@/stores/chat";
import { ChatMessage } from "./ChatMessage";

export function MessageList() {
  const messages = useChatStore((s) => s.messages);
  const endRef = useRef<HTMLDivElement>(null);

  useEffect(() => {
    endRef.current?.scrollIntoView({ behavior: "smooth" });
  }, [messages]);

  return (
    <div className="h-full overflow-y-auto px-4 py-6 space-y-4">
      {messages.map((msg) => (
        <ChatMessage key={msg.id} message={msg} />
      ))}
      <div ref={endRef} />
    </div>
  );
}
