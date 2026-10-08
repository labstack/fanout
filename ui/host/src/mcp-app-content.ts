import { panelFragment, type PanelFragment } from "../../panels/fragment";

export type MCPAppContent = {
  resource_uri: "ui://fanout/panels.html";
  tool_name: string;
  tool_input: Record<string, unknown>;
  tool_result: PanelFragment;
  is_error: boolean;
};

export function mcpAppContent(value: unknown): MCPAppContent | undefined {
  if (!value || typeof value !== "object" || Array.isArray(value)) return;
  const content = value as Record<string, unknown>;
  if (Object.keys(content).sort().join(",") !== "is_error,resource_uri,tool_input,tool_name,tool_result") return;
  if (content.resource_uri !== "ui://fanout/panels.html" || typeof content.tool_name !== "string" || !content.tool_name || typeof content.is_error !== "boolean") return;
  if (!content.tool_input || typeof content.tool_input !== "object" || Array.isArray(content.tool_input)) return;
  try { panelFragment(content.tool_result); } catch { return; }
  return content as MCPAppContent;
}
