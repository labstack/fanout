import { useEffect } from "react";
import { useChatStore } from "@/stores/chat";
import { MessageList } from "@/components/chat/MessageList";
import { ChatInput } from "@/components/chat/ChatInput";

export function ChatPage() {
  const { connect, disconnect } = useChatStore();

  useEffect(() => {
    const token =
      new URLSearchParams(location.search).get("token") ?? undefined;
    connect(token);
    return () => disconnect();
  }, [connect, disconnect]);

  return (
    <div className="flex flex-col h-screen">
      <div className="flex-1 overflow-hidden">
        <MessageList />
      </div>
      <ChatInput />
    </div>
  );
}
