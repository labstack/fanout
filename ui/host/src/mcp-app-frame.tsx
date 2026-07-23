import { AppBridge, PostMessageTransport } from "@modelcontextprotocol/ext-apps/app-bridge";
import { Client } from "@modelcontextprotocol/sdk/client/index.js";
import { StreamableHTTPClientTransport } from "@modelcontextprotocol/sdk/client/streamableHttp.js";
import { CallToolResultSchema } from "@modelcontextprotocol/sdk/types.js";
import { Alert, Box, Center, Loader, Text } from "@mantine/core";
import { useEffect, useRef, useState } from "react";
import { authorizedFetch } from "./auth";

const mcpAppMIME = "text/html;profile=mcp-app";
const mcpUIExtension = "io.modelcontextprotocol/ui";

const appMinimumHeights: Record<string, number> = {
  observability_overview: 620,
  overview: 620,
  service_topology: 760,
  topology: 760,
  service_performance: 700,
  performance: 700,
  trace_detail: 720,
  trace: 720,
  search_logs: 720,
  logs: 720,
};

export type MCPAppContent = {
  resourceUri: string;
  toolName: string;
  toolInput?: Record<string, unknown>;
  toolResult?: unknown;
  isError?: boolean;
};

type BrowserMCPConnection = {
  client: Client;
  references: number;
  closed: boolean;
  closeListeners: Set<() => void>;
  closeTimer?: ReturnType<typeof setTimeout>;
};

let sharedConnection: BrowserMCPConnection | null = null;
let sharedConnectionPromise: Promise<BrowserMCPConnection> | null = null;

async function createBrowserMCPConnection(): Promise<BrowserMCPConnection> {
  const transport = new StreamableHTTPClientTransport(new URL("/api/mcp", location.origin), {
    fetch: (url, init) => authorizedFetch(url, init),
  });
  const client = new Client({ name: "fanout-browser", version: "0.2.0" }, {
    capabilities: {
      extensions: {
        [mcpUIExtension]: { mimeTypes: [mcpAppMIME] },
      },
    },
  });
  try {
    await client.connect(transport);
    const connection: BrowserMCPConnection = {
      client,
      references: 0,
      closed: false,
      closeListeners: new Set(),
    };
    client.onclose = () => invalidateBrowserMCPConnection(connection, false);
    client.onerror = () => invalidateBrowserMCPConnection(connection, true);
    return connection;
  } catch (cause) {
    await transport.close().catch(() => undefined);
    throw cause;
  }
}

async function acquireBrowserMCPConnection(): Promise<BrowserMCPConnection> {
  if (!sharedConnectionPromise) {
    const pending = createBrowserMCPConnection();
    sharedConnectionPromise = pending;
    pending.then((connection) => {
      if (sharedConnectionPromise === pending && !connection.closed) sharedConnection = connection;
    }).catch(() => {
      if (sharedConnectionPromise === pending) sharedConnectionPromise = null;
    });
  }
  const connection = await sharedConnectionPromise;
  if (connection.closed) {
    if (sharedConnectionPromise) sharedConnectionPromise = null;
    return acquireBrowserMCPConnection();
  }
  if (connection.closeTimer) {
    clearTimeout(connection.closeTimer);
    connection.closeTimer = undefined;
  }
  connection.references += 1;
  return connection;
}

function invalidateBrowserMCPConnection(connection: BrowserMCPConnection, closeClient: boolean) {
  if (connection.closed) return;
  connection.closed = true;
  if (connection.closeTimer) clearTimeout(connection.closeTimer);
  connection.closeTimer = undefined;
  if (sharedConnection === connection) sharedConnection = null;
  sharedConnectionPromise = null;
  for (const listener of [...connection.closeListeners]) listener();
  if (closeClient) void connection.client.close().catch(() => undefined);
}

function releaseBrowserMCPConnection(connection: BrowserMCPConnection) {
  connection.references = Math.max(0, connection.references - 1);
  if (connection.closed || connection.references || connection.closeTimer) return;
  connection.closeTimer = setTimeout(() => {
    connection.closeTimer = undefined;
    if (connection.references || sharedConnection !== connection) return;
    connection.closed = true;
    sharedConnection = null;
    sharedConnectionPromise = null;
    void connection.client.close().catch(() => undefined);
  }, 0);
}

type ResourceCSP = {
  connectDomains?: unknown;
  resourceDomains?: unknown;
  frameDomains?: unknown;
  baseUriDomains?: unknown;
};

function record(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}

function cspSources(value: unknown, schemes: string[]): string[] {
  if (!Array.isArray(value)) return [];
  const allowed = new Set(schemes);
  return value.filter((source): source is string => {
    if (typeof source !== "string" || /[\s;'\"]/.test(source)) return false;
    const match = source.match(/^([a-z]+):\/\/([^/]+)$/i);
    return Boolean(match && allowed.has(match[1].toLowerCase()));
  });
}

function mcpAppCSP(meta: unknown): string {
  const csp = record(record(record(meta)?.ui)?.csp) as ResourceCSP | undefined;
  const connect = cspSources(csp?.connectDomains, ["http", "https", "ws", "wss"]);
  const resources = cspSources(csp?.resourceDomains, ["http", "https"]);
  const frames = cspSources(csp?.frameDomains, ["http", "https"]);
  const bases = cspSources(csp?.baseUriDomains, ["http", "https"]);
  const resourceSuffix = resources.length ? ` ${resources.join(" ")}` : "";
  const directives = [
    "default-src 'none'",
    `script-src 'self' 'unsafe-inline'${resourceSuffix}`,
    `style-src 'self' 'unsafe-inline'${resourceSuffix}`,
    `img-src 'self' data:${resourceSuffix}`,
    `media-src 'self' data:${resourceSuffix}`,
    `connect-src ${connect.length ? connect.join(" ") : "'none'"}`,
  ];
  if (resources.length) directives.push(`font-src 'self' ${resources.join(" ")}`);
  if (frames.length) directives.push(`frame-src ${frames.join(" ")}`);
  if (bases.length) directives.push(`base-uri ${bases.join(" ")}`);
  return `${directives.join("; ")};`;
}

function enforceMCPAppCSP(html: string, meta: unknown): string {
  const policy = mcpAppCSP(meta);
  const tag = `<meta http-equiv="Content-Security-Policy" content="${policy}">`;
  if (/<head(?:\s[^>]*)?>/i.test(html)) return html.replace(/<head(?:\s[^>]*)?>/i, (head) => `${head}${tag}`);
  if (/<html(?:\s[^>]*)?>/i.test(html)) return html.replace(/<html(?:\s[^>]*)?>/i, (root) => `${root}<head>${tag}</head>`);
  return `<!doctype html><html><head>${tag}</head><body>${html}</body></html>`;
}

function userText(blocks: Array<Record<string, unknown>>): string {
  return blocks.filter((block) => block.type === "text").map((block) => String(block.text ?? "")).join("\n");
}

export default function MCPAppFrame({ content, onMessage }: { content: MCPAppContent; onMessage: (text: string) => Promise<void> }) {
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const clientRef = useRef<Client | null>(null);
  const connectionRef = useRef<BrowserMCPConnection | null>(null);
  const bridgeRef = useRef<AppBridge | null>(null);
  const [html, setHTML] = useState("");
  const minimumHeight = appMinimumHeights[content.toolName] ?? 620;
  const [height, setHeight] = useState(minimumHeight);
  const [error, setError] = useState("");
  const [connectionGeneration, setConnectionGeneration] = useState(0);

  useEffect(() => {
    let disposed = false;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    setHTML("");
    setError("");
    const reconnect = () => setConnectionGeneration((generation) => generation + 1);
    async function load() {
      try {
        const connection = await acquireBrowserMCPConnection();
        if (disposed) {
          releaseBrowserMCPConnection(connection);
          return;
        }
        connectionRef.current = connection;
        connection.closeListeners.add(reconnect);
        clientRef.current = connection.client;
        const resource = await connection.client.readResource({ uri: content.resourceUri });
        const first = resource.contents[0];
        if (!first || !("text" in first) || !first.text) throw new Error("MCP App resource has no HTML content");
        if (first.uri !== content.resourceUri) throw new Error("MCP App resource URI does not match the requested URI");
        if (first.mimeType !== mcpAppMIME) throw new Error("MCP App resource has an unsupported MIME type");
        if (!disposed) setHTML(enforceMCPAppCSP(first.text, first._meta));
      } catch (cause) {
        console.error("MCP app resource load failed", cause);
        if (!disposed) {
          setError("This view could not be loaded. Please try again.");
          // Once a live connection has died, keep retrying through the short
          // outage window of a server restart. Initial loads and invalid
          // resource responses still fail closed without an automatic loop.
          if (connectionGeneration > 0) {
            retryTimer = setTimeout(() => setConnectionGeneration((generation) => generation + 1), 1500);
          }
        }
      }
    }
    void load();
    return () => {
      disposed = true;
      if (retryTimer) clearTimeout(retryTimer);
      const teardown = bridgeRef.current?.teardownResource({}).catch(() => undefined) ?? Promise.resolve();
      const connection = connectionRef.current;
      if (connection) {
        connection.closeListeners.delete(reconnect);
        void teardown.finally(() => releaseBrowserMCPConnection(connection));
      }
      bridgeRef.current = null;
      clientRef.current = null;
      connectionRef.current = null;
    };
  }, [content.resourceUri, connectionGeneration]);

  async function connectBridge() {
    const iframe = iframeRef.current;
    const mcpClient = clientRef.current;
    if (!iframe?.contentWindow || !html || !mcpClient || bridgeRef.current) return;
    try {
      const bridge = new AppBridge(
        null,
        { name: "Fanout", version: "0.2.0" },
        { openLinks: {}, serverTools: {}, logging: {} },
        { hostContext: { theme: "light", displayMode: "inline" } },
      );
      bridge.oncalltool = (params, extra) => mcpClient.request(
        { method: "tools/call", params },
        CallToolResultSchema,
        { signal: extra.signal },
      );
      bridgeRef.current = bridge;
      bridge.onsizechange = ({ height: requested }) => {
        if (requested) setHeight(Math.max(minimumHeight, Math.ceil(requested) + 32));
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
  return <Box component="iframe" ref={iframeRef} title="Fanout analysis view" sandbox="allow-scripts" scrolling="no" srcDoc={html} w="100%" bd={0} bg="var(--mantine-color-body)" style={{ display: "block", height, transition: "height 200ms ease" }} onLoad={() => void connectBridge()} />;
}
