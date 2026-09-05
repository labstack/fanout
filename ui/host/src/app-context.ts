import type { Message } from "@ag-ui/client";
import { createContext, useContext, type FormEvent, type RefObject } from "react";

export type FanoutAppContextValue = {
  agentAvailable: boolean;
  threadID: string;
  threadMissing: boolean;
  messages: Message[];
  messageTimes: Record<string, number>;
  ready: boolean;
  running: boolean;
  activity: string;
  input: string;
  setInput: (value: string) => void;
  error: string;
  bottomRef: RefObject<HTMLDivElement | null>;
  inputRef: RefObject<HTMLTextAreaElement | null>;
  send: (text: string) => Promise<void>;
  submit: (event: FormEvent) => void;
  stop: () => void;
  retry: () => void;
  openChat: (prompt?: string) => void;
  newThread: () => void;
  selectThread: (threadID: string) => void;
};

export const FanoutAppContext = createContext<FanoutAppContextValue | null>(null);

export function useFanoutApp(): FanoutAppContextValue {
  const context = useContext(FanoutAppContext);
  if (!context) throw new Error("Fanout app context is unavailable");
  return context;
}

export const createDashboardPrompt = "Create a new dashboard for me. First ask what I want to monitor, then design it when you have enough context.";
