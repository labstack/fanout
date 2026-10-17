export type AgentEvent = {
  type: string;
  timestamp: number;
  toolCallId?: string;
  toolCallName?: string;
  content?: string;
};
export type WaitInterval = { kind: "model_thinking" | "tool_call"; start: number; end: number; tool?: string };

/** Observe the completed, unmodified SSE response; never route or replace requests. */
export function recordingTimeline(stream: string, responseAt: number) {
  const events = stream.split(/\r?\n\r?\n/).flatMap(frame => {
    const data = frame.split(/\r?\n/).filter(line => line.startsWith("data:")).map(line => line.slice(5).trimStart()).join("\n");
    return data ? [JSON.parse(data) as AgentEvent] : [];
  });
  const first = events.find(event => event.type === "RUN_STARTED");
  if (!first || !Number.isFinite(first.timestamp)) throw new Error("Agent stream has no timestamped RUN_STARTED event");
  if (events.some(event => event.type === "RUN_ERROR")) throw new Error("Agent run failed; inspect the instance before recording again");
  if (!events.some(event => event.type === "RUN_FINISHED")) throw new Error("Agent stream ended without RUN_FINISHED");
  const waits: WaitInterval[] = [];
  const tools = new Map<string, { start: number; tool: string }>();
  const names = new Map<string, string>();
  let thinking: number | undefined, dashboardId: string | undefined;
  const endThinking = (end: number) => {
    if (thinking !== undefined && end > thinking) waits.push({ kind: "model_thinking", start: thinking, end });
    thinking = undefined;
  };
  for (const event of events) {
    if (!Number.isFinite(event.timestamp)) throw new Error(`Agent event ${event.type} has no timestamp`);
    // Anchor server timestamps to receipt of the response headers to avoid clock skew.
    const at = responseAt + (event.timestamp - first.timestamp) / 1000;
    switch (event.type) {
      case "RUN_STARTED": thinking = at; break;
      case "REASONING_MESSAGE_START": case "TEXT_MESSAGE_START": endThinking(at); break;
      case "REASONING_MESSAGE_END": if (thinking === undefined && tools.size === 0) thinking = at; break;
      case "TOOL_CALL_START":
        endThinking(at);
        if (!event.toolCallId || !event.toolCallName) throw new Error("Tool call has no id or name");
        tools.set(event.toolCallId, { start: at, tool: event.toolCallName });
        names.set(event.toolCallId, event.toolCallName);
        break;
      case "TOOL_CALL_RESULT": {
        const call = tools.get(event.toolCallId!);
        if (!call) throw new Error("Tool result has no matching start event");
        if (at > call.start) waits.push({ kind: "tool_call", ...call, end: at });
        tools.delete(event.toolCallId!);
        if (tools.size === 0) thinking = at;
        if (names.get(event.toolCallId!) === "create_dashboard" && event.content) {
          const result = JSON.parse(event.content) as { isError?: boolean; error?: unknown; dashboard?: { id?: string }; receipt?: unknown };
          if (!result.isError && !result.error && result.receipt && typeof result.dashboard?.id === "string") dashboardId = result.dashboard.id;
        }
        break;
      }
      case "RUN_FINISHED": endThinking(at); break;
    }
  }
  if (tools.size) throw new Error("Agent finished with unresolved tool calls");
  return { waits: waits.sort((a, b) => a.start - b.start), dashboardId };
}
