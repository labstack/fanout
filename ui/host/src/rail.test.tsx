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
    { id: "dash-main", name: "System overview", description: "", is_default: true, widget_count: 4, updated_at: "2026-07-22 03:00:00" },
    { id: "dash-checkout", name: "Checkout", description: "", is_default: false, widget_count: 2, updated_at: "2026-07-22 03:00:00" },
  ] });
}

function respond(input: RequestInfo | URL, init?: RequestInit) {
  const url = new URL(String(input), "http://localhost");
  if (url.pathname === "/api/dashboards") return dashboards();
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
    onNewChat: vi.fn(), onSelectThread: vi.fn(), onDeletedThread: vi.fn(), onSelectDashboard: vi.fn(), onCreateDashboard: vi.fn(),
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

    const dashboard = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Checkout") && !button.textContent.includes("latency"));
    await act(async () => dashboard?.click());
    expect(handlers.onSelectDashboard).toHaveBeenCalledWith("dash-checkout");

    const newChat = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.trim() === "New chat");
    await act(async () => newChat?.click());
    expect(handlers.onNewChat).toHaveBeenCalled();
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
    const search = document.querySelector('input[aria-label="Search chats and dashboards"]') as HTMLInputElement;
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
    const search = document.querySelector('input[aria-label="Search chats and dashboards"]') as HTMLInputElement;
    await act(async () => setValue(search, "zzz"));
    await vi.waitFor(() => expect(document.body.textContent).toContain("No matching dashboards"), { timeout: 1500 });
    expect(document.body.textContent).toContain("Create with AI");
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
});
