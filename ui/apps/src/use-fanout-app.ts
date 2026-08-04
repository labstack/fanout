import type { App, McpUiHostContext } from "@modelcontextprotocol/ext-apps";
import { useApp } from "@modelcontextprotocol/ext-apps/react";
import type { CallToolResult } from "@modelcontextprotocol/sdk/types.js";
import { useEffect, useState } from "react";

export function useFanoutApp<T>(name: string) {
  const [result, setResult] = useState<T | null>(null);
  const [toolInput, setToolInput] = useState<Record<string, unknown>>({});
  const [host, setHost] = useState<McpUiHostContext>();
  const [toolError, setToolError] = useState<string | null>(null);

  function acceptResult(incoming: CallToolResult) {
    if (incoming.isError) {
      setToolError("This view could not be refreshed. Please try again.");
      return;
    }
    setToolError(null);
    setResult(incoming.structuredContent as T);
  }

  const connection = useApp({
    appInfo: { name, version: "1.0.0" },
    capabilities: {},
    onAppCreated: (app) => {
      app.ontoolinput = (input) => {
        setToolInput((input.arguments ?? {}) as Record<string, unknown>);
      };
      app.ontoolresult = acceptResult;
      app.onhostcontextchanged = (context) => setHost((previous) => ({ ...previous, ...context }));
      app.onerror = (cause) => {
        console.error("MCP app error", cause);
        setToolError("This view could not be refreshed. Please try again.");
      };
    },
  });

  useEffect(() => {
    if (connection.app) setHost(connection.app.getHostContext());
  }, [connection.app]);

  async function callTool(toolName: string) {
    if (!connection.app) return;
    try {
      acceptResult(await connection.app.callServerTool({ name: toolName, arguments: toolInput }));
    } catch (cause) {
      console.error(`Tool call ${toolName} failed`, cause);
      setToolError("This view could not be refreshed. Please try again.");
    }
  }

  return { ...connection, result, toolInput, host, toolError, callTool };
}

export async function askAbout(app: App | null, subject: string) {
  if (!app) return;
  await app.sendMessage({ role: "user", content: [{ type: "text", text: subject }] });
}
