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

// What a tool call is doing, in the reader's words rather than the tool's.
// The session names the activity, so the map lives beside it: nothing here
// may reach for the view.
const activityLabels: Record<string, string> = {
  observability_overview: "Checking system health…",
  service_topology: "Mapping service dependencies…",
  service_performance: "Reading performance signals…",
  trace_detail: "Inspecting a trace…",
  search_logs: "Searching logs…",
  intelligence_snapshot: "Reviewing detected anomalies…",
};

export function activityLabel(toolName: string): string {
  return activityLabels[toolName] ?? "Working on it…";
}
