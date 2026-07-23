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
    constructor(client: unknown) { mcp.bridgeClients.push(client); }
    async connect() { await this.oninitialized?.(); }
    async sendToolInput() {}
    async sendToolResult() {}
    async teardownResource() {}
  },
  PostMessageTransport: class {},
}));

import MCPAppFrame from "./mcp-app-frame";

describe("MCPAppFrame", () => {
  beforeEach(() => {
    vi.clearAllMocks();
    mcp.clients.length = 0;
    mcp.clientOptions.length = 0;
    mcp.bridgeClients.length = 0;
  });

  it("shares one MCP transport across concurrent visualization blocks", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const resources = ["overview", "topology", "performance", "trace", "logs"];

    await act(async () => {
      root.render(<MantineProvider>{resources.map((name) => <MCPAppFrame
        key={name}
        content={{ resourceUri: `ui://fanout/${name}.html`, toolName: name }}
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
      await act(async () => frame.dispatchEvent(new Event("load")));
    }
    expect(mcp.bridgeClients).toEqual(resources.map(() => null));

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
      content={{ resourceUri: "ui://fanout/overview.html", toolName: "overview" }}
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

  it.each([
    [{ uri: "ui://fanout/other.html", mimeType: "text/html;profile=mcp-app", text: "<html></html>" }, "URI"],
    [{ uri: "ui://fanout/overview.html", mimeType: "text/html", text: "<html></html>" }, "MIME"],
  ])("rejects a resource with invalid identity metadata (%s)", async (resource, _kind) => {
    mcp.readResource.mockResolvedValueOnce({ contents: [resource] });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<MantineProvider><MCPAppFrame
      content={{ resourceUri: "ui://fanout/overview.html", toolName: "overview" }}
      onMessage={async () => undefined}
    /></MantineProvider>));
    await vi.waitFor(() => expect(container.textContent).toContain("This view could not be loaded"));
    expect(container.querySelector("iframe")).toBeNull();
    await act(async () => root.unmount());
    container.remove();
  });
});
