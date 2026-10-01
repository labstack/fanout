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
  inputRef: RefObject<HTMLTextAreaElement | null>;
  send: (text: string) => Promise<void>;
  submit: (event: FormEvent) => void;
  stop: () => void;
  retry: () => void;
  /** Asks the server for this thread again after a load that failed. */
  reloadThread: () => void;
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
const toolLabels: Record<string, { activity: string; title: string }> = {
  get_observability_overview: { activity: "Checking system health…", title: "System health" },
  get_service_topology: { activity: "Mapping service dependencies…", title: "Service map" },
  get_service_performance: { activity: "Reading performance signals…", title: "Performance" },
  inspect_trace: { activity: "Inspecting a trace…", title: "Trace analysis" },
  search_logs: { activity: "Searching logs…", title: "Logs" },
  get_intelligence_snapshot: { activity: "Reviewing detected anomalies…", title: "Detected anomalies" },
};

export function activityLabel(toolName: string): string {
  return toolLabels[toolName]?.activity ?? "Working on it…";
}

export function toolTitle(toolName: string): string {
  return toolLabels[toolName]?.title ?? "System analysis";
}
