import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { describe, expect, it, vi } from "vitest";

const mcp = vi.hoisted(() => ({
  connect: vi.fn().mockResolvedValue(undefined),
  readResource: vi.fn().mockResolvedValue({ contents: [{ text: "<!doctype html><div id=\"root\"></div>" }] }),
  closeClient: vi.fn().mockResolvedValue(undefined),
  closeTransport: vi.fn().mockResolvedValue(undefined),
}));

vi.mock("@modelcontextprotocol/sdk/client/index.js", () => ({
  Client: class {
    connect = mcp.connect;
    readResource = mcp.readResource;
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
    async connect() { await this.oninitialized?.(); }
    async sendToolInput() {}
    async sendToolResult() {}
    async teardownResource() {}
  },
  PostMessageTransport: class {},
}));

import MCPAppFrame from "./mcp-app-frame";

describe("MCPAppFrame", () => {
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

    await act(async () => root.unmount());
    await vi.waitFor(() => expect(mcp.closeClient).toHaveBeenCalledTimes(1));
    expect(mcp.closeTransport).toHaveBeenCalledTimes(1);
    container.remove();
  });
});
