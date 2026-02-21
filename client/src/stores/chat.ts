import { create } from "zustand";
import type { StoreApi } from "zustand";
import { ChatSocket, wsURL } from "@/lib/ws";
import type { Block, ChatEvent } from "@/lib/types";

// ---------------------------------------------------------------------------
// Types
// ---------------------------------------------------------------------------

export interface ToolCall {
  name: string;
  input: string;
  done: boolean;
}

export interface Message {
  id: string;
  role: "user" | "assistant";
  content: string;
  blocks?: Block[];
  toolCalls: ToolCall[];
  loading: boolean;
  error?: string;
}

interface ChatStore {
  messages: Message[];
  connected: boolean;

  connect: (token?: string) => void;
  disconnect: () => void;
  sendMessage: (text: string, window?: number, namespace?: string) => void;
  cancel: () => void;
  clear: () => void;
}

// ---------------------------------------------------------------------------
// Zustand setter/getter helpers
// ---------------------------------------------------------------------------

type SetState = StoreApi<ChatStore>["setState"];
type GetState = StoreApi<ChatStore>["getState"];

// ---------------------------------------------------------------------------
// WebSocket instance (kept outside store state — not serializable)
// ---------------------------------------------------------------------------

let socket: ChatSocket | null = null;

// ---------------------------------------------------------------------------
// Event handler — updates the last assistant message immutably
// ---------------------------------------------------------------------------

function handleEvent(set: SetState, _get: GetState, event: ChatEvent) {
  set((state) => {
    const messages = [...state.messages];
    const lastIdx = messages.length - 1;
    const last = messages[lastIdx];

    if (!last || last.role !== "assistant") return state;

    switch (event.type) {
      case "token":
        messages[lastIdx] = {
          ...last,
          content: last.content + (event.content ?? ""),
        };
        break;

      case "tool_call":
        messages[lastIdx] = {
          ...last,
          toolCalls: [
            ...last.toolCalls,
            { name: event.name ?? "", input: event.input ?? "", done: false },
          ],
        };
        break;

      case "tool_result":
        messages[lastIdx] = {
          ...last,
          toolCalls: last.toolCalls.map((tc) =>
            tc.name === event.name && !tc.done ? { ...tc, done: true } : tc,
          ),
        };
        break;

      case "card":
        // Legacy: append HTML to content (will be replaced by blocks)
        messages[lastIdx] = {
          ...last,
          content: last.content + (event.html ?? event.content ?? ""),
        };
        break;

      case "done":
        messages[lastIdx] = {
          ...last,
          loading: false,
          blocks: event.blocks ?? undefined,
          id: event.id ?? last.id,
        };
        break;

      case "error":
        messages[lastIdx] = {
          ...last,
          loading: false,
          error: event.error,
        };
        break;

      case "tail":
        // Future: parse event.content as JSON for log tail entries
        console.debug("[chat] tail event:", event.content);
        break;

      case "tail_end":
        // Future: mark tail stopped
        console.debug("[chat] tail_end event");
        break;

      default:
        break;
    }

    return { messages };
  });
}

// ---------------------------------------------------------------------------
// Store
// ---------------------------------------------------------------------------

export const useChatStore = create<ChatStore>((set, get) => ({
  messages: [],
  connected: false,

  connect: (token?: string) => {
    if (socket) return;

    socket = new ChatSocket(
      wsURL("/ws/chat", token),
      (event) => handleEvent(set, get, event),
      (connected) => set({ connected }),
    );
    socket.connect();
  },

  disconnect: () => {
    socket?.close();
    socket = null;
    set({ connected: false });
  },

  sendMessage: (text, window = 60, namespace = "") => {
    const userMsg: Message = {
      id: crypto.randomUUID(),
      role: "user",
      content: text,
      toolCalls: [],
      loading: false,
    };

    const assistantMsg: Message = {
      id: crypto.randomUUID(),
      role: "assistant",
      content: "",
      toolCalls: [],
      loading: true,
    };

    set((state) => ({
      messages: [...state.messages, userMsg, assistantMsg],
    }));

    socket?.send({ type: "message", content: text, window, namespace });
  },

  cancel: () => {
    socket?.send({ type: "cancel" });

    set((state) => {
      const messages = [...state.messages];
      const last = messages[messages.length - 1];
      if (last?.role === "assistant" && last.loading) {
        messages[messages.length - 1] = { ...last, loading: false };
      }
      return { messages };
    });
  },

  clear: () => {
    socket?.send({ type: "clear" });
    set({ messages: [] });
  },
}));
