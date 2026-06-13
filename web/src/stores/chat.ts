import { create } from "zustand";
import type { StoreApi } from "zustand";
import { ChatClient } from "@/lib/sse";
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
  /** When true, the next token should replace content instead of appending. */
  pendingReplace?: boolean;
}

interface ChatStore {
  messages: Message[];
  streaming: boolean;

  init: (token?: string) => void;
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
// ChatClient instance (kept outside store state — not serializable)
// ---------------------------------------------------------------------------

let client: ChatClient | null = null;

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
          content: last.pendingReplace
            ? (event.content ?? "")
            : last.content + (event.content ?? ""),
          pendingReplace: false,
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
          pendingReplace: true,
        };
        break;

      case "done": {
        // Ensure all tool calls show as complete when the response is finalized,
        // in case individual tool_result events were missed or arrived out of order.
        const incomplete = last.toolCalls.filter((tc) => !tc.done);
        if (incomplete.length > 0) {
          console.warn(
            "[chat] done with incomplete tool calls, force-completing:",
            incomplete.map((tc) => tc.name),
          );
        }
        messages[lastIdx] = {
          ...last,
          loading: false,
          blocks: event.blocks ?? undefined,
          id: event.id ?? last.id,
          toolCalls: last.toolCalls.map((tc) =>
            tc.done ? tc : { ...tc, done: true },
          ),
        };
        break;
      }

      case "error":
        messages[lastIdx] = {
          ...last,
          loading: false,
          error: event.error,
        };
        break;

      default:
        console.warn("[chat] unrecognized event type:", event.type);
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
  streaming: false,

  init: (token?: string) => {
    if (client) {
      // Already initialized — refresh the token in case auth changed (re-auth /
      // refresh) so the client doesn't keep streaming with a stale credential.
      if (token) client.setToken(token);
      return;
    }

    client = new ChatClient(
      "",
      (event) => handleEvent(set, get, event),
      (status) => set({ streaming: status === "streaming" }),
    );
    if (token) {
      client.setToken(token);
    }
  },

  sendMessage: (text, window = 60, namespace = "") => {
    if (!client) {
      console.error("[chat] sendMessage called before init()");
      return;
    }

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

    // send() is async — fire and forget (errors are handled via events)
    client.send({ content: text, window, namespace }).catch((err) => {
      console.error("[chat] send failed:", err);
      set((state) => {
        const messages = [...state.messages];
        const last = messages[messages.length - 1];
        if (last?.role === "assistant" && last.loading) {
          messages[messages.length - 1] = {
            ...last,
            loading: false,
            error: "Failed to send message. Please try again.",
          };
        }
        return { messages };
      });
    });
  },

  cancel: () => {
    client?.cancel();

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
    client?.clear();
    set({ messages: [] });
  },
}));
