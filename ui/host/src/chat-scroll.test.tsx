import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useStickToBottom } from "./chat-scroll";

/* jsdom does no layout, so the pane's geometry is declared here: a 200px
   viewport over content that grows to 900px, which is what a turn does while
   its markdown lays out and its embedded views load. */
function sized(element: HTMLElement, { scrollHeight, clientHeight }: { scrollHeight: number; clientHeight: number }) {
  Object.defineProperty(element, "scrollHeight", { value: scrollHeight, configurable: true });
  Object.defineProperty(element, "clientHeight", { value: clientHeight, configurable: true });
}

let resize: (() => void) | null = null;

class FakeResizeObserver {
  constructor(callback: () => void) { resize = callback; }
  observe() {}
  disconnect() { resize = null; }
}

function Pane({ pane = "thread-1" }: { pane?: string }) {
  const { scrollRef, contentRef } = useStickToBottom<HTMLDivElement, HTMLDivElement>();
  return <div key={pane} ref={scrollRef} data-testid="scroller"><div ref={contentRef}>thread</div></div>;
}

describe("chat scrolling", () => {
  beforeEach(() => { vi.stubGlobal("ResizeObserver", FakeResizeObserver); });
  afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ""; resize = null; });

  async function mount() {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<Pane />));
    const scroller = container.querySelector<HTMLDivElement>('[data-testid="scroller"]')!;
    sized(scroller, { scrollHeight: 200, clientHeight: 200 });
    return { root, scroller };
  }

  it("follows content that keeps growing after the message arrived", async () => {
    const { root, scroller } = await mount();
    expect(scroller.scrollTop).toBe(0);
    // The answer renders, then its markdown and embedded views lay out.
    sized(scroller, { scrollHeight: 900, clientHeight: 200 });
    act(() => resize?.());
    expect(scroller.scrollTop).toBe(900);
    await act(async () => root.unmount());
  });

  it("lets go only when the reader actually scrolls up", async () => {
    const { root, scroller } = await mount();
    sized(scroller, { scrollHeight: 900, clientHeight: 200 });
    scroller.dispatchEvent(new Event("wheel"));
    scroller.scrollTop = 100;
    scroller.dispatchEvent(new Event("scroll"));
    act(() => resize?.());
    expect(scroller.scrollTop).toBe(100);

    // Returning to the bottom pins it again.
    scroller.scrollTop = 700;
    scroller.dispatchEvent(new Event("scroll"));
    sized(scroller, { scrollHeight: 1200, clientHeight: 200 });
    act(() => resize?.());
    expect(scroller.scrollTop).toBe(1200);
    await act(async () => root.unmount());
  });

  // A streaming answer briefly gets shorter as its markdown re-lays out, and
  // the browser clamps the scroll position when it does. Read as a position,
  // that is indistinguishable from the reader scrolling up — and it let go of
  // a 10,000px answer halfway through it.
  it("does not read a layout clamp as the reader leaving", async () => {
    const { root, scroller } = await mount();
    sized(scroller, { scrollHeight: 4000, clientHeight: 200 });
    act(() => resize?.());
    expect(scroller.scrollTop).toBe(4000);

    // The content shrinks; the browser clamps scrollTop and reports a scroll.
    sized(scroller, { scrollHeight: 3000, clientHeight: 200 });
    scroller.scrollTop = 2800;
    scroller.dispatchEvent(new Event("scroll"));
    sized(scroller, { scrollHeight: 10000, clientHeight: 200 });
    act(() => resize?.());
    expect(scroller.scrollTop).toBe(10000);
    await act(async () => root.unmount());
  });

  // Naming a draft thread moves the conversation from /chat to /chat/<id>.
  // React reconciles those two routes into one component instance and swaps
  // the DOM underneath it, so anything wired once stayed on nodes that were no
  // longer on the page — the answer arrived off-screen with the pane at
  // scrollTop 0.
  it("re-arms on the new nodes when the route swaps them", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<Pane pane="thread-1" />));
    const first = container.querySelector<HTMLDivElement>('[data-testid="scroller"]')!;
    sized(first, { scrollHeight: 200, clientHeight: 200 });

    // The route changes: same component, a new scroller underneath it.
    await act(async () => root.render(<Pane pane="thread-2" />));
    const second = container.querySelector<HTMLDivElement>('[data-testid="scroller"]')!;
    sized(second, { scrollHeight: 900, clientHeight: 200 });
    act(() => resize?.());
    expect(second.scrollTop).toBe(900);
    await act(async () => root.unmount());
  });
});
