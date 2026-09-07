import { MantineProvider } from "@mantine/core";
import type { Message } from "@ag-ui/client";
import { act, createRef, type FormEvent } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FanoutAppContext, type FanoutAppContextValue } from "./app-context";
import { ChatPage } from "./chat";

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
