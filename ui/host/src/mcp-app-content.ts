import { panelFragment, type PanelFragment } from "../../panels/fragment";

export type MCPAppContent = {
  resource_uri: "ui://fanout/panels.html";
  tool_name: string;
  tool_input: Record<string, unknown>;
  tool_result: PanelFragment;
  is_error: boolean;
};

// AG-UI snapshots are immutable objects. Share the validation result across
// collapsed chat summaries and the independently callable iframe boundary.
const validated = new WeakSet<object>();

export function mcpAppContent(value: unknown): MCPAppContent | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return;
  if (validated.has(value)) return value as MCPAppContent;
  const content = value as Record<string, unknown>;
  if (Object.keys(content).sort().join(",") !== "is_error,resource_uri,tool_input,tool_name,tool_result") return;
  if (content.resource_uri !== "ui://fanout/panels.html" || typeof content.tool_name !== "string" || !content.tool_name || typeof content.is_error !== "boolean") return;
  if (!content.tool_input || typeof content.tool_input !== "object" || Array.isArray(content.tool_input)) return;
  try { panelFragment(content.tool_result); } catch { return; }
  validated.add(content);
  return content as MCPAppContent;
}
