import { MantineProvider } from "@mantine/core";
import type { Message } from "@ag-ui/client";
import { act, createRef, type FormEvent } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FanoutAppContext, type FanoutAppContextValue } from "./app-context";
import { ChatPage } from "./chat";
import { fixture } from "../tests/fixtures";
vi.mock("./mcp-app-frame", () => ({ default: () => <div data-app-frame>Panel frame</div> }));

function value(overrides: Partial<FanoutAppContextValue> = {}): FanoutAppContextValue {
  return {
    agentAvailable: true, threadID: "thread-1", threadMissing: false, messages: [], messageTimes: {}, ready: true, running: false, activity: "",
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

function appMessage(id: string, tool: string, title: string, rows = 1): Message {
  const fragment = fixture(); fragment.dashboard.panels[0].title = title;
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
it("prefers a deduped preset over an equivalent later custom view", async () => {
  const preset = appMessage("one", "get_service_topology", "Requested");
  const custom = structuredClone(preset) as Message & { content: Record<string, unknown> }; custom.id = "two"; custom.content.tool_name = "query_telemetry";
  const root = await mount(value({ messages: [preset, custom, appMessage("three", "query_telemetry", "Discovery")] }));
  try {
    const toggles = [...document.querySelectorAll('button[aria-expanded]')];
    expect(toggles.map(toggle => toggle.getAttribute("aria-expanded"))).toEqual(["true", "false"]);
  } finally { await act(async () => root.unmount()); }
});

it("collapses earlier views, dedupes within a turn, expands accessibly and restores on reload", async () => {
  const app = (id: string, title: string, changed = false): Message => {
    const f = fixture(); f.dashboard.name = title; f.dashboard.panels[0].title = title;
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
  expect(document.body.textContent).toContain("Invalid telemetry window");
  expect(document.body.textContent).toContain("This view could not be loaded. Please try again.");
  await act(async () => root.unmount());
});

it("keeps distinct captured windows and variables, and resets dedupe at the next user turn", async () => {
  const app = (id: string, from: number, vars?: Record<string, string>): Message => {
    const fragment = fixture(); fragment.results[0].from_ms = from; fragment.vars = vars;
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
  const preset = structuredClone(map); preset.dashboard.name = "Telemetry"; preset.dashboard.panels[0].id = "services"; preset.dashboard.panels[0].title = "Service dependencies"; preset.dashboard.panels[0].query!.limit = 400; preset.results[0].id = "services";
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
