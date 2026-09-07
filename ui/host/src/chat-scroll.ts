import { useCallback, useRef } from "react";

/** How far from the bottom still counts as "at the bottom". A reader who has
 *  scrolled up by more than this has chosen a place in the thread, and moving
 *  them takes it away. */
const anchorSlack = 64;

/** How long after a wheel, a drag or a key the resulting scroll still counts
 *  as that reader's. Scroll events land a frame or two behind the input. */
const inputSettles = 300;

/** Keeps a chat pane pinned to its newest message.
 *
 *  Scrolling once per message does not hold: a turn's height keeps growing
 *  after the message that carried it — markdown lays out, an embedded view
 *  loads in its iframe, a lazy chunk arrives — and each of those pushes the
 *  answer below the fold again. The measured result was an answer that
 *  arrived off-screen with the pane sitting at scrollTop 0, and a 10,000px
 *  reply whose first screen was all the reader ever saw.
 *
 *  Three things make it hold.
 *
 *  It watches the content's size rather than the message list, so every late
 *  arrival counts, whoever produced it.
 *
 *  It wires itself from ref callbacks rather than an effect. Naming a draft
 *  thread moves the same conversation from /chat to /chat/<id> without
 *  changing the thread's identity: React reconciles the two routes into one
 *  component instance and swaps the DOM underneath it, so an effect keyed on
 *  anything the component knows about does not re-run, and every listener
 *  stays on a container that is no longer on the page. A ref callback fires
 *  exactly when the node it is given changes, which is the event that matters.
 *
 *  It lets go only for an actual wheel, drag or key. Position cannot be used
 *  to infer that the reader left: the pane's own scrolling moves scrollTop,
 *  and so does the browser clamping it every time a streaming answer briefly
 *  gets shorter. Both were read as scrolling up.
 */
export function useStickToBottom<Scroller extends HTMLElement, Content extends HTMLElement>() {
  const scroller = useRef<Scroller | null>(null);
  const stuck = useRef(true);
  const detach = useRef<() => void>(() => {});
  const observer = useRef<ResizeObserver | null>(null);

  const follow = useCallback(() => {
    const node = scroller.current;
    if (node && stuck.current) node.scrollTop = node.scrollHeight;
  }, []);

  const scrollRef = useCallback((node: Scroller | null) => {
    detach.current();
    scroller.current = node;
    if (!node) return;

    let handledAt = 0;
    let dragging = false;
    const handled = () => { handledAt = Date.now(); };
    const onPointerDown = () => { dragging = true; };
    const onPointerUp = () => { dragging = false; };
    const onScroll = () => {
      if (node.scrollHeight - node.clientHeight - node.scrollTop <= anchorSlack) stuck.current = true;
      else if (dragging || Date.now() - handledAt < inputSettles) stuck.current = false;
    };
    node.addEventListener("scroll", onScroll, { passive: true });
    node.addEventListener("wheel", handled, { passive: true });
    node.addEventListener("touchmove", handled, { passive: true });
    node.addEventListener("keydown", handled);
    node.addEventListener("pointerdown", onPointerDown);
    window.addEventListener("pointerup", onPointerUp);
    detach.current = () => {
      node.removeEventListener("scroll", onScroll);
      node.removeEventListener("wheel", handled);
      node.removeEventListener("touchmove", handled);
      node.removeEventListener("keydown", handled);
      node.removeEventListener("pointerdown", onPointerDown);
      window.removeEventListener("pointerup", onPointerUp);
      detach.current = () => {};
    };

    // A pane that opens on an existing thread opens at its newest message,
    // the way returning to a conversation should.
    stuck.current = true;
    node.scrollTop = node.scrollHeight;
  }, []);

  const contentRef = useCallback((node: Content | null) => {
    observer.current?.disconnect();
    observer.current = null;
    if (!node) return;
    observer.current = new ResizeObserver(follow);
    observer.current.observe(node);
  }, [follow]);

  const toBottom = useCallback(() => {
    stuck.current = true;
    follow();
  }, [follow]);

  return { scrollRef, contentRef, toBottom };
}
