import { act, useCallback } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { useTurnAnchor } from "./chat-scroll";

/* happy-dom has no layout. Model a clamped scroll viewport with an earlier
   turn above the question and an answer growing below it. The current turn's
   real min-height participates in this geometry, including when growth above
   it leaves the overall content height unchanged. */
type Geometry = { above: number; below: number; viewport: number };
let resize: (() => void) | null = null;
let observed = new Set<Element>();

class FakeResizeObserver {
  constructor(callback: () => void) { resize = callback; }
  observe(node: Element) { observed.add(node); }
  unobserve(node: Element) { observed.delete(node); }
  disconnect() { resize = null; observed.clear(); }
}

function rect(top: number, height: number) { return { top, bottom: top + height, height } as DOMRect; }

function Pane({ pane = "thread-1", question = "q1", geometry }: { pane?: string; question?: string; geometry: Geometry }) {
  const { scrollRef, contentRef, turnRef, showJump, jumpToLatest } = useTurnAnchor<HTMLDivElement, HTMLDivElement>(question);
  const setScroller = useCallback((node: HTMLDivElement | null) => {
    if (node) {
      let scrollTop = 0;
      Object.defineProperties(node, {
        clientHeight: { get: () => geometry.viewport, configurable: true },
        scrollHeight: { get: () => geometry.above + Math.max(geometry.below, parseFloat(node.querySelector<HTMLElement>("[data-chat-turn]")?.style.minHeight ?? "0") || 0) + 16, configurable: true },
        scrollTop: { get: () => scrollTop, set: (top: number) => { scrollTop = Math.max(0, Math.min(top, node.scrollHeight - node.clientHeight)); }, configurable: true },
      });
      node.getBoundingClientRect = () => rect(40, geometry.viewport);
      node.querySelector<HTMLElement>("[data-history]")!.getBoundingClientRect = () => rect(40 - scrollTop, geometry.above);
      node.querySelector<HTMLElement>("[data-question]")!.getBoundingClientRect = () => rect(40 + geometry.above - scrollTop, 50);
    }
    scrollRef(node);
  }, [geometry, scrollRef]);
  const setTurn = useCallback((node: HTMLDivElement | null) => {
    if (node) {
      node.getBoundingClientRect = () => rect(40 + geometry.above - (node.closest<HTMLElement>("[data-scroller]")?.scrollTop ?? 0), Math.max(geometry.below, parseFloat(node.style.minHeight) || 0));
      node.querySelector<HTMLElement>("[data-question]")!.getBoundingClientRect = () => rect(node.getBoundingClientRect().top, 50);
      node.querySelector<HTMLElement>("[data-answer]")!.getBoundingClientRect = () => rect(node.getBoundingClientRect().top + 50, geometry.below - 50);
    }
    turnRef(node);
  }, [geometry, turnRef]);
  return <>
    <div key={pane} ref={setScroller} data-scroller tabIndex={0}>
      <div ref={contentRef} style={{ paddingBottom: 16 }}>
        <div data-history data-chat-anchor>Earlier turn</div>
        <div key={question} ref={setTurn} data-chat-turn>
          <div data-question data-chat-anchor>{question}</div>
          <textarea aria-label="Embedded input" />
          <div data-answer>Answer</div>
        </div>
      </div>
    </div>
    {showJump && <button aria-label="Jump to latest" onClick={jumpToLatest}>↓</button>}
  </>;
}

describe("chat turn anchoring", () => {
  beforeEach(() => { vi.stubGlobal("ResizeObserver", FakeResizeObserver); vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); });
  afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ""; resize = null; observed = new Set(); });

  async function mount(initial: Partial<Geometry> = {}) {
    const geometry = { above: 600, below: 80, viewport: 200, ...initial };
    const container = document.createElement("div"); document.body.append(container);
    const root = createRoot(container);
    const render = async (props: { question?: string; pane?: string } = {}) => { await act(async () => root.render(<Pane {...props} geometry={geometry} />)); };
    await render();
    const scroller = container.querySelector<HTMLDivElement>("[data-scroller]")!;
    const questionTop = () => container.querySelector<HTMLElement>("[data-question]")!.getBoundingClientRect().top - scroller.getBoundingClientRect().top;
    const jump = () => container.querySelector<HTMLButtonElement>('button[aria-label="Jump to latest"]');
    return { root, container, scroller, geometry, render, questionTop, jump };
  }

  function scrollTo(scroller: HTMLElement, top: number) {
    act(() => { scroller.scrollTop = top; scroller.dispatchEvent(new Event("scroll")); });
  }
  function layout() { act(() => resize?.()); }

  it("opens an existing thread with its last question at the top inset", async () => {
    const { root, scroller, questionTop, jump } = await mount();
    expect(questionTop()).toBe(16);
    expect(scroller.scrollTop).toBe(584);
    expect(jump()).toBeNull();
    await act(async () => root.unmount());
  });

  it("anchors a newly sent question even after the reader left the previous one", async () => {
    const { root, scroller, geometry, render, questionTop } = await mount();
    scroller.dispatchEvent(new Event("wheel")); scrollTo(scroller, 100);
    geometry.above = 1400;
    await render({ question: "q2" });
    expect(questionTop()).toBe(16);
    expect(scroller.scrollTop).toBe(1384);
    await act(async () => root.unmount());
  });

  it("gives a short first turn room to anchor and consumes that room as the answer grows", async () => {
    const { root, scroller, geometry, questionTop, container } = await mount({ above: 20, below: 60 });
    expect(questionTop()).toBe(16);
    const turn = container.querySelector<HTMLElement>("[data-chat-turn]")!;
    expect(turn.style.minHeight).toBe("168px");
    expect(scroller.scrollHeight).toBe(204);
    geometry.below = 1000; layout();
    expect(scroller.scrollHeight).toBe(1036); // No extra 168px after a full turn.
    expect(questionTop()).toBe(16);
    expect(scroller.scrollTop).toBe(4);
    await act(async () => root.unmount());
  });

  it("keeps the question anchored through markdown, iframe and lazy answer growth below it", async () => {
    const { root, scroller, geometry, questionTop } = await mount();
    for (const below of [900, 4000, 10000]) {
      geometry.below = below; layout();
      expect(questionTop()).toBe(16);
      expect(scroller.scrollTop).toBe(584);
    }
    await act(async () => root.unmount());
  });

  it("corrects late growth above the question, including when total content height stays the same", async () => {
    const { root, scroller, geometry, questionTop, container } = await mount({ below: 1000 });
    expect(observed.has(container.querySelector("[data-history]")!)).toBe(true);
    const height = scroller.scrollHeight;
    geometry.above += 300; geometry.below -= 300; layout();
    expect(scroller.scrollHeight).toBe(height);
    expect(questionTop()).toBe(16);
    expect(scroller.scrollTop).toBe(884);
    await act(async () => root.unmount());
  });

  it("does not treat programmatic scrolls or content clamping as reader input", async () => {
    const { root, scroller, geometry, questionTop } = await mount({ below: 4000 });
    scrollTo(scroller, 1200);
    geometry.below = 60; scrollTo(scroller, scroller.scrollTop); layout();
    expect(questionTop()).toBe(16);
    geometry.below = 10000; layout();
    expect(questionTop()).toBe(16);
    // A scroll produced by the hook while a pointer is held is still its own.
    scroller.dispatchEvent(new Event("pointerdown"));
    geometry.above += 300; layout();
    scroller.dispatchEvent(new Event("scroll"));
    geometry.above += 300; layout();
    expect(questionTop()).toBe(16);
    window.dispatchEvent(new Event("pointerup"));
    await act(async () => root.unmount());
  });

  it.each(["wheel", "touch", "key", "drag"])("releases the question for a reader's %s scroll", async input => {
    const { root, scroller, geometry } = await mount({ below: 4000 });
    if (input === "key") scroller.dispatchEvent(new KeyboardEvent("keydown", { key: "PageUp", bubbles: true }));
    else scroller.dispatchEvent(new Event(input === "touch" ? "touchmove" : input === "drag" ? "pointerdown" : "wheel"));
    scrollTo(scroller, 1200);
    window.dispatchEvent(new Event("pointerup"));
    geometry.below = 10000; layout();
    expect(scroller.scrollTop).toBe(1200);
    await act(async () => root.unmount());
  });

  it("releases on wheel input even before its delayed scroll event arrives", async () => {
    const { root, scroller, geometry } = await mount();
    scroller.dispatchEvent(new Event("wheel"));
    scrollTo(scroller, 100);
    geometry.below = 4000; layout();
    expect(scroller.scrollTop).toBe(100);
    await act(async () => root.unmount());
  });

  it("retains the reader's visible message when an earlier iframe grows", async () => {
    const { root, scroller, geometry, questionTop } = await mount({ below: 4000 });
    scroller.dispatchEvent(new Event("wheel")); scrollTo(scroller, 620);
    expect(questionTop()).toBe(-20);
    geometry.above += 300; layout();
    expect(scroller.scrollTop).toBe(920);
    expect(questionTop()).toBe(-20);
    await act(async () => root.unmount());
  });

  it("keeps the anchor for non-scroll keys and typing in embedded inputs", async () => {
    const { root, scroller, geometry, container, questionTop } = await mount();
    scroller.dispatchEvent(new KeyboardEvent("keydown", { key: "a", bubbles: true }));
    container.querySelector("textarea")!.dispatchEvent(new KeyboardEvent("keydown", { key: "ArrowUp", bubbles: true }));
    scrollTo(scroller, 100);
    geometry.below = 4000; layout();
    expect(questionTop()).toBe(16);
    await act(async () => root.unmount());
  });

  it.each([
    ...["button", "a"].flatMap(control => ["PageUp", "PageDown", "Home", "End", "ArrowUp", "ArrowDown"].map(key => ({ control, key }))),
    { control: "a", key: " " },
  ])("releases for $key scrolling with a focused $control", async ({ control, key }) => {
    const { root, scroller, geometry } = await mount({ below: 4000 });
    try {
      const target = document.createElement(control);
      if (target instanceof HTMLAnchorElement) target.href = "#question";
      scroller.querySelector("[data-answer]")!.append(target);
      target.focus();
      expect(document.activeElement).toBe(target);
      target.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
      // happy-dom does not perform native keyboard scrolling.
      scrollTo(scroller, key === "PageUp" || key === "Home" || key === "ArrowUp" ? 100 : 1200);
      const top = scroller.scrollTop;
      geometry.below = 10000; layout();
      expect(scroller.scrollTop).toBe(top);
    } finally { await act(async () => root.unmount()); }
  });

  it.each([
    { markup: "<button>Copy</button>", key: " " },
    { markup: "<button>Copy</button>", key: "Enter" },
    { markup: "<a href='#question'>Evidence</a>", key: "Enter" },
    { markup: "<textarea></textarea>", key: "PageDown" },
    { markup: "<input />", key: "Home" },
    { markup: "<div contenteditable='true' tabindex='0'></div>", key: "ArrowDown" },
  ])("keeps the anchor for $key consumed by $markup", async ({ markup, key }) => {
    const { root, scroller, geometry, questionTop } = await mount({ below: 4000 });
    try {
      const wrapper = document.createElement("div");
      wrapper.innerHTML = markup;
      scroller.querySelector("[data-answer]")!.append(wrapper);
      const target = wrapper.firstElementChild as HTMLElement;
      target.focus();
      expect(document.activeElement).toBe(target);
      target.dispatchEvent(new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true }));
      scrollTo(scroller, 100);
      geometry.below = 10000; layout();
      expect(questionTop()).toBe(16);
    } finally { await act(async () => root.unmount()); }
  });

  it("keeps the anchor when a control prevents a scrolling key's default", async () => {
    const { root, scroller, geometry, questionTop } = await mount({ below: 4000 });
    try {
      const target = document.createElement("button");
      target.addEventListener("keydown", event => event.preventDefault());
      scroller.querySelector("[data-answer]")!.append(target);
      target.focus();
      target.dispatchEvent(new KeyboardEvent("keydown", { key: "PageDown", bubbles: true, cancelable: true }));
      scrollTo(scroller, 100);
      geometry.below = 10000; layout();
      expect(questionTop()).toBe(16);
    } finally { await act(async () => root.unmount()); }
  });

  it("shows an accessible jump button below the fold, jumps to the bottom, and hides it there", async () => {
    const { root, scroller, geometry, jump } = await mount();
    expect(jump()).toBeNull();
    geometry.below = 4000; layout();
    expect(jump()).not.toBeNull();
    await act(async () => jump()!.click());
    expect(scroller.scrollTop).toBe(scroller.scrollHeight - scroller.clientHeight);
    expect(jump()).toBeNull();
    // Jumping releases the question; it does not follow subsequent growth.
    const top = scroller.scrollTop;
    geometry.below = 6000; layout();
    expect(scroller.scrollTop).toBe(top);
    expect(jump()).not.toBeNull();
    scrollTo(scroller, scroller.scrollHeight);
    expect(jump()).toBeNull();
    await act(async () => root.unmount());
  });

  it("does not offer a jump for blank turn space when the newest content is already visible", async () => {
    const { root, scroller, jump } = await mount();
    scroller.dispatchEvent(new Event("wheel")); scrollTo(scroller, 550);
    expect(jump()).toBeNull();
    scrollTo(scroller, 0);
    expect(jump()).not.toBeNull();
    await act(async () => root.unmount());
  });

  it("does not resume following the bottom when the reader returns there", async () => {
    const { root, scroller, geometry, jump } = await mount({ below: 4000 });
    scroller.dispatchEvent(new Event("wheel")); scrollTo(scroller, scroller.scrollHeight);
    expect(jump()).toBeNull();
    const top = scroller.scrollTop;
    geometry.below = 6000; layout();
    expect(scroller.scrollTop).toBe(top);
    expect(jump()).not.toBeNull();
    await act(async () => root.unmount());
  });

  it("updates the turn's minimum height when the pane resizes", async () => {
    const { root, scroller, geometry, questionTop, container } = await mount();
    expect(observed.has(scroller)).toBe(true);
    geometry.viewport = 500; layout();
    expect(container.querySelector<HTMLElement>("[data-chat-turn]")!.style.minHeight).toBe("468px");
    expect(questionTop()).toBe(16);
    await act(async () => root.unmount());
  });

  // /chat -> /chat/<id> can preserve the component but swap its DOM nodes.
  it("rewires observers and reader input when the route replaces the pane", async () => {
    const { root, scroller: first, geometry, container, render } = await mount();
    first.dispatchEvent(new Event("wheel")); scrollTo(first, 100);
    await render({ pane: "named-thread" });
    const second = container.querySelector<HTMLDivElement>("[data-scroller]")!;
    expect(second).not.toBe(first);
    expect(observed.has(first)).toBe(false);
    expect(observed.has(second)).toBe(true);
    expect(second.scrollTop).toBe(584);
    // Detached nodes no longer release the anchor on the new pane.
    first.dispatchEvent(new Event("wheel"));
    geometry.above += 200; layout();
    expect(second.scrollTop).toBe(784);
    second.dispatchEvent(new Event("wheel")); scrollTo(second, 100);
    geometry.below = 4000; layout();
    expect(second.scrollTop).toBe(100);
    await act(async () => root.unmount());
    expect(observed.size).toBe(0);
    expect(resize).toBeNull();
  });
});
