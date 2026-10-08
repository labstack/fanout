import { MantineProvider } from "@mantine/core";
import type { Message } from "@ag-ui/client";
import { act, createRef, type FormEvent } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FanoutAppContext, useFanoutApp, type FanoutAppContextValue } from "./app-context";
import { createRootRoute, createRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import App from "./App";
import { ChatPage } from "./chat";
import { saved as receiptFixture, build as receiptBuild, user, call, result } from "../tests/dashboard-receipts";
import { fixture } from "../tests/fixtures";
vi.mock("./mcp-app-frame", () => ({ default: () => <div data-app-frame>Panel frame</div> }));
vi.mock("./auth", () => ({
  default: ({ children }: { children: React.ReactNode }) => children,
  useRuntimeStatus: () => ({ agent_available: true, setup_required: false, auth_mode: "local" }),
  useViewer: () => ({ id: "viewer", email: "viewer@example.test", name: "Viewer", role: "admin" }),
  authorizedFetch: (input: RequestInfo | URL, init?: RequestInit) => fetch(input, init),
  logout: vi.fn(), clearSession: vi.fn(),
}));

function StreamChat() {
  const { send } = useFanoutApp();
  return <><ChatPage /><button onClick={() => void send("Sort latency")}>Begin stream</button></>;
}

async function mountStreamChat(stored: Message[] = [{ id: "old-answer", role: "assistant", content: "Earlier answer" }], start = true) {
  let controller: ReadableStreamDefaultController<Uint8Array> | undefined;
  const requests: Array<{ messages: Message[] }> = [];
  const fetchMock = vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(input), "https://fanout.example.test").pathname;
    if (path === "/api/agent/runs") {
      requests.push(JSON.parse(String(init?.body)));
      return new Response(new ReadableStream<Uint8Array>({ start(c) { controller = c; init?.signal?.addEventListener("abort", () => c.error(new DOMException("Aborted", "AbortError")), { once: true }); } }), { headers: { "Content-Type": "text/event-stream" } });
    }
    const body = path === "/api/dashboards" ? { dashboards: [] } : path === "/api/agent/threads" ? { threads: [], nextCursor: "" } : { messages: stored };
    return Response.json(body);
  });
  vi.stubGlobal("fetch", fetchMock);
  (window as unknown as { happyDOM: { setURL(url: string): void } }).happyDOM.setURL("https://fanout.example.test/chat/thread-stream");
  const rootRoute = createRootRoute({ component: App });
  const chat = createRoute({ getParentRoute: () => rootRoute, path: "/chat/$threadId", component: StreamChat });
  const dashboard = createRoute({ getParentRoute: () => rootRoute, path: "/dashboards/$dashboardId", component: () => <div>Dashboard</div> });
  const router = createRouter({ routeTree: rootRoute.addChildren([chat, dashboard]) });
  const host = document.createElement("div"); document.body.append(host); const root = createRoot(host);
  await act(async () => root.render(<MantineProvider><RouterProvider router={router} /></MantineProvider>));
  await vi.waitFor(() => expect(document.querySelector("textarea")?.disabled).toBe(false));
  if (start) {
    await act(async () => button("Begin stream")!.click());
    await vi.waitFor(() => expect(controller).toBeDefined());
  }
  const emit = async (event: Record<string, unknown>) => { await act(async () => controller!.enqueue(new TextEncoder().encode(`data: ${JSON.stringify(event)}\n\n`))); };
  const close = async () => { await act(async () => controller!.close()); };
  const begin = async () => { const count = requests.length; await act(async () => button("Begin stream")!.click()); await vi.waitFor(() => expect(requests).toHaveLength(count + 1)); };
  return { root, emit, close, begin, requests };
}

async function reasoning(emit: (event: Record<string, unknown>) => Promise<void>, text: string) {
  await emit({type:"REASONING_START",messageId:"provisional"});
  await emit({type:"REASONING_MESSAGE_START",messageId:"provisional",role:"reasoning"});
  await emit({type:"REASONING_MESSAGE_CONTENT",messageId:"provisional",delta:text});
}

describe("provisional answer stream", () => {
  afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ""; });
  it("keeps running after the last tool receipt until final text arrives", async () => {
    const { root, emit, close } = await mountStreamChat();
    try {
      await emit({ type: "RUN_STARTED", threadId: "thread-stream", runId: "run" });
      await emit({ type: "TOOL_CALL_START", toolCallId: "call", toolCallName: "edit_dashboard", parentMessageId: "step" });
      await emit({ type: "TOOL_CALL_ARGS", toolCallId: "call", delta: "{}" });
      await emit({ type: "TOOL_CALL_END", toolCallId: "call" });
      expect(document.body.textContent).toContain("Building dashboard");
      await emit({ type: "TOOL_CALL_RESULT", toolCallId: "call", messageId: "result", content: JSON.stringify(receiptFixture) });
      await emit({ type: "CUSTOM", name: "model_call_usage", value: { step: 2 } });
      await vi.waitFor(() => expect(document.querySelector("[data-dashboard-result]")).not.toBeNull());
      expect(document.body.textContent).toContain("Analyzing your system");
      expect(document.querySelector('[data-build-receipt] [data-build-running]')).not.toBeNull();
      expect(document.querySelector('button[aria-label="Stop"]')).not.toBeNull();
      await emit({ type: "TEXT_MESSAGE_START", messageId: "final", role: "assistant" });
      expect(document.body.textContent).toContain("Analyzing your system");
      await emit({ type: "TEXT_MESSAGE_CONTENT", messageId: "final", delta: "Updated the latency panel." });
      await emit({ type: "TEXT_MESSAGE_END", messageId: "final" });
      await vi.waitFor(() => expect(document.body.textContent).toContain("Updated the latency panel."));
      expect(document.body.textContent).not.toContain("Analyzing your system");
      await emit({ type: "RUN_FINISHED", threadId: "thread-stream", runId: "run" }); await close();
      await vi.waitFor(() => expect(document.querySelector('button[aria-label="Stop"]')).toBeNull());
    } finally { await act(async () => root.unmount()); }
  });
  it.each(["empty", "error", "cancel"])("settles the running indicator for %s without final text", async outcome => {
    const { root, emit, close } = await mountStreamChat();
    const log = vi.spyOn(console, "error").mockImplementation(() => undefined);
    try {
      await emit({ type: "RUN_STARTED", threadId: "thread-stream", runId: "run" });
      expect(document.body.textContent).toContain("Analyzing your system");
      await emit({ type: "REASONING_START", messageId: "provisional" });
      await emit({ type: "REASONING_MESSAGE_START", messageId: "provisional", role: "reasoning" });
      await emit({ type: "REASONING_MESSAGE_CONTENT", messageId: "provisional", delta: "Unfinished narration" });
      expect(document.querySelector('[data-provisional]')?.textContent).toBe("Unfinished narration");
      if (outcome === "cancel") await act(async () => (document.querySelector('button[aria-label="Stop"]') as HTMLButtonElement).click());
      else {
        await emit(outcome === "error" ? { type: "RUN_ERROR", code: "provider_unavailable", message: "model provider unavailable" } : { type: "RUN_FINISHED", threadId: "thread-stream", runId: "run" });
        await close();
      }
      await vi.waitFor(() => expect(document.querySelector('button[aria-label="Stop"]')).toBeNull());
      expect(document.body.textContent).not.toContain("Analyzing your system");
      expect(document.querySelector('[data-provisional]')).toBeNull();
      expect(document.body.textContent).not.toContain("Unfinished narration");
      if (outcome === "error") expect(document.querySelector('[role="alert"]')?.textContent).toContain("Fanout could not reach the model provider. Please try again.");
      if (outcome === "cancel") {expect(document.querySelector('[role="alert"]')).toBeNull();expect(document.body.textContent).toContain("Stopped");}
    } finally { log.mockRestore(); await act(async () => root.unmount()); }
  });
  it("types provisional text live, collapses it on tools, and replaces the final block in place", async () => {
    const { root, emit, close } = await mountStreamChat();
    try {
      await emit({ type: "RUN_STARTED", threadId: "thread-stream", runId: "run" });
      await emit({ type: "REASONING_START", messageId: "provisional" });await emit({ type: "REASONING_MESSAGE_START", messageId: "provisional", role: "reasoning" });
      await emit({ type: "REASONING_MESSAGE_CONTENT", messageId: "provisional", delta: "Fixing the sort. Checking another thing." });
      await vi.waitFor(() => expect(document.querySelector('[data-provisional]')?.textContent).toContain("Fixing the sort."));
      expect(document.querySelector('[data-provisional]')?.classList.contains('chat-markdown--provisional')).toBe(true);
      expect(document.querySelector('[data-answer-position] .mantine-Loader-root')).not.toBeNull();
      await emit({ type: "REASONING_MESSAGE_END", messageId: "provisional" });await emit({ type: "REASONING_END", messageId: "provisional" });
      await emit({ type: "TOOL_CALL_START", toolCallId: "call", toolCallName: "edit_dashboard", parentMessageId: "step" });
      await emit({ type: "TOOL_CALL_ARGS", toolCallId: "call", delta: "{}" });await emit({ type: "TOOL_CALL_END", toolCallId: "call" });
      expect(document.querySelector('[data-provisional]')).toBeNull();expect(document.body.textContent).toContain("Building dashboard");
      await emit({ type: "TOOL_CALL_RESULT", toolCallId: "call", messageId: "result", content: "{}" });
      expect(document.querySelector('[role="status"]')?.textContent).toContain("Fixing the sort…");
      expect(document.body.textContent).not.toContain("Checking another thing.");
      await emit({ type: "REASONING_START", messageId: "provisional" });await emit({ type: "REASONING_MESSAGE_START", messageId: "provisional", role: "reasoning" });
      await emit({ type: "REASONING_MESSAGE_CONTENT", messageId: "provisional", delta: "Updated " });
      await vi.waitFor(() => expect(document.querySelector('[data-provisional]')?.textContent).toContain("Updated"));
      const position = document.querySelector('[data-answer-position]');
      await emit({ type: "REASONING_MESSAGE_CONTENT", messageId: "provisional", delta: "the latency panel." });
      await emit({ type: "REASONING_MESSAGE_END", messageId: "provisional" });await emit({ type: "REASONING_END", messageId: "provisional" });
      await emit({ type: "TEXT_MESSAGE_START", messageId: "final", role: "assistant" });
      await emit({ type: "TEXT_MESSAGE_CONTENT", messageId: "final", delta: "Updated the latency panel." });
      await emit({ type: "TEXT_MESSAGE_END", messageId: "final" });
      await vi.waitFor(() => expect(document.querySelector('[data-provisional]')).toBeNull());
      expect(document.querySelector('[data-answer-position]')).toBe(position);
      expect(position?.querySelector('.chat-markdown')?.textContent).toBe("Updated the latency panel.");
      await emit({ type: "RUN_FINISHED", threadId: "thread-stream", runId: "run" });await close();
      expect(document.querySelector('[role="log"]')?.textContent).not.toContain("Fixing");
    } finally {await act(async () => root.unmount());}
    const reloaded = await mountStreamChat([{id:"user",role:"user",content:"Sort"},{id:"final",role:"assistant",content:"Updated the latency panel."}], false);
    try {expect(document.querySelector('[role="log"]')?.textContent).toContain("Updated the latency panel.");expect(document.querySelector('[data-provisional]')).toBeNull();expect(document.body.textContent).not.toContain("Fixing");}finally{await act(async()=>reloaded.root.unmount());}
  });
  it("renders the same markdown tree and meta height when provisional text becomes final", async () => {
    const {root,emit,close}=await mountStreamChat();
    const text="## Health\n\n**Healthy** services:\n\n- Cart\n- Checkout\n\n| Service | Errors |\n| --- | --- |\n| Cart | 0 |\n\n```sql\nSELECT 1\n```";
    try {
      await emit({type:"RUN_STARTED",threadId:"thread-stream",runId:"run"});
      await reasoning(emit,text);
      const position=document.querySelector('[data-answer-position]')!;
      const markdown=position.querySelector('.chat-markdown');
      expect(markdown).not.toBeNull();
      expect(markdown?.classList.contains('chat-markdown--provisional')).toBe(true);
      const tree=markdown?.innerHTML;
      const metaHeight=position.querySelector<HTMLElement>('.chat-message-meta')?.style.height;
      expect(metaHeight).toBe('calc(1.5rem * var(--mantine-scale))');
      await emit({type:"REASONING_MESSAGE_END",messageId:"provisional"});await emit({type:"REASONING_END",messageId:"provisional"});
      await emit({type:"TEXT_MESSAGE_START",messageId:"final",role:"assistant"});
      await emit({type:"TEXT_MESSAGE_CONTENT",messageId:"final",delta:text});
      await emit({type:"TEXT_MESSAGE_END",messageId:"final"});
      expect(position.querySelector('.chat-markdown')).toBe(markdown);
      expect(markdown?.classList.contains('chat-markdown--provisional')).toBe(false);
      // happy-dom has no layout engine: identical markdown DOM + a fixed meta
      // slot is the layout proxy, rather than a vacuous zero-height assertion.
      expect(markdown?.innerHTML).toBe(tree);
      expect(position.querySelector<HTMLElement>('.chat-message-meta')?.style.height).toBe(metaHeight);
      await emit({type:"RUN_FINISHED",threadId:"thread-stream",runId:"run"});await close();
    } finally {await act(async()=>root.unmount());}
  });
  it("keeps native reasoning out of the next posted transcript without console warnings",async()=>{
    const {root,emit,close,begin,requests}=await mountStreamChat();
    const warn=vi.spyOn(console,"warn").mockImplementation(()=>undefined);
    try {
      await emit({type:"RUN_STARTED",threadId:"thread-stream",runId:"run"});await reasoning(emit,"Private provisional text");
      await emit({type:"REASONING_MESSAGE_END",messageId:"provisional"});await emit({type:"REASONING_END",messageId:"provisional"});
      await emit({type:"RUN_FINISHED",threadId:"thread-stream",runId:"run"});await close();
      await vi.waitFor(()=>expect(document.querySelector('button[aria-label="Stop"]')).toBeNull());
      await begin();
      expect(requests[1].messages.some(message=>message.role==='reasoning')).toBe(false);
      expect(JSON.stringify(requests[1])).not.toContain("Private provisional text");
      expect(warn).not.toHaveBeenCalled();
      await emit({type:"RUN_STARTED",threadId:"thread-stream",runId:"next"});await emit({type:"RUN_FINISHED",threadId:"thread-stream",runId:"next"});await close();
    } finally {warn.mockRestore();await act(async()=>root.unmount());}
  });
  it.each(["empty","error","cancel"])("clears provisional state before a tool-first next run after %s",async outcome=>{
    const {root,emit,close,begin}=await mountStreamChat();
    const log=vi.spyOn(console,"error").mockImplementation(()=>undefined);
    const warn=vi.spyOn(console,"warn").mockImplementation(()=>undefined);
    try {
      await emit({type:"RUN_STARTED",threadId:"thread-stream",runId:"run"});await reasoning(emit,"Stale narration without punctuation");
      if(outcome==='cancel')await act(async()=>(document.querySelector('button[aria-label="Stop"]') as HTMLButtonElement).click());
      else {await emit(outcome==='error'?{type:"RUN_ERROR",code:"provider_unavailable",message:"private"}:{type:"RUN_FINISHED",threadId:"thread-stream",runId:"run"});await close();}
      await vi.waitFor(()=>expect(document.querySelector('button[aria-label="Stop"]')).toBeNull());
      await begin();
      expect(document.querySelector('[data-provisional]')).toBeNull();
      await emit({type:"RUN_STARTED",threadId:"thread-stream",runId:"next"});
      await emit({type:"TOOL_CALL_START",toolCallId:"call",toolCallName:"edit_dashboard",parentMessageId:"step"});await emit({type:"TOOL_CALL_ARGS",toolCallId:"call",delta:"{}"});await emit({type:"TOOL_CALL_END",toolCallId:"call"});
      await emit({type:"TOOL_CALL_RESULT",toolCallId:"call",messageId:"result",content:"{}"});
      expect(document.querySelector('[role="status"]')?.textContent).toContain("Analyzing your system");
      expect(document.body.textContent).not.toContain("Stale narration");
      await emit({type:"RUN_FINISHED",threadId:"thread-stream",runId:"next"});await close();
    } finally {warn.mockRestore();log.mockRestore();await act(async()=>root.unmount());}
  });
  it.each([
    ['provider_unavailable','Fanout could not reach the model provider. Please try again.'],
    ['step_limit','Fanout reached its step limit before finishing. Try a narrower question.'],
    ['time_limit','Fanout reached its 5-minute time limit. Try a smaller request.'],
    ['run_failed','Fanout could not complete this analysis. Please try again.'],
    ['unknown_secret_code','Fanout could not complete this analysis. Please try again.'],
  ])("maps %s to a plain error and shows the same outcome on reload", async (code, message) => {
    const {root,emit,close}=await mountStreamChat();
    try {await emit({type:'RUN_STARTED',threadId:'thread-stream',runId:'run'});await emit({type:'RUN_ERROR',code,message:'raw_server_code'});await close();await vi.waitFor(()=>expect(document.querySelector('[role="alert"]')?.textContent).toContain(message));expect(document.body.textContent).not.toContain('raw_server_code');expect(document.body.textContent).not.toContain(code);}finally{await act(async()=>root.unmount());}
    const loaded=await mountStreamChat([{id:'user',role:'user',content:'Show'},{id:'run-outcome',role:'activity',activityType:'agent-outcome',content:{status:'failed',message}}],false);
    try {expect(document.querySelector('[role="alert"]')?.textContent).toContain(message);}finally{await act(async()=>loaded.root.unmount());}
  });
});

function value(overrides: Partial<FanoutAppContextValue> = {}): FanoutAppContextValue {
  return {
    agentAvailable: true, threadID: "thread-1", threadMissing: false, messages: [], messageTimes: {}, ready: true, running: false, activity: "", provisional: null, stopped: false,
    input: "", setInput: vi.fn(), error: "", inputRef: createRef<HTMLTextAreaElement>(),
    send: vi.fn(async () => undefined), submit: vi.fn((event: FormEvent) => event.preventDefault()), stop: vi.fn(), retry: vi.fn(), reloadThread: vi.fn(),
    openChat: vi.fn(), newThread: vi.fn(), selectThread: vi.fn(),
    ...overrides,
  };
}

async function mount(context: FanoutAppContextValue) {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  await act(async () => root.render(<MantineProvider><FanoutAppContext.Provider value={context}><ChatPage /></FanoutAppContext.Provider></MantineProvider>));
  return root;
}

function button(text: string) {
  return Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((item) => item.textContent?.trim() === text);
}
it("groups recovered tool failures quietly and expands escaped names/messages",async()=>{
 const messages=[{id:"a",role:"assistant",content:"",toolCalls:[{id:"c1",type:"function",function:{name:"query_telemetry",arguments:"{}"}},{id:"c2",type:"function",function:{name:"edit_dashboard",arguments:"{}"}}]},
 {id:"e1",role:"tool",toolCallId:"c1",content:"<img src=x onerror=alert(1)>",error:"tool error"},{id:"e2",role:"tool",toolCallId:"c2",content:"Invalid panel",error:"tool error"},{id:"done",role:"assistant",content:"Recovered answer"}] as Message[];
 const root=await mount(value({messages}));try {
 expect(document.querySelector('[role="alert"]')).toBeNull();const toggle=button("2 tool calls failed")!;expect(toggle).toBeTruthy();expect(toggle.getAttribute("aria-expanded")).toBe("false");expect(document.body.textContent).not.toContain("Invalid panel");
 await act(async()=>toggle.click());expect(document.body.textContent).toContain("query_telemetry");expect(document.body.textContent).toContain("edit_dashboard");expect(document.body.textContent).toContain("<img src=x onerror=alert(1)>");expect(document.querySelector("img")).toBeNull();
 }finally{await act(async()=>root.unmount());}
});
it("keeps a single recovered failure quiet and preserves the run-error alert",async()=>{
 const messages=[{id:"t",role:"tool",toolCallId:"c",content:"Temporary failure",error:"tool error"},{id:"answer",role:"assistant",content:"Answer"}] as Message[];
 for(const error of ["","Provider unavailable"]) {const root=await mount(value({messages,error}));try {expect(button("1 tool call failed")).toBeTruthy();expect(document.querySelectorAll('[role="alert"]')).toHaveLength(error?1:0);if(error)expect(document.querySelector('[role="alert"]')?.textContent).toContain(error);}finally{await act(async()=>root.unmount());}}
});
it("uses server view keys and kinds even when tool names and specifications differ",async()=>{
 const first=appMessage("one","renamed_preset","Requested"),second=appMessage("two","get_service_performance","Discovery");
 (first.content as any).tool_result.view={kind:"preset",key:"a".repeat(64)};(second.content as any).tool_result.view={kind:"query",key:"a".repeat(64)};
 const root=await mount(value({messages:[first,second]}));try {expect(document.querySelectorAll('[data-chat-app]')).toHaveLength(1);expect(document.querySelector('button[aria-expanded="true"]')?.textContent).toContain("Discovery");}finally{await act(async()=>root.unmount());}
});

function appMessage(id: string, tool: string, title: string, rows = 1): Message {
  const fragment = fixture(); fragment.dashboard.panels[0].title = title;
  fragment.view = {kind:tool === "query_telemetry" ? "query" : "preset", key:(id === "one" ? "1" : "2").repeat(64)};
  fragment.results[0] = { id: "p", status: "ok", elapsed_ms: 0, frame: { columns: [{ name: "body", type: "string", role: "dimension" }], values: [Array.from({ length: rows }, () => title)], rows } };
  fragment.dashboard.time = { from: `2026-10-07T${id === "one" ? "18" : "19"}:00:00Z`, to: "2026-10-07T20:00:00Z", refresh: "off" };
  return { id, role: "activity", activityType: "mcp-app", content: { resource_uri: "ui://fanout/panels.html", tool_name: tool, tool_input: {}, tool_result: fragment, is_error: false } } as Message;
}
it("aligns collapsed and expanded summaries with a leading chevron and singular/plural row counts", async () => {
  const root = await mount(value({ messages: [appMessage("one", "query_telemetry", "Discovery"), appMessage("two", "query_telemetry", "Answer", 2)] }));
  try {
    const toggle = document.querySelector<HTMLButtonElement>('button[aria-expanded="false"]')!;
    expect(toggle.classList.contains("chat-app-toggle")).toBe(true);
    expect(toggle.firstElementChild?.classList.contains("chat-app-chevron")).toBe(true);
    expect(toggle.lastElementChild?.classList.contains("chat-app-summary")).toBe(true);
    expect(toggle.textContent).toContain("Discovery · logs · 1 row"); expect(toggle.textContent).not.toContain("1 rows");
    expect(document.querySelector('button[aria-expanded="true"]')?.textContent).toContain("2 rows");
  } finally { await act(async () => root.unmount()); }
});
it("summarizes multi-panel views with panel counts and only non-ok status counts",async()=>{
  const fragment=fixture();const base=fragment.dashboard.panels[0];
  fragment.dashboard.panels=Array.from({length:4},(_,i)=>({...base,id:`p${i}`,title:"Performance"}));
  fragment.results=fragment.dashboard.panels.map((p,i)=>({id:p.id,status:i===3?"empty":"ok",elapsed_ms:0}));
  const message={id:"one",role:"activity",activityType:"mcp-app",content:{resource_uri:"ui://fanout/panels.html",tool_name:"get_service_performance",tool_input:{},tool_result:fragment,is_error:false}} as Message;
  for(const status of ["empty","error","ok"] as const) {
    fragment.results[3].status=status;
    const root=await mount(value({messages:[JSON.parse(JSON.stringify(message))]}));
    try {const summary=document.querySelector('button[aria-expanded]')!.textContent!;expect(summary).toContain(status==="ok"?"4 panels":"4 panels · 1 "+status);expect(summary).not.toContain("ok,");expect(summary).not.toContain("rows");}
    finally {await act(async()=>root.unmount());}
  }
});
it.each(["mixed", "presets", "custom"])("expands requested presets over discovery views and restores %s turns on reload", async kind => {
  const messages = [appMessage("one", kind === "custom" ? "query_telemetry" : "get_service_performance", "Requested"), appMessage("two", kind === "presets" ? "search_logs" : "query_telemetry", "Later")];
  for (const saved of [messages, JSON.parse(JSON.stringify(messages))]) {
    const root = await mount(value({ messages: saved }));
    try {
      const toggles = [...document.querySelectorAll('button[aria-expanded]')];
      expect(toggles.map(button => button.getAttribute("aria-expanded"))).toEqual(kind === "mixed" ? ["true", "false"] : kind === "presets" ? ["true", "true"] : ["false", "true"]);
    } finally { await act(async () => root.unmount()); }
  }
});
it("renders the latest server view snapshot without a host preset-winner rule", async () => {
  const preset = appMessage("one", "get_service_topology", "Requested");
  const custom = structuredClone(preset) as Message & { content: Record<string, unknown> }; custom.id = "two"; custom.content.tool_name = "query_telemetry"; (custom.content.tool_result as ReturnType<typeof fixture>).view.kind="query";
  const root = await mount(value({ messages: [preset, custom, appMessage("three", "query_telemetry", "Discovery")] }));
  try {
    const toggles = [...document.querySelectorAll('button[aria-expanded]')];
    expect(toggles.map(toggle => toggle.getAttribute("aria-expanded"))).toEqual(["false", "true"]);
  } finally { await act(async () => root.unmount()); }
});

it("collapses earlier views, dedupes within a turn, expands accessibly and restores on reload", async () => {
  const app = (id: string, title: string, changed = false): Message => {
    const f = fixture(); f.dashboard.name = title; f.dashboard.panels[0].title = title;
    f.view.key=(changed?"1":"0").repeat(64);
    if (changed) f.dashboard.time = { from: "2026-10-07T18:45:00Z", to: "2026-10-07T19:45:00Z", refresh: "off" };
    return { id, role: "activity", activityType: "mcp-app", content: { resource_uri: "ui://fanout/panels.html", tool_name: "query_telemetry", tool_input: {}, tool_result: f, is_error: false } } as Message;
  };
  const messages = [{ id: "u", role: "user", content: "Show" } as Message, app("first", "Discovery"), app("duplicate", "Discovery"), app("last", "Answer", true)];
  let root = await mount(value({ messages }));
  await vi.waitFor(() => expect(document.querySelectorAll("[data-app-frame]")).toHaveLength(1));
  const summary = document.querySelector<HTMLButtonElement>('button[aria-expanded="false"]')!;
  expect(summary.textContent).toContain("Discovery · logs · empty");
  expect(document.querySelectorAll("[data-chat-app]")).toHaveLength(2);
  await act(async () => summary.click());
  expect(summary.getAttribute("aria-expanded")).toBe("true");
  await vi.waitFor(() => expect(document.querySelectorAll("[data-app-frame]")).toHaveLength(2));
  await act(async () => root.unmount());
  root = await mount(value({ messages: JSON.parse(JSON.stringify(messages)) }));
  await vi.waitFor(() => expect(document.querySelectorAll("[data-app-frame]")).toHaveLength(1));
  expect(document.querySelectorAll("[data-chat-app]")).toHaveLength(2);
  await act(async () => root.unmount());
});

it("shows app tool errors and old string activities without throwing", async () => {
  const messages = [{ id: "error", role: "tool", toolCallId: "call", content: "Invalid telemetry window", error: "Invalid telemetry window" }, { id: "old", role: "activity", activityType: "mcp-app", content: { resource_uri: "ui://fanout/panels.html", tool_name: "query_telemetry", tool_input: {}, tool_result: "old response", is_error: false } }] as Message[];
  const root = await mount(value({ messages }));
  await act(async()=>button("1 tool call failed")!.click());
  expect(document.body.textContent).toContain("Invalid telemetry window");
  expect(document.body.textContent).toContain("This view could not be loaded. Please try again.");
  await act(async () => root.unmount());
});

it("keeps distinct captured windows and variables, and resets dedupe at the next user turn", async () => {
  const app = (id: string, from: number, vars?: Record<string, string>): Message => {
    const fragment = fixture(); fragment.results[0].from_ms = from; fragment.vars = vars; fragment.view.key=(vars?"3":String(from)).repeat(64);
    return { id, role: "activity", activityType: "mcp-app", content: { resource_uri: "ui://fanout/panels.html", tool_name: "query_telemetry", tool_input: {}, tool_result: fragment, is_error: false } } as Message;
  };
  const root = await mount(value({ messages: [{ id: "u1", role: "user", content: "Show" } as Message, app("one", 0), app("two", 1), app("three", 0, { service: "checkout" }), { id: "u2", role: "user", content: "Again" } as Message, app("four", 0)] }));
  try {
    expect(document.querySelectorAll("[data-chat-app]")).toHaveLength(4);
    await vi.waitFor(() => expect(document.querySelectorAll("[data-app-frame]")).toHaveLength(2));
  } finally { await act(async () => root.unmount()); }
});

it("dedupes a complete custom map and topology preset with different row limits", async () => {
  const map = fixture("service_map"); map.dashboard.panels[0].query = { from: "spans", limit: 20 };
  map.results[0].frame = { columns: [{ name: "service", type: "string", role: "dimension" }], values: [["checkout"]], rows: 1 };
  const preset = structuredClone(map); preset.dashboard.name = "Telemetry"; preset.dashboard.panels[0].id = "services"; preset.dashboard.panels[0].title = "Service dependencies"; preset.dashboard.panels[0].query!.limit = 400; preset.results[0].id = "services"; preset.view.kind="preset";
  const messages = [map, preset].map((fragment, i) => ({ id: String(i), role: "activity", activityType: "mcp-app", content: { resource_uri: "ui://fanout/panels.html", tool_name: i ? "get_service_topology" : "query_telemetry", tool_input: {}, tool_result: fragment, is_error: false } } as Message));
  const root = await mount(value({ messages }));
  try { expect(document.querySelectorAll("[data-chat-app]")).toHaveLength(1); }
  finally { await act(async () => root.unmount()); }
});

describe("ChatPage", () => {
  afterEach(() => { document.body.innerHTML = ""; });

  it("offers suggestions on an empty draft and sends the chosen one", async () => {
    const context = value();
    const root = await mount(context);
    expect(document.body.textContent).toContain("What do you want to know about your system?");
    expect(document.body.textContent).not.toContain("See what changed");
    const chip = button("Find the source of elevated errors");
    expect(chip).not.toBeUndefined();
    await act(async () => chip?.click());
    expect(context.send).toHaveBeenCalledWith("Find the source of elevated errors");
    await act(async () => root.unmount());
  });

  it("renders markdown tables and code blocks with product chrome", async () => {
    const messages: Message[] = [
      { id: "u1", role: "user", content: "Show the slowest endpoints" } as Message,
      { id: "a1", role: "assistant", content: "| Endpoint | P95 |\n|---|---|\n| GET /orders | 650ms |\n\n```sql\nSELECT 1\n```" } as Message,
    ];
    const root = await mount(value({ messages, messageTimes: { u1: Date.UTC(2026, 8, 5, 18, 16) } }));
    const table = document.querySelector(".chat-markdown table");
    expect(table).not.toBeNull();
    expect(table?.closest(".chat-table")).not.toBeNull();
    expect(document.querySelector(".chat-markdown pre code")?.textContent).toContain("SELECT 1");
    expect(document.querySelector('button[aria-label="Copy code"]')).not.toBeNull();
    expect(document.querySelector('button[aria-label="Copy message"]')).not.toBeNull();
    expect(document.querySelector(".mantine-Avatar-root")).toBeNull();
    await act(async () => root.unmount());
  });

  it("shows the activity line and a stop button while running", async () => {
    const context = value({ running: true, activity: "Checking system health…", messages: [{ id: "u1", role: "user", content: "hi" } as Message] });
    const root = await mount(context);
    expect(document.body.textContent).toContain("Checking system health…");
    const stop = document.querySelector('button[aria-label="Stop"]') as HTMLButtonElement;
    expect(stop).not.toBeNull();
    await act(async () => stop.click());
    expect(context.stop).toHaveBeenCalled();
    await act(async () => root.unmount());
  });

  it("offers retry after a failed run and a new chat for a missing thread", async () => {
    const failed = value({ error: "Fanout could not complete this analysis.", messages: [{ id: "u1", role: "user", content: "hi" } as Message] });
    let root = await mount(failed);
    await act(async () => button("Retry")?.click());
    expect(failed.retry).toHaveBeenCalled();
    await act(async () => root.unmount());
    document.body.innerHTML = "";

    const missing = value({ threadMissing: true });
    root = await mount(missing);
    expect(document.body.textContent).toContain("This chat no longer exists");
    await act(async () => button("New chat")?.click());
    expect(missing.newThread).toHaveBeenCalled();
    await act(async () => root.unmount());
  });

  // A thread whose load failed never becomes ready, so the loader would spin
  // forever; the pane has to state the failure and offer a way out instead.
  it("explains a thread that could not be restored instead of loading forever", async () => {
    const context = value({ ready: false, error: "This chat could not be restored. Start a new chat or try again." });
    const root = await mount(context);
    expect(document.body.textContent).toContain("This chat could not be restored");
    expect(document.body.textContent).not.toContain("Loading chat");
    // Nothing ran, so Retry has to load the thread again rather than replay
    // a run that never started.
    await act(async () => button("Retry")?.click());
    expect(context.reloadThread).toHaveBeenCalled();
    expect(context.retry).not.toHaveBeenCalled();
    await act(async () => button("New chat")?.click());
    expect(context.newThread).toHaveBeenCalled();
    await act(async () => root.unmount());
  });

  it("marks the copy button copied when the clipboard accepts", async () => {
    const writeText = stubClipboard(async () => undefined);
    const root = await mount(value({ messages: [{ id: "a1", role: "assistant", content: "All clear." } as Message] }));
    await clickCopy();
    expect(writeText).toHaveBeenCalledWith("All clear.");
    expect(copyButton()?.getAttribute("data-state")).toBe("copied");
    await act(async () => root.unmount());
  });

  // A denied permission or an insecure context rejects the write. That must
  // read as a failure on the button, not as an unhandled rejection.
  it("reports a refused clipboard copy instead of rejecting", async () => {
    const writeText = stubClipboard(async () => { throw new Error("write permission denied"); });
    const unhandled: unknown[] = [];
    const capture = (reason: unknown) => { unhandled.push(reason); };
    node.process.on("unhandledRejection", capture);
    try {
      const root = await mount(value({ messages: [{ id: "a1", role: "assistant", content: "All clear." } as Message] }));
      expect(copyButton()).not.toBeNull();
      await clickCopy();
      expect(writeText).toHaveBeenCalledWith("All clear.");
      expect(unhandled).toEqual([]);
      expect(copyButton()?.getAttribute("data-state")).toBe("failed");
      await act(async () => root.unmount());
    } finally {
      node.process.off("unhandledRejection", capture);
    }
  });

  // A second click inside the reset window used to be cut short by the first
  // click's own timer, flipping the button back to idle mid-window.
  it("keeps the copied state through a second click inside the reset window", async () => {
    const writeText = stubClipboard(async () => undefined);
    vi.useFakeTimers({ toFake: ["setTimeout", "clearTimeout"] });
    try {
      const root = await mount(value({ messages: [{ id: "a1", role: "assistant", content: "All clear." } as Message] }));
      await act(async () => {
        copyButton()?.click();
        await vi.advanceTimersByTimeAsync(0);
      });
      expect(copyButton()?.getAttribute("data-state")).toBe("copied");
      await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
      expect(copyButton()?.getAttribute("data-state")).toBe("copied");
      await act(async () => {
        copyButton()?.click();
        await vi.advanceTimersByTimeAsync(0);
      });
      expect(copyButton()?.getAttribute("data-state")).toBe("copied");
      await act(async () => { await vi.advanceTimersByTimeAsync(1000); });
      expect(copyButton()?.getAttribute("data-state")).toBe("copied");
      expect(writeText).toHaveBeenCalledTimes(2);
      await act(async () => root.unmount());
    } finally {
      vi.useRealTimers();
    }
  });
});

// The suite runs on Node but does not depend on @types/node, so the rejection
// hooks are reached through globalThis rather than through a type dependency.
const node = globalThis as unknown as { process: { on(event: string, listener: (reason: unknown) => void): void; off(event: string, listener: (reason: unknown) => void): void } };

function copyButton() {
  return document.querySelector<HTMLButtonElement>('button[aria-label="Copy message"]');
}

function stubClipboard(writeText: (text: string) => Promise<void>) {
  const spy = vi.fn(writeText);
  Object.defineProperty(navigator, "clipboard", { value: { writeText: spy }, configurable: true });
  return spy;
}

// One act scope covers the click and the clipboard promise it starts, so the
// state the promise sets lands inside it — and a rejection nobody handled has
// drained the microtask queue by the time the timer fires.
async function clickCopy() {
  await act(async () => {
    copyButton()?.click();
    await new Promise((resolve) => setTimeout(resolve, 0));
  });
}

it.each([null,{}, {resourceUri:"ui://fanout/panels.html",toolName:"query_telemetry"}, {resource_uri:"ui://fanout/trace-detail.html",tool_name:"inspect_trace",tool_input:{},tool_result:{},is_error:false}])("shows an Alert for incompatible persisted mcp-app content (%s)",async content=>{
 const root=await mount(value({messages:[{id:"activity",role:"activity",activityType:"mcp-app",content} as unknown as Message]}));
 expect(document.body.textContent).toContain("This view could not be loaded. Please try again.");
 expect(document.querySelector("iframe")).toBeNull();await act(async()=>root.unmount());document.body.innerHTML="";
});

it("groups failed calls by assistant message within one user turn",async()=>{
 const messages=[{id:"u",role:"user",content:"Help"},
 {id:"a1",role:"assistant",content:"",toolCalls:[{id:"c1",type:"function",function:{name:"query_telemetry",arguments:"{}"}}]},
 {id:"a2",role:"assistant",content:"",toolCalls:[{id:"c2",type:"function",function:{name:"search_logs",arguments:"{}"}},{id:"c3",type:"function",function:{name:"search_logs",arguments:"{}"}}]},
 {id:"f1",role:"tool",toolCallId:"c1",content:"first failure",error:"error"},
 {id:"f2",role:"tool",toolCallId:"c2",content:"second failure",error:"error"},
 {id:"f3",role:"tool",toolCallId:"c3",content:"third failure",error:"error"}] as Message[];
 const root=await mount(value({messages}));try {
 expect(button("1 tool call failed")).toBeTruthy();expect(button("2 tool calls failed")).toBeTruthy();
 await act(async()=>button("1 tool call failed")!.click());expect(document.body.textContent).toContain("first failure");expect(document.body.textContent).not.toContain("second failure");
 await act(async()=>button("2 tool calls failed")!.click());expect(document.body.textContent).toContain("second failure");expect(document.body.textContent).toContain("third failure");
 }finally{await act(async()=>root.unmount());}
});

it("reconstructs one receipt per build turn with no final card or narrated tool inventory",async()=>{
 const messages=receiptBuild(); const assistant=messages.find(m=>m.role==="assistant")!;
 if(assistant.role==="assistant")assistant.content="Stray intermediate paragraph with panel inventory";
 messages.push({id:"final-receipt",role:"assistant",content:"Latency now uses the requested threshold."});
 const {root}=await mountStreamChat(JSON.parse(JSON.stringify(messages)),false);
 try{expect(document.querySelectorAll("[data-build-receipt]")).toHaveLength(1);expect(document.querySelectorAll("[data-dashboard-result]")).toHaveLength(1);expect(document.querySelectorAll('[data-build-receipt] [role="status"]')).toHaveLength(1);expect(document.body.textContent).not.toContain("Stray intermediate paragraph");expect(document.body.textContent).toContain("Latency now uses the requested threshold.");}
 finally{await act(async()=>root.unmount());}
});

it.each([false,true])("renders no empty transcript slots after a finished run (receipt=%s)",async withReceipt=>{
 const messages:Message[]=withReceipt?receiptBuild():[{id:"user",role:"user",content:"Explain"},{id:"tool-step",role:"assistant",content:"",toolCalls:[{id:"read",type:"function",function:{name:"get_dashboard",arguments:"{}"}}]},{id:"read-result",role:"tool",toolCallId:"read",content:"{}"}];
 messages.push({id:"empty-final",role:"assistant",content:""},{id:"ignored-activity",role:"activity",activityType:"model_call_usage",content:{}},{id:"actual-final",role:"assistant",content:"Final answer."});
 const {root}=await mountStreamChat(messages as Message[],false);
 try{const answer=document.querySelector('[data-answer-position]')!;expect(answer.textContent).toContain('Final answer.');
 const siblings=[...answer.parentElement!.children];expect(siblings.filter(el=>!el.textContent?.trim()&&!el.querySelector('iframe'))).toHaveLength(0);
 expect(document.querySelectorAll('.chat-message-meta').length).toBe(1); // final answer copy only; unstamped user has no meta slot
 }finally{await act(async()=>root.unmount());vi.unstubAllGlobals();document.body.innerHTML='';}
});
it('labels in-flight stages as progress and reserves attention for ended runs',async()=>{
 const {root,emit,close}=await mountStreamChat();
 try{await emit({type:'RUN_STARTED',threadId:'thread-stream',runId:'run'});await emit({type:'TOOL_CALL_START',toolCallId:'pending',toolCallName:'create_dashboard',parentMessageId:'step'});await emit({type:'TOOL_CALL_ARGS',toolCallId:'pending',delta:'{}'});await emit({type:'TOOL_CALL_END',toolCallId:'pending'});
 expect(document.querySelector('[data-receipt-attention]')).toBeNull();expect(document.querySelector('[data-build-running]')).not.toBeNull();
 await emit({type:'RUN_FINISHED',threadId:'thread-stream',runId:'run'});await close();await vi.waitFor(()=>expect(document.querySelector('[data-build-running]')).toBeNull());expect(document.querySelector('[data-receipt-attention]')?.textContent).toContain('save: incomplete');
 }finally{await act(async()=>root.unmount());vi.unstubAllGlobals();document.body.innerHTML='';}
});
it('renders nothing for an empty final-answer slot after the run',async()=>{
 const {root}=await mountStreamChat([{id:'user',role:'user',content:'Explain'},{id:'empty',role:'assistant',content:''}],false);
 try{expect(document.querySelector('[data-answer-position]')).toBeNull();expect(document.querySelector('.chat-message-meta')).toBeNull();}
 finally{await act(async()=>root.unmount());vi.unstubAllGlobals();document.body.innerHTML='';}
});

it.each(['create_dashboard','edit_dashboard','restore_dashboard_version'])('keeps an interrupted %s out of chat error groups',async name=>{
 const messages=[user(),call('save',name),result('save',{error:'interrupted'},'interrupted')];
 const {root}=await mountStreamChat(messages,false);
 try{expect(document.body.textContent).toContain('Save interrupted · outcome unknown');expect(document.body.textContent).not.toMatch(/tool calls? failed|Save failed|not saved/);}
 finally{await act(async()=>root.unmount());vi.unstubAllGlobals();document.body.innerHTML='';}
});

it.each(['live','reloaded'])('renders the committed restore receipt in %s chat',async mode=>{
 const messages=[user(),call('restore','restore_dashboard_version',{id:'board',version:1}),result('restore',receiptFixture)];
 const {root,emit,close}=await mountStreamChat(mode==='reloaded'?JSON.parse(JSON.stringify(messages)):[],mode==='live');
 try{
  if(mode==='live') {
   await emit({type:'RUN_STARTED',threadId:'thread-stream',runId:'run'});
   await emit({type:'TOOL_CALL_START',toolCallId:'restore',toolCallName:'restore_dashboard_version',parentMessageId:'restore-step'});
   await emit({type:'TOOL_CALL_ARGS',toolCallId:'restore',delta:'{"id":"board","version":1}'});
   await emit({type:'TOOL_CALL_END',toolCallId:'restore'});
   await emit({type:'TOOL_CALL_RESULT',toolCallId:'restore',messageId:'restored',content:JSON.stringify(receiptFixture)});
   await emit({type:'RUN_FINISHED',threadId:'thread-stream',runId:'run'});
   await close();
  }
  expect(document.querySelectorAll('[data-build-receipt]')).toHaveLength(1);
  expect(document.body.textContent).toContain('Saved v2');
  expect(document.body.textContent).not.toContain('restore_dashboard_version');
  await act(async()=>button('Details')!.click());
  expect(document.body.textContent).toContain('Restored');
 }finally{await act(async()=>root.unmount());vi.unstubAllGlobals();document.body.innerHTML='';}
});
it('replaces interrupted chat uncertainty with a later proven save',async()=>{
 const messages=[user(),call('save','edit_dashboard'),result('save',{error:'interrupted'},'interrupted'),{...result('save',receiptFixture),id:'proven-result'}];
 const {root}=await mountStreamChat(messages,false);
 try{expect(document.body.textContent).toContain('Saved v2');expect(document.body.textContent).not.toMatch(/interrupted|outcome unknown|tool calls? failed/);}
 finally{await act(async()=>root.unmount());vi.unstubAllGlobals();document.body.innerHTML='';}
});
