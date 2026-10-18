import { useCallback, useLayoutEffect, useRef, useState } from "react";

const questionInset = 16;
const scrollKeys = new Set(["ArrowUp", "ArrowDown", "PageUp", "PageDown", "Home", "End", " "]);

/** Anchors the latest turn at its question, leaving its answer to grow below.
 *
 * Markdown, iframe views and lazy chunks keep changing layout after messages
 * arrive. Observe the pane, content and individual message blocks: growth above
 * the question can move it even when the current turn's minimum height keeps
 * the overall content height unchanged. That minimum is only a floor, so the
 * turn consumes the spare room as its own content fills the pane.
 *
 * Wire from ref callbacks. Naming a draft moves /chat to /chat/<id> while React
 * can keep this component instance and replace its DOM. Effects keyed only on
 * thread identity leave listeners and observers on discarded nodes.
 *
 * Only reader input releases the question. Programmatic scroll events and the
 * browser clamping scrollTop as content shrinks must never release it. After
 * release, retain the visible message through late growth above the viewport.
 */
export function useTurnAnchor<Scroller extends HTMLElement, Content extends HTMLElement>(lastQuestion?: string) {
  const scroller = useRef<Scroller | null>(null);
  const content = useRef<Content | null>(null);
  const turn = useRef<Content | null>(null);
  const anchored = useRef(true);
  const question = useRef(lastQuestion);
  const detach = useRef<() => void>(() => {});
  const observer = useRef<ResizeObserver | null>(null);
  const observed = useRef(new Set<Element>());
  const readerAnchor = useRef<{ node: HTMLElement; top: number } | null>(null);
  const programmaticTop = useRef<number | null>(null);
  const [showJump, setShowJump] = useState(false);

  const updateJump = useCallback(() => {
    const pane = scroller.current;
    const newest = turn.current?.lastElementChild;
    // The height floor is scrollable, but empty room is not new content.
    const belowFold = !pane || !newest || newest.getBoundingClientRect().bottom > pane.getBoundingClientRect().top + pane.clientTop + pane.clientHeight + 1;
    setShowJump(!!pane && belowFold && pane.scrollHeight - pane.clientHeight - pane.scrollTop > 1);
  }, []);

  const captureAnchor = useCallback(() => {
    const pane = scroller.current;
    if (!pane) return;
    const top = pane.getBoundingClientRect().top + pane.clientTop;
    const node = [...pane.querySelectorAll<HTMLElement>("[data-chat-anchor]")].find(item => item.getBoundingClientRect().bottom > top);
    readerAnchor.current = node ? { node, top: node.getBoundingClientRect().top } : null;
  }, []);

  const moveTo = useCallback((pane: Scroller, top: number) => {
    pane.scrollTop = top;
    programmaticTop.current = pane.scrollTop;
  }, []);

  const align = useCallback(() => {
    const pane = scroller.current;
    if (!pane) return;
    if (turn.current) {
      const bottomPadding = content.current ? parseFloat(getComputedStyle(content.current).paddingBottom) || 0 : 0;
      turn.current.style.minHeight = `${Math.max(0, pane.clientHeight - questionInset - bottomPadding)}px`;
      if (anchored.current) moveTo(pane, pane.scrollTop + turn.current.getBoundingClientRect().top - pane.getBoundingClientRect().top - pane.clientTop - questionInset);
    }
    if (!anchored.current) {
      const anchor = readerAnchor.current;
      if (anchor?.node.isConnected) moveTo(pane, pane.scrollTop + anchor.node.getBoundingClientRect().top - anchor.top);
      captureAnchor();
    }
    updateJump();
  }, [captureAnchor, moveTo, updateJump]);

  const observeLayout = useCallback(() => {
    const pane = scroller.current, body = content.current;
    if (!pane || !body) {
      observer.current?.disconnect();
      observer.current = null;
      observed.current.clear();
      return;
    }
    observer.current ??= new ResizeObserver(align);
    const targets = new Set<Element>([pane, body, ...body.querySelectorAll("[data-chat-anchor]")]);
    if (turn.current) targets.add(turn.current);
    for (const node of observed.current) if (!targets.has(node)) observer.current.unobserve(node);
    for (const node of targets) if (!observed.current.has(node)) observer.current.observe(node);
    observed.current = targets;
  }, [align]);

  const scrollRef = useCallback((node: Scroller | null) => {
    detach.current();
    scroller.current = node;
    programmaticTop.current = null;
    readerAnchor.current = null;
    anchored.current = true;
    observeLayout();
    if (!node) return;

    let dragging = false;
    const release = () => { anchored.current = false; captureAnchor(); };
    const onKeyDown = (event: KeyboardEvent) => {
      const target = event.target;
      if (event.defaultPrevented || !scrollKeys.has(event.key)) return;
      if (target instanceof HTMLElement) {
        if (target.closest("input, textarea, select, [contenteditable]")) return;
        // Space activates a button; navigation keys still scroll the pane.
        // Links consume Enter, which is not a scrolling key.
        if (event.key === " " ? target.closest("button") : target.closest("[role=slider]")) return;
      }
      release();
    };
    const onPointerDown = () => { dragging = true; };
    const onPointerUp = () => { dragging = false; };
    const onScroll = () => {
      if (dragging && node.scrollTop !== programmaticTop.current) release();
      if (!anchored.current) captureAnchor();
      updateJump();
    };
    node.addEventListener("scroll", onScroll, { passive: true });
    node.addEventListener("wheel", release, { passive: true });
    node.addEventListener("touchmove", release, { passive: true });
    node.addEventListener("keydown", onKeyDown);
    node.addEventListener("pointerdown", onPointerDown);
    window.addEventListener("pointerup", onPointerUp);
    window.addEventListener("pointercancel", onPointerUp);
    detach.current = () => {
      node.removeEventListener("scroll", onScroll);
      node.removeEventListener("wheel", release);
      node.removeEventListener("touchmove", release);
      node.removeEventListener("keydown", onKeyDown);
      node.removeEventListener("pointerdown", onPointerDown);
      window.removeEventListener("pointerup", onPointerUp);
      window.removeEventListener("pointercancel", onPointerUp);
      detach.current = () => {};
    };
    align();
  }, [align, captureAnchor, observeLayout, updateJump]);

  const contentRef = useCallback((node: Content | null) => {
    content.current = node;
    observeLayout();
  }, [observeLayout]);

  const turnRef = useCallback((node: Content | null) => {
    turn.current?.style.removeProperty("min-height");
    turn.current = node;
    observeLayout();
  }, [observeLayout]);

  useLayoutEffect(() => {
    if (question.current !== lastQuestion) {
      question.current = lastQuestion;
      anchored.current = true;
      readerAnchor.current = null;
    }
    // Include newly rendered message blocks, even if the turn's height floor
    // absorbed their growth and no container resize occurred.
    observeLayout();
    align();
  });

  const jumpToLatest = useCallback(() => {
    const pane = scroller.current;
    if (!pane) return;
    // The jump control disappears at the bottom; keep focus in the conversation.
    pane.focus({ preventScroll: true });
    anchored.current = false;
    moveTo(pane, pane.scrollHeight);
    captureAnchor();
    updateJump();
  }, [captureAnchor, moveTo, updateJump]);

  return { scrollRef, contentRef, turnRef, showJump, jumpToLatest };
}
