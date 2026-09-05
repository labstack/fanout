import { MantineProvider } from "@mantine/core";
import { createRootRoute, createRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

declare global {
  interface Window { happyDOM: { setURL(url: string): void } }
}

const agentMocks = vi.hoisted(() => ({ runAgent: vi.fn(async () => undefined), abortRun: vi.fn(), instances: [] as Array<{ threadId: string }> }));

vi.mock("@ag-ui/client", () => ({
  HttpAgent: class {
    threadId: string;
    messages: Array<{ id: string; role: string; content?: string }> = [];
    constructor(options: { threadId: string }) { this.threadId = options.threadId; agentMocks.instances.push(this); }
    subscribe() { return { unsubscribe: () => undefined }; }
    setMessages(messages: Array<{ id: string; role: string; content?: string }>) { this.messages = messages; }
    addMessage(message: { id: string; role: string; content?: string }) { this.messages = [...this.messages, message]; }
    runAgent = agentMocks.runAgent;
    abortRun = agentMocks.abortRun;
  },
}));

vi.mock("./auth", () => ({
  default: ({ children }: { children: React.ReactNode }) => children,
  useRuntimeStatus: () => ({ setup_required: false, auth_mode: "local", agent_available: true, smtp_configured: true, self_signup: false }),
  useViewer: () => ({ id: "viewer-1", email: "v@example.com", name: "Vee", role: "admin" }),
  authorizedFetch: (input: RequestInfo | URL, init?: RequestInit) => fetch(input, init),
  logout: vi.fn(async () => undefined),
  clearSession: vi.fn(),
}));

import App from "./App";
import { ChatPage } from "./chat";

const fetchMock = vi.fn<typeof fetch>();

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function setValue(input: HTMLTextAreaElement, value: string) {
  Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(input, value);
  input.dispatchEvent(new InputEvent("input", { bubbles: true, data: value, inputType: "insertText" }));
}

describe("Session", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    agentMocks.runAgent.mockClear();
    agentMocks.abortRun.mockClear();
    agentMocks.instances.length = 0;
    fetchMock.mockImplementation(async (input) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/dashboards") return json({ dashboards: [] });
      if (url.pathname === "/api/agent/threads") return json({ threads: [], nextCursor: "" });
      if (url.pathname.startsWith("/api/agent/threads/")) return json({ message: "not found" }, 404);
      throw new Error(`unexpected request: ${url.pathname}`);
    });
    window.happyDOM.setURL("https://fanout.example.com/chat");
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    document.body.innerHTML = "";
  });

  it("starts a draft without fetching a thread and names the thread on first send", async () => {
    const rootRoute = createRootRoute({ component: App });
    const chatIndex = createRoute({ getParentRoute: () => rootRoute, path: "/chat/", component: ChatPage });
    const chatThread = createRoute({ getParentRoute: () => rootRoute, path: "/chat/$threadId", component: ChatPage });
    const router = createRouter({ routeTree: rootRoute.addChildren([chatIndex, chatThread]) });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(<MantineProvider><RouterProvider router={router} /></MantineProvider>));
    await vi.waitFor(() => expect(document.querySelector('textarea[aria-label="Message Fanout"]')).not.toBeNull());
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes("/api/agent/threads/"))).toBe(false);

    const composer = document.querySelector('textarea[aria-label="Message Fanout"]') as HTMLTextAreaElement;
    await act(async () => setValue(composer, "Summarize system health"));
    await act(async () => composer.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));

    await vi.waitFor(() => expect(agentMocks.runAgent).toHaveBeenCalledTimes(1));
    await vi.waitFor(() => expect(window.location.pathname).toMatch(/^\/chat\/[0-9a-f-]{36}$/));
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes("/api/agent/threads/"))).toBe(false);
    expect(document.body.textContent).not.toContain("no longer exists");
    // Naming the draft must not restart the session underneath the run.
    expect(agentMocks.abortRun).not.toHaveBeenCalled();
    expect(document.body.textContent).toContain("Summarize system health");

    await act(async () => root.unmount());
  });

  it("explains a thread that no longer exists", async () => {
    window.happyDOM.setURL("https://fanout.example.com/chat/deleted-thread");
    const rootRoute = createRootRoute({ component: App });
    const chatThread = createRoute({ getParentRoute: () => rootRoute, path: "/chat/$threadId", component: ChatPage });
    const router = createRouter({ routeTree: rootRoute.addChildren([chatThread]) });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(<MantineProvider><RouterProvider router={router} /></MantineProvider>));
    await vi.waitFor(() => expect(document.body.textContent).toContain("This chat no longer exists"));
    expect(document.body.textContent).toContain("New chat");
    await act(async () => root.unmount());
  });
});
