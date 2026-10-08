import type { Message } from "@ag-ui/client";

/** AG-UI tool results carry MCP structured content as JSON text. Join them
 * to the assistant's tool call rather than trusting a dashboard-shaped read. */
export function dashboardToolResult(toolCallId: string, content: unknown, messages: readonly Message[]) {
  const call = messages.flatMap(message => message.role === "assistant" ? message.toolCalls ?? [] : []).find(call => call.id === toolCallId);
  const name = call?.function.name;
  if (name !== "create_dashboard" && name !== "edit_dashboard" && name !== "replace_dashboard") return null;
  if (typeof content !== "string") return null;
  try {
    const payload = JSON.parse(content);
    const record = payload?.dashboard;
    if (payload?.error || payload?.isError || typeof record?.id !== "string" || !record.id || typeof record?.name !== "string" || !record.name) return null;
    return { id: record.id, name: record.name, label: name === "create_dashboard" ? "Created" : "Updated" };
  } catch { return null; }
}
