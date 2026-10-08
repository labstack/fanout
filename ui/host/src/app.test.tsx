import { MantineProvider } from "@mantine/core";
import type { AgentSubscriber, Message } from "@ag-ui/client";
import { QueryClient } from "@tanstack/react-query";
import { createRootRoute, createRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import App from "./App";
import { ChatPage } from "./chat";
import { createDashboardPrompt, useFanoutApp } from "./app-context";
import { parseSearch, toSearchParams } from "./dashboards/search";

declare global {
  interface Window { happyDOM: { setURL(url: string): void } }
}

const agentMocks = vi.hoisted(() => ({ runAgent: vi.fn(async () => undefined), abortRun: vi.fn(), instances: [] as Array<{ threadId: string; subscriber?: AgentSubscriber }> }));

vi.mock("@ag-ui/client", () => ({
  HttpAgent: class {
    threadId: string;
    subscriber?: AgentSubscriber;
    messages: Array<{ id: string; role: string; content?: string }> = [];
    constructor(options: { threadId: string }) { this.threadId = options.threadId; agentMocks.instances.push(this); }
    subscribe(subscriber: AgentSubscriber) { this.subscriber = subscriber; return { unsubscribe: () => undefined }; }
    setMessages(messages: Array<{ id: string; role: string; content?: string }>) { this.messages = messages; }
    addMessage(message: { id: string; role: string; content?: string }) { this.messages = [...this.messages, message]; }
    runAgent = agentMocks.runAgent;
    abortRun = agentMocks.abortRun;
  },
}));

const defaultViewer = { id: "viewer-1", email: "v@example.com", name: "Vee", role: "admin" };
const viewerMock = vi.hoisted(() => ({ current: { id: "viewer-1", email: "v@example.com", name: "Vee", role: "admin" } }));

vi.mock("./auth", () => ({
  default: ({ children }: { children: React.ReactNode }) => children,
  useRuntimeStatus: () => ({ setup_required: false, auth_mode: "local", agent_available: true, smtp_configured: true, self_signup: false }),
  useViewer: () => viewerMock.current,
  authorizedFetch: (input: RequestInfo | URL, init?: RequestInit) => fetch(input, init),
  logout: vi.fn(async () => undefined),
  clearSession: vi.fn(),
}));


const fetchMock = vi.fn<typeof fetch>();

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function button(text: string) {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((item) => item.textContent?.trim() === text);
}

function setValue(input: HTMLTextAreaElement, value: string) {
  Object.getOwnPropertyDescriptor(HTMLTextAreaElement.prototype, "value")?.set?.call(input, value);
  input.dispatchEvent(new InputEvent("input", { bubbles: true, data: value, inputType: "insertText" }));
}

function SessionProbe() {
  const {provisional,send}=useFanoutApp();
  return <><ChatPage/><output data-provisional-state>{JSON.stringify(provisional)}</output><button onClick={()=>void send("Hello")}>Start probe</button></>;
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
    vi.restoreAllMocks();
    vi.unstubAllGlobals();
    document.body.innerHTML = "";
    viewerMock.current = defaultViewer;
  });

  it.each(["create_dashboard", "edit_dashboard", "replace_dashboard"])("refreshes the rail and links the saved dashboard at the streamed %s result", async (name) => {
    const invalidate = vi.spyOn(QueryClient.prototype, "invalidateQueries");
    const rootRoute = createRootRoute({ component: App });
    const chat = createRoute({ getParentRoute: () => rootRoute, path: "/chat/", component: ChatPage });
    const detail = createRoute({ getParentRoute: () => rootRoute, path: "/dashboards/$dashboardId", component: () => <div>Saved dashboard</div> });
    const router = createRouter({ routeTree: rootRoute.addChildren([chat, detail]) });
    const host = document.createElement("div"); document.body.append(host); const root = createRoot(host);
    try {
      await act(async () => root.render(<MantineProvider><RouterProvider router={router} /></MantineProvider>));
      await vi.waitFor(() => expect(document.querySelector("textarea")).not.toBeNull());
      await vi.waitFor(() => expect(fetchMock.mock.calls.some(([url]) => String(url) === "/api/dashboards")).toBe(true));
      fetchMock.mockImplementation(async (input) => {
        const url = new URL(String(input), "http://localhost");
        if (url.pathname === "/api/dashboards") return json({ dashboards: [{ id: "saved-1", name: "Cart errors", version: 2, panel_count: 2 }] });
        if (url.pathname === "/api/agent/threads") return json({ threads: [], nextCursor: "" });
        return json({ message: "not found" }, 404);
      });
      const content = JSON.stringify({ dashboard: { id: "saved-1", name: "Cart errors", version: 2, spec: { name: "Cart errors" } }, receipt: {base_version: name === "create_dashboard" ? 0 : 1, version: 2, changes: [], layout_changed: false, save_check: {checked: true, elapsed_ms: 12, panels: []}} });
      const messages = [
        { id: "user", role: "user", content: "Build errors" },
        { id: "provisional", role: "reasoning", content: "Saving it now" },
        { id: "before", role: "assistant", content: "", toolCalls: [{ id: "call-1", type: "function", function: { name, arguments: "{}" } }] },
        { id: "result", role: "tool", toolCallId: "call-1", content },
        { id: "after", role: "assistant", content: "All done" },
      ] as Message[];
      const subscriber = agentMocks.instances.at(-1)!.subscriber!;
      await act(async () => {
        await subscriber.onToolCallResultEvent?.({ event: { type: "TOOL_CALL_RESULT", messageId: "result", toolCallId: "call-1", content }, messages } as unknown as Parameters<NonNullable<AgentSubscriber["onToolCallResultEvent"]>>[0]);
        await subscriber.onMessagesChanged?.({ messages } as unknown as Parameters<NonNullable<AgentSubscriber["onMessagesChanged"]>>[0]);
      });
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ["dashboards"] });
      expect(invalidate).toHaveBeenCalledWith({ queryKey: ["dashboard", "saved-1"] });
      await vi.waitFor(() => expect(host.querySelector('nav a[href="/dashboards/saved-1"]')).not.toBeNull());
      const link = host.querySelector<HTMLAnchorElement>('.chat-scroll a[href="/dashboards/saved-1"]');
      expect(link?.textContent).toBe("Open dashboard · saved v2");
      expect(link?.closest("[data-dashboard-result]")?.textContent).toContain("Saved v2");
      const transcript = host.querySelector('[role="log"]')!.textContent!;
      expect(transcript).not.toContain("Saving it now");
      expect(transcript.indexOf("Open dashboard")).toBeLessThan(transcript.indexOf("All done"));
      await act(async () => link!.click());
      await vi.waitFor(() => expect(router.state.location.pathname).toBe("/dashboards/saved-1"));
    } finally { await act(async () => root.unmount()); }
  });

  it.each([
    ["get_dashboard", JSON.stringify({ dashboard: { id: "read-1", name: "Read only" } })],
    ["create_dashboard", JSON.stringify({ error: "Save failed" })],
    ["edit_dashboard", "not JSON"],
  ])("ignores unrelated or unsuccessful tool results (%s)", async (name, content) => {
    const invalidate = vi.spyOn(QueryClient.prototype, "invalidateQueries");
    const rootRoute = createRootRoute({ component: App });
    const chat = createRoute({ getParentRoute: () => rootRoute, path: "/chat/", component: ChatPage });
    const router = createRouter({ routeTree: rootRoute.addChildren([chat]) });
    const host = document.createElement("div"); document.body.append(host); const root = createRoot(host);
    try {
      await act(async () => root.render(<MantineProvider><RouterProvider router={router} /></MantineProvider>));
      await vi.waitFor(() => expect(document.querySelector("textarea")).not.toBeNull());
      const messages = [
        { id: "call", role: "assistant", content: "", toolCalls: [{ id: "call-1", type: "function", function: { name, arguments: "{}" } }] },
        { id: "result", role: "tool", toolCallId: "call-1", content },
      ] as Message[];
      const subscriber = agentMocks.instances.at(-1)!.subscriber!;
      await act(async () => {
        await subscriber.onToolCallResultEvent?.({ event: { type: "TOOL_CALL_RESULT", messageId: "result", toolCallId: "call-1", content }, messages } as unknown as Parameters<NonNullable<AgentSubscriber["onToolCallResultEvent"]>>[0]);
        await subscriber.onMessagesChanged?.({ messages } as unknown as Parameters<NonNullable<AgentSubscriber["onMessagesChanged"]>>[0]);
      });
      expect(invalidate).not.toHaveBeenCalled();
      expect(host.querySelector("[data-dashboard-result]")).toBeNull();
    } finally { await act(async () => root.unmount()); }
  });

  it("opens a rail dashboard and clears the previous dashboard variable state", async () => {
    window.happyDOM.setURL("https://fanout.example.com/dashboards/dash-main?var-service=checkout&window=6h");
    fetchMock.mockImplementation(async (input) => {
      if (String(input) === "/api/dashboards") return json({ dashboards: [
        { id: "dash-main", name: "System overview", description: "", is_default: true, version: 1, panel_count: 4, updated_at: "2026-07-22 03:00:00" },
        { id: "dash-checkout", name: "Checkout", description: "", is_default: false, version: 1, panel_count: 2, updated_at: "2026-07-22 03:00:00" },
      ] });
      return json({ threads: [], nextCursor: "" });
    });
    const rootRoute = createRootRoute({ component: App });
    const detail = createRoute({ getParentRoute: () => rootRoute, path: "/dashboards/$dashboardId", validateSearch: (raw: Record<string, unknown>) => toSearchParams(parseSearch(raw)), component: () => <div>Dashboard</div> });
    const router = createRouter({ routeTree: rootRoute.addChildren([detail]) });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<MantineProvider><RouterProvider router={router} /></MantineProvider>));
    await vi.waitFor(() => expect(document.querySelector('a[href="/dashboards/dash-checkout"]')).not.toBeNull());
    expect(router.state.location.search).toHaveProperty("var-service", "checkout");
    await act(async () => (document.querySelector('a[href="/dashboards/dash-checkout"]') as HTMLAnchorElement).click());
    await vi.waitFor(() => expect(router.state.location.pathname).toBe("/dashboards/dash-checkout"));
    expect(router.state.location.search).toEqual({});
    expect(window.location.search).toBe("");
    await act(async () => root.unmount());
  });

  it("opens chat from the empty rail with the dashboard page starter prompt", async () => {
    const rootRoute = createRootRoute({ component: App });
    const chatIndex = createRoute({ getParentRoute: () => rootRoute, path: "/chat/", component: ChatPage });
    const chatThread = createRoute({ getParentRoute: () => rootRoute, path: "/chat/$threadId", component: ChatPage });
    const router = createRouter({ routeTree: rootRoute.addChildren([chatIndex, chatThread]) });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<MantineProvider><RouterProvider router={router} /></MantineProvider>));
    await vi.waitFor(() => expect(button("New dashboard")).not.toBeUndefined());
    await act(async () => button("New dashboard")!.click());
    await vi.waitFor(() => expect(agentMocks.runAgent).toHaveBeenCalledOnce());
    expect(window.location.pathname).toMatch(/^\/chat\/[0-9a-f-]{36}$/);
    expect(document.body.textContent).toContain(createDashboardPrompt);
    expect(createDashboardPrompt).toBe("Build me a dashboard for my services, showing request volume, latency, and errors. Ask me which services to monitor first.");
    await act(async () => root.unmount());
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

  it.each(["failure_callback","run_throw","thread_switch"])("clears provisional context on %s independently of the running render gate",async terminal=>{
    const log=vi.spyOn(console,"error").mockImplementation(()=>undefined);
    let rejectRun:(reason:Error)=>void=()=>{};
    if(terminal==='run_throw')agentMocks.runAgent.mockImplementationOnce(()=>new Promise<undefined>((_resolve,reject)=>{rejectRun=reject;}));
    fetchMock.mockImplementation(async input=>{
      const path=new URL(String(input),"https://fanout.example.com").pathname;
      return json(path==='/api/dashboards'?{dashboards:[]}:path==='/api/agent/threads'?{threads:[],nextCursor:""}:{messages:[]});
    });
    window.happyDOM.setURL("https://fanout.example.com/chat/thread");
    const rootRoute=createRootRoute({component:App});
    const chat=createRoute({getParentRoute:()=>rootRoute,path:"/chat/$threadId",component:SessionProbe});
    const router=createRouter({routeTree:rootRoute.addChildren([chat])});
    const host=document.createElement("div");document.body.append(host);const root=createRoot(host);
    try {
      await act(async()=>root.render(<MantineProvider><RouterProvider router={router}/></MantineProvider>));
      await vi.waitFor(()=>expect(document.querySelector('textarea')?.disabled).toBe(false));
      await act(async()=>button("Start probe")!.click());
      const subscriber=agentMocks.instances.at(-1)!.subscriber!;
      for(const event of [{type:"REASONING_MESSAGE_START",messageId:"provisional",role:"reasoning"},{type:"REASONING_MESSAGE_CONTENT",messageId:"provisional",delta:"Unfinished narration"}])await act(async()=>{await subscriber.onEvent?.({event,messages:[]} as unknown as Parameters<NonNullable<AgentSubscriber["onEvent"]>>[0]);});
      expect(document.querySelector('[data-provisional-state]')?.textContent).toContain("Unfinished narration");
      if(terminal==='failure_callback')await act(async()=>{await subscriber.onRunFailed?.({error:new Error("socket closed"),messages:[]} as unknown as Parameters<NonNullable<AgentSubscriber["onRunFailed"]>>[0]);});
      else if(terminal==='run_throw')await act(async()=>rejectRun(new Error("run failed")));
      else await act(async()=>{await router.navigate({to:"/chat/$threadId",params:{threadId:"another"}});});
      await vi.waitFor(()=>expect(document.querySelector('[data-provisional-state]')?.textContent).toBe('null'));
    }finally{log.mockRestore();await act(async()=>root.unmount());}
  });

  it("forgets a draft whose first run failed", async () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    agentMocks.runAgent.mockRejectedValueOnce(new Error("run failed"));
    const rootRoute = createRootRoute({ component: App });
    const chatIndex = createRoute({ getParentRoute: () => rootRoute, path: "/chat/", component: ChatPage });
    const chatThread = createRoute({ getParentRoute: () => rootRoute, path: "/chat/$threadId", component: ChatPage });
    const router = createRouter({ routeTree: rootRoute.addChildren([chatIndex, chatThread]) });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(<MantineProvider><RouterProvider router={router} /></MantineProvider>));
    await vi.waitFor(() => expect(document.querySelector('textarea[aria-label="Message Fanout"]')).not.toBeNull());

    const composer = document.querySelector('textarea[aria-label="Message Fanout"]') as HTMLTextAreaElement;
    await act(async () => setValue(composer, "Summarize system health"));
    await act(async () => composer.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true })));

    await vi.waitFor(() => expect(document.body.textContent).toContain("Fanout could not complete this analysis"));
    const threadID = window.location.pathname.slice("/chat/".length);
    expect(threadID).toMatch(/^[0-9a-f-]{36}$/);
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes(`/api/agent/threads/${threadID}`))).toBe(false);

    // Open another chat, then come back. The failed draft is no longer
    // remembered, so the session asks the server instead of opening an empty
    // pane, and learns the thread was never persisted.
    await act(async () => { await router.navigate({ to: "/chat/$threadId", params: { threadId: "another-thread" } }); });
    await act(async () => { await router.navigate({ to: "/chat/$threadId", params: { threadId: threadID } }); });

    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([input]) => String(input).includes(`/api/agent/threads/${threadID}`))).toBe(true));
    await vi.waitFor(() => expect(document.body.textContent).toContain("This chat no longer exists"));

    await act(async () => root.unmount());
    consoleError.mockRestore();
  });

  // Retry on a restore that failed has no run to replay: it has to ask the
  // server for the thread again, which is what tells the reader whether the
  // failure was momentary or the thread is gone.
  it("asks for the thread again when the restore is retried", async () => {
    const consoleError = vi.spyOn(console, "error").mockImplementation(() => undefined);
    window.happyDOM.setURL("https://fanout.example.com/chat/broken-thread");
    let threadRequests = 0;
    fetchMock.mockImplementation(async (input) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/dashboards") return json({ dashboards: [] });
      if (url.pathname === "/api/agent/threads") return json({ threads: [], nextCursor: "" });
      if (url.pathname.startsWith("/api/agent/threads/")) {
        threadRequests += 1;
        return threadRequests === 1 ? json({ message: "boom" }, 500) : json({ message: "not found" }, 404);
      }
      throw new Error(`unexpected request: ${url.pathname}`);
    });
    const rootRoute = createRootRoute({ component: App });
    const chatThread = createRoute({ getParentRoute: () => rootRoute, path: "/chat/$threadId", component: ChatPage });
    const router = createRouter({ routeTree: rootRoute.addChildren([chatThread]) });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(<MantineProvider><RouterProvider router={router} /></MantineProvider>));
    await vi.waitFor(() => expect(document.body.textContent).toContain("This chat could not be restored"));
    expect(threadRequests).toBe(1);

    await act(async () => button("Retry")?.click());
    await vi.waitFor(() => expect(threadRequests).toBe(2));
    await vi.waitFor(() => expect(document.body.textContent).toContain("This chat no longer exists"));

    await act(async () => root.unmount());
    consoleError.mockRestore();
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

  it("the burger's label names the action it performs, not a fixed state", async () => {
    const rootRoute = createRootRoute({ component: App });
    const chatIndex = createRoute({ getParentRoute: () => rootRoute, path: "/chat/", component: ChatPage });
    const router = createRouter({ routeTree: rootRoute.addChildren([chatIndex]) });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(<MantineProvider><RouterProvider router={router} /></MantineProvider>));
    await vi.waitFor(() => expect(document.querySelector('button[aria-label="Open navigation"]')).not.toBeNull());

    await act(async () => (document.querySelector('button[aria-label="Open navigation"]') as HTMLButtonElement).click());
    expect(document.querySelector('button[aria-label="Close navigation"]')).not.toBeNull();
    expect(document.querySelector('button[aria-label="Open navigation"]')).toBeNull();

    await act(async () => (document.querySelector('button[aria-label="Close navigation"]') as HTMLButtonElement).click());
    expect(document.querySelector('button[aria-label="Open navigation"]')).not.toBeNull();

    await act(async () => root.unmount());
  });

  it("falls back to the email initial when the viewer's name is only whitespace", async () => {
    viewerMock.current = { id: "viewer-1", email: "v@example.com", name: "   ", role: "admin" };
    const rootRoute = createRootRoute({ component: App });
    const chatIndex = createRoute({ getParentRoute: () => rootRoute, path: "/chat/", component: ChatPage });
    const router = createRouter({ routeTree: rootRoute.addChildren([chatIndex]) });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);

    await act(async () => root.render(<MantineProvider><RouterProvider router={router} /></MantineProvider>));
    await vi.waitFor(() => expect(document.querySelector('button[aria-label="Account menu"]')).not.toBeNull());
    expect(document.querySelector('button[aria-label="Account menu"]')?.textContent?.trim()).toBe("V");

    await act(async () => root.unmount());
  });
});
