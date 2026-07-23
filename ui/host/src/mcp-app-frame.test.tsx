import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { beforeEach, describe, expect, it, vi } from "vitest";

const mcp = vi.hoisted(() => ({
  connect: vi.fn().mockResolvedValue(undefined),
  readResource: vi.fn().mockImplementation(({ uri }: { uri: string }) => Promise.resolve({ contents: [{
    uri,
    mimeType: "text/html;profile=mcp-app",
    text: "<!doctype html><html><head></head><body><div id=\"root\"></div></body></html>",
    _meta: { ui: { csp: {} } },
  }] })),
  request: vi.fn().mockResolvedValue({ content: [] }),
  closeClient: vi.fn().mockResolvedValue(undefined),
  closeTransport: vi.fn().mockResolvedValue(undefined),
  clients: [] as Array<{ onclose?: () => void; onerror?: (error: Error) => void }>,
  clientOptions: [] as unknown[],
  bridgeClients: [] as unknown[],
  bridges: [] as Array<{ onsizechange?: (size: { height?: number }) => void }>,
}));

vi.mock("@modelcontextprotocol/sdk/client/index.js", () => ({
  Client: class {
    onclose?: () => void;
    onerror?: (error: Error) => void;
    constructor(_info: unknown, options: unknown) {
      mcp.clients.push(this);
      mcp.clientOptions.push(options);
    }
    connect = mcp.connect;
    readResource = mcp.readResource;
    request = mcp.request;
    close = mcp.closeClient;
  },
}));

vi.mock("@modelcontextprotocol/sdk/client/streamableHttp.js", () => ({
  StreamableHTTPClientTransport: class {
    close = mcp.closeTransport;
  },
}));

vi.mock("@modelcontextprotocol/ext-apps/app-bridge", () => ({
  AppBridge: class {
    oninitialized?: () => Promise<void>;
    oncalltool?: (params: unknown, extra: { signal: AbortSignal }) => Promise<unknown>;
    onsizechange?: (size: { height?: number }) => void;
    constructor(client: unknown) { mcp.bridgeClients.push(client); mcp.bridges.push(this); }
    async connect() { await this.oninitialized?.(); }
    async sendToolInput() {}
    async sendToolResult() {}
    async teardownResource() {}
  },
  PostMessageTransport: class {},
}));

import MCPAppFrame, { mcpAppCSP } from "./mcp-app-frame";

describe("MCPAppFrame", () => {
  beforeEach(async () => {
    // Let the previous frame's zero-delay shared-connection release finish
    // before clearing the module-level client fixture.
    await new Promise((resolve) => setTimeout(resolve, 0));
    vi.clearAllMocks();
    mcp.clients.length = 0;
    mcp.clientOptions.length = 0;
    mcp.bridgeClients.length = 0;
    mcp.bridges.length = 0;
  });

  it("shares one MCP transport across concurrent visualization blocks", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const resources = [
      { toolName: "observability_overview", slug: "observability-overview" },
      { toolName: "service_topology", slug: "service-topology" },
      { toolName: "service_performance", slug: "service-performance" },
      { toolName: "trace_detail", slug: "trace-detail" },
      { toolName: "search_logs", slug: "log-explorer" },
    ];

    await act(async () => {
      root.render(<MantineProvider>{resources.map(({ toolName, slug }) => <MCPAppFrame
        key={toolName}
        content={{ resourceUri: `ui://fanout/${slug}.html`, toolName }}
        onMessage={async () => undefined}
      />)}</MantineProvider>);
    });

    await vi.waitFor(() => expect(mcp.readResource).toHaveBeenCalledTimes(resources.length));
    expect(mcp.connect).toHaveBeenCalledTimes(1);
    expect(mcp.clientOptions[0]).toMatchObject({ capabilities: { extensions: {
      "io.modelcontextprotocol/ui": { mimeTypes: ["text/html;profile=mcp-app"] },
    } } });
    for (const frame of container.querySelectorAll("iframe")) {
      expect(frame.getAttribute("srcdoc")).toContain("Content-Security-Policy");
      expect(frame.getAttribute("srcdoc")).toContain("default-src 'none'");
      expect(frame.getAttribute("scrolling")).toBe("auto");
      await act(async () => frame.dispatchEvent(new Event("load")));
    }
    expect(mcp.bridgeClients).toEqual(resources.map(() => null));
    const firstFrame = container.querySelector("iframe")!;
    expect(firstFrame.style.height).toBe("620px");
    await act(async () => mcp.bridges[0].onsizechange?.({ height: 1080 }));
    expect(firstFrame.style.height).toBe("1112px");
    await act(async () => mcp.bridges[0].onsizechange?.({ height: 5000 }));
    expect(firstFrame.style.height).toBe("2000px");

    await act(async () => root.unmount());
    await vi.waitFor(() => expect(mcp.closeClient).toHaveBeenCalledTimes(1));
    expect(mcp.closeTransport).not.toHaveBeenCalled();
    container.remove();
  });

  it("retries mounted views through a server restart", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(<MantineProvider><MCPAppFrame
      content={{ resourceUri: "ui://fanout/observability-overview.html", toolName: "observability_overview" }}
      onMessage={async () => undefined}
    /></MantineProvider>));
    await vi.waitFor(() => expect(mcp.connect).toHaveBeenCalledTimes(1));

    mcp.connect.mockRejectedValueOnce(new Error("server restarting"));
    await act(async () => mcp.clients[0].onclose?.());
    await vi.waitFor(() => expect(mcp.connect).toHaveBeenCalledTimes(2));
    await vi.waitFor(() => expect(mcp.connect).toHaveBeenCalledTimes(3), { timeout: 3000 });
    await vi.waitFor(() => expect(mcp.readResource).toHaveBeenCalledTimes(2));

    await act(async () => root.unmount());
    container.remove();
  });

  it("does not retry a permanently invalid resource after reconnecting", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(<MantineProvider><MCPAppFrame
      content={{ resourceUri: "ui://fanout/observability-overview.html", toolName: "observability_overview" }}
      onMessage={async () => undefined}
    /></MantineProvider>));
    await vi.waitFor(() => expect(mcp.readResource).toHaveBeenCalledTimes(1));

    mcp.readResource.mockResolvedValueOnce({ contents: [{
      uri: "ui://fanout/wrong.html",
      mimeType: "text/html;profile=mcp-app",
      text: "<html></html>",
    }] });
    await act(async () => mcp.clients[0].onclose?.());
    await vi.waitFor(() => expect(mcp.readResource).toHaveBeenCalledTimes(2));
    await new Promise((resolve) => setTimeout(resolve, 1000));
    expect(mcp.readResource).toHaveBeenCalledTimes(2);

    await act(async () => root.unmount());
    container.remove();
  });

  it.each([
    [{ uri: "ui://fanout/other.html", mimeType: "text/html;profile=mcp-app", text: "<html></html>" }, "URI"],
    [{ uri: "ui://fanout/observability-overview.html", mimeType: "text/html", text: "<html></html>" }, "MIME"],
  ])("rejects a resource with invalid identity metadata (%s)", async (resource, _kind) => {
    mcp.readResource.mockResolvedValueOnce({ contents: [resource] });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<MantineProvider><MCPAppFrame
      content={{ resourceUri: "ui://fanout/observability-overview.html", toolName: "observability_overview" }}
      onMessage={async () => undefined}
    /></MantineProvider>));
    await vi.waitFor(() => expect(container.textContent).toContain("This view could not be loaded"));
    expect(container.querySelector("iframe")).toBeNull();
    await act(async () => root.unmount());
    container.remove();
  });

  it("builds CSP directives only from safe declared domains", () => {
    const policy = mcpAppCSP({ ui: { csp: {
      connectDomains: ["https://api.example.com", "wss://stream.example.com", "https://evil.example;script-src"],
      resourceDomains: ["https://cdn.example.com", "'unsafe-inline'"],
      frameDomains: ["https://frames.example.com"],
      baseUriDomains: ["https://base.example.com"],
    } } });

    expect(policy).toContain("connect-src https://api.example.com wss://stream.example.com");
    expect(policy).toContain("script-src 'self' 'unsafe-inline' https://cdn.example.com");
    expect(policy).toContain("frame-src https://frames.example.com");
    expect(policy).toContain("base-uri https://base.example.com");
    expect(policy).not.toContain("evil.example");
  });
});
