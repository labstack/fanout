import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useStickToBottom } from "./chat-scroll";

/* happy-dom does no layout, and its ResizeObserver is a no-op, so the pane's
   geometry and the observer are both declared here: a ~200px viewport over
   content that grows the way a turn does while its markdown lays out and its
   embedded views load. */
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

  async function mount(pane?: string) {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    await act(async () => root.render(<Pane pane={pane} />));
    const scroller = container.querySelector<HTMLDivElement>('[data-testid="scroller"]')!;
    sized(scroller, { scrollHeight: 200, clientHeight: 200 });
    return { root, scroller, container };
  }

  /** Move the pane and tell it, the way the browser does. */
  function scrollTo(scroller: HTMLElement, top: number) {
    scroller.scrollTop = top;
    scroller.dispatchEvent(new Event("scroll"));
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

  it("stays pinned when the pane is moved by something that is not the reader", async () => {
    // Position alone cannot tell these apart from a reader scrolling up: the
    // pane's own scrolling reports intermediate positions, and the browser
    // clamps scrollTop every time a streaming answer briefly gets shorter.
    // Both used to unpin the pane and abandon a reader mid-answer.
    const { root, scroller } = await mount();
    sized(scroller, { scrollHeight: 4000, clientHeight: 200 });
    scrollTo(scroller, 1200);
    sized(scroller, { scrollHeight: 10000, clientHeight: 200 });
    act(() => resize?.());
    expect(scroller.scrollTop).toBe(10000);
    await act(async () => root.unmount());
  });

  it("lets go for a wheel, and for a key, and for a scrollbar drag", async () => {
    for (const leave of [
      (scroller: HTMLElement) => scroller.dispatchEvent(new Event("wheel")),
      (scroller: HTMLElement) => scroller.dispatchEvent(new KeyboardEvent("keydown", { key: "PageUp", bubbles: true })),
      (scroller: HTMLElement) => scroller.dispatchEvent(new Event("pointerdown")),
    ]) {
      const { root, scroller } = await mount();
      sized(scroller, { scrollHeight: 4000, clientHeight: 200 });
      leave(scroller);
      scrollTo(scroller, 1200);
      sized(scroller, { scrollHeight: 10000, clientHeight: 200 });
      act(() => resize?.());
      expect(scroller.scrollTop).toBe(1200);
      await act(async () => root.unmount());
      window.dispatchEvent(new Event("pointerup"));
      document.body.innerHTML = "";
    }
  });

  it("pins again when the reader comes back to the bottom", async () => {
    const { root, scroller } = await mount();
    sized(scroller, { scrollHeight: 4000, clientHeight: 200 });
    scroller.dispatchEvent(new Event("wheel"));
    scrollTo(scroller, 1200);
    // Back to within the anchor's slack of the bottom.
    scrollTo(scroller, 3790);
    sized(scroller, { scrollHeight: 6000, clientHeight: 200 });
    act(() => resize?.());
    expect(scroller.scrollTop).toBe(6000);
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
    expect(second).not.toBe(first);
    sized(second, { scrollHeight: 900, clientHeight: 200 });
    act(() => resize?.());
    expect(second.scrollTop).toBe(900);
    await act(async () => root.unmount());
  });
});
