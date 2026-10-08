import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, createRef } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import Rail, { type RailHandle } from "./rail";

const fetchMock = vi.fn<typeof fetch>();

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function threadsPage(query = "") {
  return json({
    threads: [{ threadId: "thread-checkout", title: query ? `Result for ${query}` : "Checkout latency", updatedAt: "2026-07-22 03:00:00" }],
    nextCursor: "",
  });
}

function dashboards() {
  return json({ dashboards: [
    { id: "dash-main", name: "System overview", description: "", is_default: true, version: 1, panel_count: 4, updated_at: "2026-07-22 03:00:00" },
    { id: "dash-checkout", name: "Checkout", description: "", is_default: false, version: 1, panel_count: 2, updated_at: "2026-07-22 03:00:00" },
  ] });
}

function respond(input: RequestInfo | URL, init?: RequestInit) {
  const url = new URL(String(input), "http://localhost");
  if (url.pathname === "/api/dashboards") return dashboards();
  if (url.pathname === "/api/telemetry/schema") {
    return json({window:"1h",signals:{},measures:[],units:[],services:[{value:"checkout",count:10},{value:"payments",count:8}]});
  }
  if (url.pathname === "/api/agent/threads") return threadsPage(url.searchParams.get("q") ?? "");
  if (url.pathname.startsWith("/api/agent/threads/") && init?.method === "PATCH") return json({ title: "Checkout follow-up" });
  if (url.pathname.startsWith("/api/agent/threads/") && init?.method === "DELETE") return new Response(null, { status: 204 });
  throw new Error(`unexpected request: ${url.pathname}`);
}

function mount(props: Partial<Parameters<typeof Rail>[0]> = {}) {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const handlers = {
    onNewChat: vi.fn(), onSelectThread: vi.fn(), onDeletedThread: vi.fn(), onSelectDashboard: vi.fn(), onCreateDashboard: vi.fn(), onInvestigateService: vi.fn(),
  };
  const ref = createRef<RailHandle>();
  const render = () => root.render(
    <QueryClientProvider client={queryClient}>
      <MantineProvider>
        <Rail ref={ref} agentAvailable activeThreadID="thread-checkout" activeDashboardID="dash-main" {...handlers} {...props} />
      </MantineProvider>
    </QueryClientProvider>,
  );
  return { root, handlers, ref, render };
}

function setValue(input: HTMLInputElement, value: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(input, value);
  input.dispatchEvent(new InputEvent("input", { bubbles: true, data: value, inputType: "insertText" }));
  input.dispatchEvent(new Event("change", { bubbles: true }));
}

describe("Rail", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    fetchMock.mockImplementation(async (input, init) => respond(input, init));
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    document.body.innerHTML = "";
  });

  it("lists chats and dashboards, marks the active rows, and selects", async () => {
    const { root, handlers, render } = mount();
    await act(async () => render());
    await vi.waitFor(() => expect(document.body.textContent).toContain("Checkout latency"));
    await vi.waitFor(() => expect(document.body.textContent).toContain("System overview"));
    expect(document.body.textContent).toContain("Chats");
    expect(document.body.textContent).toContain("Dashboards");
    expect(document.body.textContent).not.toContain("Investigation");

    const activeRows = document.querySelectorAll('[aria-current="page"]');
    expect(activeRows).toHaveLength(2);

    const thread = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Checkout latency"));
    await act(async () => thread?.click());
    expect(handlers.onSelectThread).toHaveBeenCalledWith("thread-checkout");

    const dashboard = Array.from(document.querySelectorAll<HTMLAnchorElement>("a")).find((button) => button.textContent?.includes("Checkout") && !button.textContent.includes("latency"));
    await act(async () => dashboard?.click());
    expect(handlers.onSelectDashboard).toHaveBeenCalledWith("dash-checkout");

    const newChat = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.trim() === "New chat");
    await act(async () => newChat?.click());
    expect(handlers.onNewChat).toHaveBeenCalled();
    await act(async () => root.unmount());
  });

  it("lists panel dashboard summaries from the new dashboard API", async () => {
    const { root, render } = mount();
    await act(async () => render());
    await vi.waitFor(() => expect(document.querySelectorAll('a[href^="/dashboards/"]')).toHaveLength(2));
    expect(fetchMock.mock.calls.filter(([input]) => String(input) === "/api/dashboards")).toHaveLength(1);
    expect(document.body.textContent).toContain("System overview");
    expect(document.body.textContent).toContain("Checkout");
    await act(async () => root.unmount());
  });

  it("marks only the default dashboard independently of the active dashboard", async () => {
    const { root, render } = mount({ activeDashboardID: "dash-checkout" });
    await act(async () => render());
    await vi.waitFor(() => expect(document.querySelector('a[href="/dashboards/dash-main"]')).not.toBeNull());
    const defaultRow = document.querySelector('a[href="/dashboards/dash-main"]');
    const activeRow = document.querySelector('a[href="/dashboards/dash-checkout"]');
    expect(defaultRow?.textContent).toContain("Default");
    expect(defaultRow?.getAttribute("aria-current")).toBeNull();
    expect(activeRow?.textContent).not.toContain("Default");
    expect(activeRow?.getAttribute("aria-current")).toBe("page");
    await act(async () => root.unmount());
  });

  it("links each dashboard without search state and preserves modified clicks", async () => {
    const { root, handlers, render } = mount();
    await act(async () => render());
    await vi.waitFor(() => expect(document.querySelectorAll('a[href^="/dashboards/"]')).toHaveLength(2));
    for (const id of ["dash-main", "dash-checkout"]) {
      const link = document.querySelector<HTMLAnchorElement>(`a[href="/dashboards/${id}"]`)!;
      expect(new URL(link.href).search).toBe("");
      await act(async () => link.click());
      expect(handlers.onSelectDashboard).toHaveBeenLastCalledWith(id);
    }
    handlers.onSelectDashboard.mockClear();
    const event = new MouseEvent("click", { bubbles: true, cancelable: true, ctrlKey: true });
    await act(async () => { document.querySelector('a[href="/dashboards/dash-checkout"]')!.dispatchEvent(event); });
    expect(event.defaultPrevented).toBe(false);
    expect(handlers.onSelectDashboard).not.toHaveBeenCalled();
    await act(async () => root.unmount());
  });

  it("offers New dashboard when the dashboard list is empty and the agent is available", async () => {
    fetchMock.mockImplementation(async (input, init) => String(input) === "/api/dashboards" ? json({ dashboards: [] }) : respond(input, init));
    const { root, handlers, render } = mount();
    await act(async () => render());
    await vi.waitFor(() => expect(document.body.textContent).toContain("New dashboard"));
    const create = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((item) => item.textContent?.trim() === "New dashboard");
    await act(async () => create!.click());
    expect(handlers.onCreateDashboard).toHaveBeenCalledOnce();
    expect(document.body.textContent).not.toContain("Create with AI");
    await act(async () => root.unmount());
  });

  it("hides New dashboard for an empty list when the agent is unavailable", async () => {
    fetchMock.mockImplementation(async () => json({ dashboards: [] }));
    const { root, render } = mount({ agentAvailable: false });
    await act(async () => render());
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalledOnce());
    expect(document.body.textContent).not.toContain("New dashboard");
    expect(document.body.textContent).not.toContain("Create with AI");
    await act(async () => root.unmount());
  });

  // The label order reads most to least recent regardless of the order
  // threads arrive from the API, so the mock deliberately returns an older
  // thread before a same-day one.
  it("groups chats in a fixed section order regardless of API order", async () => {
    const now = new Date();
    const sqliteTimestamp = (date: Date) => date.toISOString().slice(0, 19).replace("T", " ");
    const todayThread = { threadId: "thread-standup", title: "Standup notes", updatedAt: sqliteTimestamp(now) };
    const olderThread = { threadId: "thread-historical", title: "Historical chat", updatedAt: sqliteTimestamp(new Date(now.getTime() - 10 * 24 * 60 * 60 * 1000)) };
    fetchMock.mockImplementation(async (input) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/dashboards") return dashboards();
      if (url.pathname === "/api/agent/threads") return json({ threads: [olderThread, todayThread], nextCursor: "" });
      throw new Error(`unexpected request: ${url.pathname}`);
    });
    const { root, render } = mount();
    await act(async () => render());
    await vi.waitFor(() => expect(document.body.textContent).toContain("Historical chat"));
    expect(document.body.textContent).toContain("Standup notes");
    const text = document.body.textContent ?? "";
    const todayIndex = text.indexOf("Today");
    const olderIndex = text.indexOf("Older");
    expect(todayIndex).toBeGreaterThanOrEqual(0);
    expect(olderIndex).toBeGreaterThanOrEqual(0);
    expect(todayIndex).toBeLessThan(olderIndex);
    await act(async () => root.unmount());
  });

  it("hides chat affordances when the agent is unavailable", async () => {
    const { root, render } = mount({ agentAvailable: false });
    await act(async () => render());
    await vi.waitFor(() => expect(document.body.textContent).toContain("System overview"));
    expect(document.body.textContent).not.toContain("New chat");
    expect(document.body.textContent).not.toContain("Chats");
    expect(document.body.textContent).not.toContain("Create with AI");
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes("/api/agent/threads"))).toBe(false);
    await act(async () => root.unmount());
  });

  it("searches chats on the server and filters dashboards locally", async () => {
    const { root, ref, render } = mount();
    await act(async () => render());
    await vi.waitFor(() => expect(document.body.textContent).toContain("Checkout latency"));
    act(() => ref.current?.focusSearch());
    const search = document.querySelector('input[aria-label="Search chats, dashboards and services"]') as HTMLInputElement;
    expect(document.activeElement).toBe(search);
    await act(async () => setValue(search, "checkout"));
    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([input]) => String(input).includes("q=checkout"))).toBe(true), { timeout: 1500 });
    await vi.waitFor(() => expect(document.body.textContent).toContain("Result for checkout"));
    expect(document.body.textContent).toContain("Checkout");
    expect(document.body.textContent).not.toContain("System overview");
    expect(document.body.textContent).not.toContain("No matching dashboards");
    await act(async () => root.unmount());
  });

  // A search that matches no dashboard has to say so, or the section reads as
  // an app with no dashboards in it.
  it("says when a search matches no dashboard", async () => {
    const { root, render } = mount();
    await act(async () => render());
    await vi.waitFor(() => expect(document.body.textContent).toContain("System overview"));
    const search = document.querySelector('input[aria-label="Search chats, dashboards and services"]') as HTMLInputElement;
    await act(async () => setValue(search, "zzz"));
    await vi.waitFor(() => expect(document.body.textContent).toContain("No matching dashboards"), { timeout: 1500 });
    expect(document.body.textContent).toContain("Create with AI");
    await act(async () => root.unmount());
  });

  // A search naming a live service used to come back empty-handed, because the
  // search only knew about things the user had already named.
  it("offers a matching service and opens an investigation", async () => {
    const { root, handlers, render } = mount();
    await act(async () => render());
    await vi.waitFor(() => expect(document.body.textContent).toContain("System overview"));
    const search = document.querySelector('input[aria-label="Search chats, dashboards and services"]') as HTMLInputElement;
    await act(async () => setValue(search, "payments"));
    await vi.waitFor(() => expect(document.body.textContent).toContain("Services"), { timeout: 1500 });
    const service = Array.from(document.querySelectorAll<HTMLElement>(".rail-row")).find((row) => row.textContent?.trim() === "payments");
    expect(service).not.toBeUndefined();
    await act(async () => service?.click());
    expect(handlers.onInvestigateService).toHaveBeenCalledWith("payments");
    await act(async () => root.unmount());
  });

  it("keeps the service catalogue loading until its matching rows are ready", async () => {
    let resolveSchema!: (response: Response) => void;
    const schemaResponse = new Promise<Response>((resolve) => { resolveSchema = resolve; });
    fetchMock.mockImplementation(async (input, init) => String(input).includes("/api/telemetry/schema") ? schemaResponse : respond(input, init));
    const { root, render } = mount();
    try {
      await act(async () => render());
      await vi.waitFor(() => expect(document.body.textContent).toContain("System overview"));
      await act(async () => setValue(document.querySelector('input[aria-label="Search chats, dashboards and services"]')!, "payments"));
      await vi.waitFor(() => expect(fetchMock.mock.calls.some(([input]) => String(input).includes("/api/telemetry/schema"))).toBe(true));
      expect(document.body.textContent).not.toContain("Services");
      expect(document.body.textContent).not.toContain("No matching services");
      await act(async () => resolveSchema(json({ services: [{ value: "payments", count: 8 }] })));
      await vi.waitFor(() => expect(document.body.textContent).toContain("Services"));
      expect([...document.querySelectorAll(".rail-row")].some(row => row.textContent?.trim() === "payments")).toBe(true);
    } finally {
      resolveSchema(json({ services: [] }));
      await act(async () => root.unmount());
    }
  });

  it("does not ask for the service catalogue until someone searches", async () => {
    const { root, render } = mount();
    await act(async () => render());
    await vi.waitFor(() => expect(document.body.textContent).toContain("System overview"));
    expect(fetchMock.mock.calls.some(([input]) => String(input).includes("/api/telemetry/schema"))).toBe(false);
    await act(async () => root.unmount());
  });

  it("renames and deletes a chat", async () => {
    const { root, handlers, render } = mount();
    await act(async () => render());
    await vi.waitFor(() => expect(document.querySelector('button[aria-label="Actions for Checkout latency"]')).not.toBeNull());
    const openActions = () => document.querySelector('button[aria-label="Actions for Checkout latency"]') as HTMLButtonElement;

    await act(async () => openActions().click());
    const rename = Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).find((item) => item.textContent?.includes("Rename"));
    await act(async () => rename?.click());
    const name = document.querySelector('input[value="Checkout latency"]') as HTMLInputElement;
    await act(async () => setValue(name, "Checkout follow-up"));
    const save = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.trim() === "Save");
    await act(async () => save?.click());
    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "PATCH")).toBe(true));

    await act(async () => openActions().click());
    const remove = Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).find((item) => item.textContent?.includes("Delete"));
    await act(async () => remove?.click());
    const confirm = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')).find((button) => button.textContent?.trim() === "Delete");
    await act(async () => confirm?.click());
    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "DELETE")).toBe(true));
    expect(handlers.onDeletedThread).toHaveBeenCalledWith("thread-checkout");
    await act(async () => root.unmount());
  });

it.each(["empty","error"])("keeps schema search %s visible without inventing services",async mode=>{
 vi.stubGlobal("fetch",fetchMock);fetchMock.mockReset();document.body.innerHTML="";
 fetchMock.mockImplementation(async(input,init)=>String(input).includes("/api/telemetry/schema")?json(mode==="error"?{message:"Unavailable"}:{services:[]},mode==="error"?503:200):respond(input,init));
 const {root,render}=mount();await act(async()=>render());try {
 await vi.waitFor(()=>expect(document.body.textContent).toContain("System overview"));
 await act(async()=>setValue(document.querySelector('input[aria-label="Search chats, dashboards and services"]')!,"missing-service"));
 await vi.waitFor(()=>expect(fetchMock.mock.calls.some(([input])=>String(input)==="/api/telemetry/schema?window=1h")).toBe(true),{timeout:1500});
 await vi.waitFor(()=>expect(document.body.textContent).toContain(mode==="error"?"Services could not be searched":"No matching services"));
 expect([...document.querySelectorAll(".rail-row")].some(n=>n.textContent?.trim()==="missing-service")).toBe(false);
 }finally{await act(async()=>root.unmount());}
});
});
