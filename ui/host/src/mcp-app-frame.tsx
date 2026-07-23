import { AppBridge, PostMessageTransport } from "@modelcontextprotocol/ext-apps/app-bridge";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { Alert, Box, Center, Loader, Text } from "@mantine/core";
import { useEffect, useRef, useState } from "react";
import { authorizedFetch } from "./auth";

export type MCPAppContent = {
  resourceUri: string;
  toolName: string;
  toolInput?: Record<string, unknown>;
  toolResult?: unknown;
  isError?: boolean;
};

function userText(blocks: Array<Record<string, unknown>>): string {
  return blocks.filter((block) => block.type === "text").map((block) => String(block.text ?? "")).join("\n");
}

export default function MCPAppFrame({ content, onMessage }: { content: MCPAppContent; onMessage: (text: string) => Promise<void> }) {
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const clientRef = useRef<Client | null>(null);
  const transportRef = useRef<StreamableHTTPClientTransport | null>(null);
  const bridgeRef = useRef<AppBridge | null>(null);
  const [html, setHTML] = useState("");
  const [height, setHeight] = useState(360);
  const [error, setError] = useState("");

  useEffect(() => {
    let disposed = false;
    async function load() {
      try {
        const transport = new StreamableHTTPClientTransport(new URL("/api/mcp", location.origin), {
          fetch: (url, init) => authorizedFetch(url, init),
        });
        const client = new Client({ name: "fanout-browser", version: "0.2.0" });
        await client.connect(transport);
        clientRef.current = client;
        transportRef.current = transport;
        const resource = await client.readResource({ uri: content.resourceUri });
        const first = resource.contents[0];
        if (!first || !("text" in first) || !first.text) throw new Error("MCP App resource has no HTML content");
        if (!disposed) setHTML(first.text);
      } catch (cause) {
        console.error("MCP app resource load failed", cause);
        if (!disposed) setError("This view could not be loaded. Please try again.");
      }
    }
    void load();
    return () => {
      disposed = true;
      void bridgeRef.current?.teardownResource({}).catch(() => undefined);
      void clientRef.current?.close().catch(() => undefined);
      void transportRef.current?.close().catch(() => undefined);
      bridgeRef.current = null;
      clientRef.current = null;
      transportRef.current = null;
    };
  }, [content.resourceUri]);

  async function connectBridge() {
    const iframe = iframeRef.current;
    const mcpClient = clientRef.current;
    if (!iframe?.contentWindow || !html || !mcpClient || bridgeRef.current) return;
    try {
      const bridge = new AppBridge(
        mcpClient,
        { name: "Fanout", version: "0.2.0" },
        { openLinks: {}, serverTools: {}, logging: {} },
        { hostContext: { theme: "light", displayMode: "inline" } },
      );
      bridgeRef.current = bridge;
      bridge.onsizechange = ({ height: requested }) => {
        if (requested) setHeight(Math.max(180, Math.min(requested, 900)));
      };
      bridge.onmessage = async ({ content: blocks }) => {
        const text = userText(blocks as Array<Record<string, unknown>>);
        if (!text) return { isError: true };
        await onMessage(text);
        return {};
      };
      bridge.oninitialized = async () => {
        await bridge.sendToolInput({ arguments: content.toolInput ?? {} });
        await bridge.sendToolResult({
          content: [{ type: "text", text: JSON.stringify(content.toolResult ?? {}) }],
          structuredContent: content.toolResult as Record<string, unknown> | undefined,
          isError: content.isError,
        });
      };
      await bridge.connect(new PostMessageTransport(iframe.contentWindow, iframe.contentWindow));
    } catch (cause) {
      console.error("MCP app bridge connect failed", cause);
      setError("This view could not be loaded. Please try again.");
    }
  }

  if (error) return <Alert color="red" m="md">{error}</Alert>;
  if (!html) return <Center mih={180} p="xl"><Loader size="sm" /><Text c="dimmed" size="sm" ml="sm">Preparing view…</Text></Center>;
  return <Box component="iframe" ref={iframeRef} title="Fanout analysis view" sandbox="allow-scripts" srcDoc={html} w="100%" bd={0} bg="var(--mantine-color-body)" style={{ display: "block", height, transition: "height 200ms ease" }} onLoad={() => void connectBridge()} />;
}
