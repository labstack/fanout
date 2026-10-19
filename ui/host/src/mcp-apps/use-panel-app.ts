import type { App, McpUiHostContext } from "@modelcontextprotocol/ext-apps";
import { useApp } from "@modelcontextprotocol/ext-apps/react";
import { useEffect, useState } from "react";
import { panelFragment, type PanelFragment } from "../../../panels/fragment";

export function usePanelApp() {
  const [fragment, setFragment] = useState<PanelFragment | null>(null);
  const [host, setHost] = useState<McpUiHostContext>();
  const [toolError, setToolError] = useState<string | null>(null);
  function acceptResult(incoming: Awaited<ReturnType<App["callServerTool"]>>) {
    try {
      if (incoming.isError) {
        const text = incoming.content?.find(item => item.type === "text");
        const problem = incoming.structuredContent ?? (text?.type === "text" ? JSON.parse(text.text) : undefined);
        if (problem?.error && typeof problem.error === "object" && "code" in problem.error && problem.error.code === "no_data" && "message" in problem.error && typeof problem.error.message === "string") {
          setFragment(null); setToolError(problem.error.message); return;
        }
        throw new Error("Tool error");
      }
      const next = panelFragment(incoming.structuredContent);
      setFragment(next); setToolError(null);
    } catch {
      setToolError("This view could not be loaded. Please try again.");
    }
  }
  const connection = useApp({
    autoResize: false,
    appInfo: { name: "Fanout panels", version: "1.0.0" }, capabilities: {},
    onAppCreated: app => {
      app.ontoolresult = acceptResult;
      app.ontoolcancelled = () => setToolError("View cancelled.");
      app.onhostcontextchanged = context => setHost(previous => ({ ...previous, ...context }));
      app.onerror = () => setToolError("This view could not be refreshed. Please try again.");
    },
  });
  useEffect(() => {
    if (connection.app) setHost(connection.app.getHostContext());
  }, [connection.app]);
  return { ...connection, fragment, host, toolError };
}
