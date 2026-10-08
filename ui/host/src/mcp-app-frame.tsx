import {mcpAppCSP} from "./mcp-app-csp";
import { AppBridge, PostMessageTransport } from "@modelcontextprotocol/ext-apps/app-bridge";
import { Client, StreamableHTTPClientTransport } from "@modelcontextprotocol/client";
import { Alert, Box, Button, Center, FocusTrap, Loader, Text, useComputedColorScheme } from "@mantine/core";
import { useEffect, useRef, useState } from "react";
import { mcpAppContent, type MCPAppContent } from "./mcp-app-content";
import { authorizedFetch } from "./auth";

const mcpAppMIME = "text/html;profile=mcp-app";
const mcpUIExtension = "io.modelcontextprotocol/ui";
const maxConnectionAcquireAttempts = 3;
const maxReconnectAttempts = 5;
const maxAppHeight = 2000;

class InvalidMCPAppResourceError extends Error {}


type BrowserMCPConnection = {
  client: Client;
  tools: Promise<Awaited<ReturnType<Client["listTools"]>>>;
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
      tools: client.listTools(),
      references: 0,
      closed: false,
      closeListeners: new Set(),
    };
    // Handle rejection immediately even if the resource load fails first.
    void connection.tools.catch(() => undefined);
    client.onclose = () => invalidateBrowserMCPConnection(connection, false);
    client.onerror = () => invalidateBrowserMCPConnection(connection, true);
    return connection;
  } catch (cause) {
    await transport.close().catch(() => undefined);
    throw cause;
  }
}

async function acquireBrowserMCPConnection(): Promise<BrowserMCPConnection> {
  for (let attempt = 0; attempt < maxConnectionAcquireAttempts; attempt += 1) {
    if (!sharedConnectionPromise) {
      const created = createBrowserMCPConnection();
      sharedConnectionPromise = created;
      created.then((connection) => {
        if (sharedConnectionPromise === created && !connection.closed) sharedConnection = connection;
      }).catch(() => {
        if (sharedConnectionPromise === created) sharedConnectionPromise = null;
      });
    }
    const pending = sharedConnectionPromise;
    if (!pending) continue;
    const connection = await pending;
    if (connection.closed) {
      if (sharedConnectionPromise === pending) sharedConnectionPromise = null;
      continue;
    }
    if (connection.closeTimer) {
      clearTimeout(connection.closeTimer);
      connection.closeTimer = undefined;
    }
    connection.references += 1;
    return connection;
  }
  throw new Error("MCP connection closed during setup");
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

function record(value: unknown): Record<string, unknown> | undefined {
  return value !== null && typeof value === "object" && !Array.isArray(value) ? value as Record<string, unknown> : undefined;
}
type CachedResource = { text: string; _meta?: unknown };
const resources = new Map<string, { pending: Promise<CachedResource>; expires: number }>();
async function cachedResource(client: Client, uri: string): Promise<CachedResource> {
  const existing = resources.get(uri);
  if (existing && existing.expires > Date.now()) return existing.pending;
  const entry = { pending: Promise.resolve({ text: "" }) as Promise<CachedResource>, expires: Infinity };
  entry.pending = client.readResource({ uri }).then(resource => {
    const first = resource.contents[0];
    if (resource.contents.length !== 1 || !first || !("text" in first) || !first.text) throw new InvalidMCPAppResourceError("MCP App resource has no HTML content");
    if (first.uri !== uri) throw new InvalidMCPAppResourceError("MCP App resource URI does not match the requested URI");
    if (first.mimeType !== mcpAppMIME) throw new InvalidMCPAppResourceError("MCP App resource has an unsupported MIME type");
    const ttl = record(resource)?.ttlMs;
    entry.expires = Date.now() + (typeof ttl === "number" && ttl > 0 ? ttl : 300_000);
    return { text: first.text, _meta: first._meta };
  }).catch(cause => { if (resources.get(uri) === entry) resources.delete(uri); throw cause; });
  resources.set(uri, entry);
  return entry.pending;
}

function enforceMCPAppCSP(html: string, meta: unknown): string {
  const policy = mcpAppCSP(meta);
  const tag = `<meta http-equiv="Content-Security-Policy" content="${policy}">`;
  if (/<head(?:\s[^>]*)?>/i.test(html)) return html.replace(/<head(?:\s[^>]*)?>/i, (head) => `${head}${tag}`);
  if (/<html(?:\s[^>]*)?>/i.test(html)) return html.replace(/<html(?:\s[^>]*)?>/i, (root) => `${root}<head>${tag}</head>`);
  return `<!doctype html><html><head>${tag}</head><body>${html}</body></html>`;
}

export default function MCPAppFrame({ content: value }: { content: unknown }) {
  const content = mcpAppContent(value);
  if (!content) return <Alert color="bad" m="md">This view could not be loaded. Please try again.</Alert>;
  return <ValidatedMCPAppFrame content={content} />;
}

function ValidatedMCPAppFrame({ content }: { content: MCPAppContent }) {
  const iframeRef = useRef<HTMLIFrameElement>(null);
  const clientRef = useRef<Client | null>(null);
  const connectionRef = useRef<BrowserMCPConnection | null>(null);
  const bridgeRef = useRef<AppBridge | null>(null);
  const [html, setHTML] = useState("");
  // The app reports its own size through the bridge; the floor only covers
  // the moment before the first report.
  const minimumHeight = 240;
  const [displayMode, setDisplayMode] = useState<"inline" | "fullscreen">("inline");
  const closeRef = useRef<HTMLButtonElement>(null);
  const modeRef = useRef(displayMode);
  modeRef.current = displayMode;
  const changeDisplayMode = (mode: "inline" | "fullscreen") => {
    setDisplayMode(mode);
    bridgeRef.current?.setHostContext({ theme: colorSchemeRef.current, displayMode: mode, availableDisplayModes: ["inline", "fullscreen"] });
    if (mode === "inline") iframeRef.current?.focus();
  };
  useEffect(() => {
    if (displayMode !== "fullscreen") return;
    closeRef.current?.focus();
    const previous = document.body.style.overflow;
    document.body.style.overflow = "hidden";
    const escape = (event: KeyboardEvent) => { if (event.key === "Escape") { event.preventDefault(); changeDisplayMode("inline"); } };
    window.addEventListener("keydown", escape);
    return () => { document.body.style.overflow = previous; window.removeEventListener("keydown", escape); };
  }, [displayMode]);
  const [height, setHeight] = useState(minimumHeight);
  const [error, setError] = useState("");
  const [connectionGeneration, setConnectionGeneration] = useState(0);
  const reconnectAttemptRef = useRef(0);
  // An embedded view cannot see the host's stylesheet, so the scheme travels to
  // it as host context. The ref is what a bridge reads at connect time; the
  // effect below pushes every later change to a bridge already open.
  const colorScheme = useComputedColorScheme("light");
  const colorSchemeRef = useRef(colorScheme);
  colorSchemeRef.current = colorScheme;

  useEffect(() => {
    bridgeRef.current?.setHostContext({ theme: colorScheme, displayMode: modeRef.current, availableDisplayModes: ["inline", "fullscreen"] });
  }, [colorScheme]);

  useEffect(() => { reconnectAttemptRef.current = 0; }, [content.resource_uri]);

  useEffect(() => {
    let disposed = false;
    let retryTimer: ReturnType<typeof setTimeout> | undefined;
    setDisplayMode("inline");
    setHTML("");
    setError("");
    const scheduleReconnect = () => {
      const attempt = reconnectAttemptRef.current + 1;
      if (attempt > maxReconnectAttempts) return;
      reconnectAttemptRef.current = attempt;
      const delay = attempt === 1 ? 0 : Math.min(750 * 2 ** (attempt - 2), 6000);
      if (delay === 0) setConnectionGeneration((generation) => generation + 1);
      else retryTimer = setTimeout(() => setConnectionGeneration((generation) => generation + 1), delay);
    };
    const reconnect = () => scheduleReconnect();
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
        const first = await cachedResource(connection.client, content.resource_uri);
        await connection.tools;
        if (!disposed) {
          reconnectAttemptRef.current = 0;
          setHTML(enforceMCPAppCSP(first.text, first._meta));
        }
      } catch (cause) {
        console.error("MCP app resource load failed", cause);
        if (!disposed) {
          setError("This view could not be loaded. Please try again.");
          // Retry only a bounded series that began with a live connection
          // dying. Invalid resources and initial-load failures fail closed.
          if (reconnectAttemptRef.current > 0 && !(cause instanceof InvalidMCPAppResourceError)) scheduleReconnect();
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
  }, [content.resource_uri, connectionGeneration]);

  async function connectBridge() {
    const iframe = iframeRef.current;
    const mcpClient = clientRef.current;
    if (!iframe?.contentWindow || !html || !mcpClient || bridgeRef.current) return;
    try {
      const bridge = new AppBridge(
        null,
        { name: "Fanout", version: "0.2.0" },
        { serverTools: {}, logging: {} },
        { hostContext: { theme: colorSchemeRef.current, displayMode: "inline", availableDisplayModes: ["inline", "fullscreen"] } },
      );
      const connection = connectionRef.current;
      if (!connection) throw new Error("MCP connection unavailable");
      bridge.oncalltool = async (params, extra) => {
        const tools = await connection.tools;
        const tool = tools.tools.find(tool => tool.name === params.name);
        const visibility = record(record(tool?._meta)?.ui)?.visibility;
        if (!Array.isArray(visibility) || !visibility.includes("app") || /^(?:create|edit|replace|restore|delete)_/.test(params.name)) throw new Error("This tool is unavailable in the app");
        return mcpClient.callTool(params, { signal: extra.mcpReq.signal });
      };
      bridge.onrequestdisplaymode = async ({ mode }) => {
        if (mode !== "inline" && mode !== "fullscreen") return { mode: modeRef.current };
        changeDisplayMode(mode);
        return { mode };
      };
      bridgeRef.current = bridge;
      bridge.onsizechange = ({ height: requested }) => {
        if (requested) setHeight(Math.min(maxAppHeight, Math.max(minimumHeight, Math.round(requested))));
      };
      bridge.onmessage = async () => ({ isError: true });
      bridge.oninitialized = async () => {
        await bridge.sendToolInput({ arguments: content.tool_input ?? {} });
        await bridge.sendToolResult({
          content: [{ type: "text", text: JSON.stringify(content.tool_result ?? {}) }],
          structuredContent: content.tool_result as Record<string, unknown> | undefined,
          isError: content.is_error,
        });
      };
      await bridge.connect(new PostMessageTransport(iframe.contentWindow, iframe.contentWindow));
    } catch (cause) {
      console.error("MCP app bridge connect failed", cause);
      setError("This view could not be loaded. Please try again.");
    }
  }

  if (error) return <Alert color="bad" m="md">{error}</Alert>;
  if (!html) return <Center mih={180} p="xl"><Loader size="sm" /><Text c="dimmed" size="sm" ml="sm">Preparing view…</Text></Center>;
  // Keep the iframe in the same DOM position: reparenting or remounting reloads the app.
  return <FocusTrap active={displayMode === "fullscreen"}><Box data-app-fullscreen={displayMode === "fullscreen" ? "" : undefined}
    role={displayMode === "fullscreen" ? "dialog" : undefined} aria-modal={displayMode === "fullscreen" ? true : undefined} aria-label={displayMode === "fullscreen" ? "Fanout analysis view" : undefined}
    bg="var(--mantine-color-body)" style={displayMode === "fullscreen" ? { position: "fixed", inset: 0, zIndex: 1000, display: "flex", flexDirection: "column" } : undefined}>
    {displayMode === "fullscreen" && <Box p="xs" ta="right"><Button ref={closeRef} data-autofocus size="compact-sm" variant="default" aria-label="Close analysis view" onClick={() => changeDisplayMode("inline")}>Close</Button></Box>}
    <Box component="iframe" ref={iframeRef} title="Fanout analysis view" sandbox="allow-scripts" scrolling="auto" srcDoc={html} w="100%" bd={0} bg="var(--mantine-color-body)" style={{ display: "block", height: displayMode === "fullscreen" ? "100%" : height, ...(displayMode === "fullscreen" ? { flex: "1 1 0", minHeight: 0 } : {}) }} onLoad={() => void connectBridge()} />
  </Box></FocusTrap>;
}
