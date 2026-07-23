import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import ChatHistoryDrawer from "./chat-history";

const fetchMock = vi.fn<typeof fetch>();

function page(query = "") {
  return new Response(JSON.stringify({
    threads: [{
      threadId: "thread-checkout",
      title: query ? `Result for ${query}` : "Investigate checkout latency",
      updatedAt: new Date().toISOString(),
    }],
    nextCursor: "",
  }), { status: 200, headers: { "content-type": "application/json" } });
}

describe("ChatHistoryDrawer", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    fetchMock.mockImplementation(async (input) => {
      const query = new URL(String(input), "http://localhost").searchParams.get("q") ?? "";
      return page(query);
    });
  });

  afterEach(() => {
    vi.unstubAllGlobals();
    document.body.innerHTML = "";
  });

  it("loads owner history, highlights the active thread, and searches", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const select = vi.fn();
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });

    await act(async () => root.render(
      <QueryClientProvider client={queryClient}>
        <MantineProvider>
          <ChatHistoryDrawer opened activeThreadID="thread-checkout" onClose={() => undefined} onNewChat={() => undefined} onSelect={select} onDeleted={() => undefined} />
        </MantineProvider>
      </QueryClientProvider>,
    ));

    await vi.waitFor(() => expect(document.body.textContent).toContain("Investigate checkout latency"));
    const active = document.querySelector('[aria-current="page"]') as HTMLButtonElement;
    expect(active).not.toBeNull();
    await act(async () => active.click());
    expect(select).toHaveBeenCalledWith("thread-checkout");

    const search = document.querySelector('input[aria-label="Search investigations"]') as HTMLInputElement;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(search, "checkout");
      search.dispatchEvent(new InputEvent("input", { bubbles: true, data: "checkout", inputType: "insertText" }));
      search.dispatchEvent(new Event("change", { bubbles: true }));
    });
    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([input]) => String(input).includes("q=checkout"))).toBe(true), { timeout: 1500 });
    await vi.waitFor(() => expect(document.body.textContent).toContain("Result for checkout"));

    await act(async () => root.unmount());
  });

  it("renames and deletes a conversation", async () => {
    fetchMock.mockImplementation(async (_input, init) => {
      if (init?.method === "PATCH") {
        return new Response(JSON.stringify({ title: "Checkout follow-up" }), { status: 200 });
      }
      if (init?.method === "DELETE") return new Response(null, { status: 204 });
      return page();
    });
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const deleted = vi.fn();
    const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });

    await act(async () => root.render(
      <QueryClientProvider client={queryClient}>
        <MantineProvider>
          <ChatHistoryDrawer opened activeThreadID="thread-checkout" onClose={() => undefined} onNewChat={() => undefined} onSelect={() => undefined} onDeleted={deleted} />
        </MantineProvider>
      </QueryClientProvider>,
    ));
    await vi.waitFor(() => expect(document.querySelector('button[aria-label="Actions for Investigate checkout latency"]')).not.toBeNull());

    const openActions = () => document.querySelector('button[aria-label="Actions for Investigate checkout latency"]') as HTMLButtonElement;
    await act(async () => openActions().click());
    const rename = Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).find((item) => item.textContent?.includes("Rename"));
    expect(rename).not.toBeUndefined();
    await act(async () => rename?.click());
    const name = document.querySelector('input[value="Investigate checkout latency"]') as HTMLInputElement;
    await act(async () => {
      Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(name, "Checkout follow-up");
      name.dispatchEvent(new InputEvent("input", { bubbles: true, data: "Checkout follow-up", inputType: "insertText" }));
      name.dispatchEvent(new Event("change", { bubbles: true }));
    });
    const save = Array.from(document.querySelectorAll<HTMLButtonElement>('button')).find((button) => button.textContent?.trim() === "Save");
    expect(save).not.toBeUndefined();
    await act(async () => save?.click());
    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "PATCH")).toBe(true));

    await act(async () => openActions().click());
    const remove = Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).find((item) => item.textContent?.includes("Delete"));
    expect(remove).not.toBeUndefined();
    await act(async () => remove?.click());
    const confirm = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')).find((button) => button.textContent?.trim() === "Delete");
    expect(confirm).not.toBeUndefined();
    await act(async () => confirm?.click());
    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "DELETE")).toBe(true));
    expect(deleted).toHaveBeenCalledWith("thread-checkout");

    await act(async () => root.unmount());
  });
});
