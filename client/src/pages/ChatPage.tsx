import { useEffect } from "react";
import { useChatStore } from "@/stores/chat";
import { MessageList } from "@/components/chat/MessageList";
import { ChatInput } from "@/components/chat/ChatInput";
import { EmptyState } from "@/components/chat/EmptyState";

export function ChatPage() {
  const { init, messages } = useChatStore();

  useEffect(() => {
    const token =
      new URLSearchParams(location.search).get("token") ?? undefined;
    init(token);
  }, [init]);

  const hasMessages = messages.length > 0;

  return (
    <div className="flex flex-col h-full">
      <div className="flex-1 overflow-hidden">
        {hasMessages ? <MessageList /> : <EmptyState />}
      </div>
      <ChatInput />
    </div>
  );
}
