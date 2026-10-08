import type { Message } from "@ag-ui/client";
import { createContext, useContext, useMemo, useRef, type FormEvent, type RefObject } from "react";

import { receiptForTurn, type BuildReceipt } from "./dashboard-receipt";

export type FanoutAppContextValue = {
  agentAvailable: boolean;
  threadID: string;
  threadMissing: boolean;
  messages: Message[];
  messageTimes: Record<string, number>;
  ready: boolean;
  running: boolean;
  activity: string;
  provisional: { id: string; text: string; collapsed: boolean } | null;
  stopped: boolean;
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

export const createDashboardPrompt = "Build me a dashboard for my services, showing request volume, latency, and errors. Ask me which services to monitor first.";

// What a tool call is doing, in the reader's words rather than the tool's.
// The session names the activity, so the map lives beside it: nothing here
// may reach for the view.
const toolLabels: Record<string, { activity: string; title: string }> = {
  query_telemetry: { activity: "Querying telemetry…", title: "Telemetry panels" },
  get_observability_overview: { activity: "Checking system health…", title: "System health" },
  get_service_topology: { activity: "Mapping service dependencies…", title: "Service map" },
  get_service_performance: { activity: "Reading performance signals…", title: "Performance" },
  inspect_trace: { activity: "Inspecting a trace…", title: "Trace analysis" },
  search_logs: { activity: "Searching logs…", title: "Logs" },
  get_intelligence_snapshot: { activity: "Reviewing detected anomalies…", title: "Detected anomalies" },
  get_telemetry_schema: { activity: "Reading what telemetry exists…", title: "Telemetry schema" },
  preview_panels: { activity: "Checking dashboard panels…", title: "Panel check" },
  list_dashboards: { activity: "Looking through your dashboards…", title: "Dashboards" },
  get_dashboard: { activity: "Opening your dashboard…", title: "Dashboard" },
  create_dashboard: { activity: "Building your dashboard…", title: "Dashboard" },
  edit_dashboard: { activity: "Updating your dashboard…", title: "Dashboard" },
  replace_dashboard: { activity: "Redesigning your dashboard…", title: "Dashboard" },
};

export function activityLabel(toolName: string): string {
  return toolLabels[toolName]?.activity ?? "Working on it…";
}

export function runErrorMessage(code?: string): string {
  switch (code) {
    case "provider_unavailable": return "Fanout could not reach the model provider. Please try again.";
    case "step_limit": return "Fanout reached its step limit before finishing. Try a narrower question.";
    case "time_limit": return "Fanout reached its 5-minute time limit. Try a smaller request.";
    default: return "Fanout could not complete this analysis. Please try again.";
  }
}

// Reconstructed from the persisted transcript, so switching threads and reload
// use the same evidence as live tool events.
export function useDashboardReceipts(messages: readonly Message[]) {
  const finished = useRef(new Map<string,{last:Message;length:number;receipt:BuildReceipt | null}>());
  return useMemo(() => {
    const receipts = new Map<string,BuildReceipt>(), turnIDs = new Set<string>();
    const starts = messages.flatMap((message,index)=>message.role === "user" ? [index] : []);
    starts.forEach((start,index)=>{
      const end = starts[index+1] ?? messages.length, user=messages[start], last=messages[end-1];
      turnIDs.add(user.id);
      const cached=finished.current.get(user.id);
      const receipt=cached && cached.last === last && cached.length === end-start ? cached.receipt : receiptForTurn(messages.slice(start,end),user.id);
      if(index < starts.length-1) finished.current.set(user.id,{last,length:end-start,receipt});
      if(receipt) receipts.set(user.id,receipt);
    });
    for(const id of finished.current.keys()) if(!turnIDs.has(id)) finished.current.delete(id);
    return receipts;
  },[messages]);
}
