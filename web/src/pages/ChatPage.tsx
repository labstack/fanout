import { useEffect, useRef } from "react";
import { useSearchParams } from "react-router";
import { useChatStore } from "@/stores/chat";
import { MessageList } from "@/components/chat/MessageList";
import { ChatInput } from "@/components/chat/ChatInput";
import { EmptyState } from "@/components/chat/EmptyState";
import { getApiToken } from "@/api/client";

export function ChatPage() {
  const [searchParams, setSearchParams] = useSearchParams();
  const sentPromptRef = useRef<string | null>(null);
  const { init, messages, sendMessage } = useChatStore();

  useEffect(() => {
    const token = searchParams.get("token") ?? getApiToken() ?? undefined;
    init(token);
  }, [init, searchParams]);

  useEffect(() => {
    const prompt = searchParams.get("q");
    if (!prompt) return;

    const promptKey = `${searchParams.get("token") ?? ""}:${prompt}`;
    if (sentPromptRef.current === promptKey) return;

    sentPromptRef.current = promptKey;
    sendMessage(prompt);

    const next = new URLSearchParams(searchParams);
    next.delete("q");
    setSearchParams(next, { replace: true });
  }, [searchParams, sendMessage, setSearchParams]);

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
