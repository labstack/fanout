import { useRef } from "react";

/**
 * Remembers the control that opened a dialog. A dialog rendered only while
 * open unmounts without Mantine's focus return, so closing it puts focus back
 * here, or on `fallback` when the opener is gone.
 */
export function useOpener() {
  const opener = useRef<HTMLElement | null>(null);
  return useRef({
    remember(target: EventTarget | null) { opener.current = target instanceof HTMLElement ? target : null; },
    restore(fallback?: HTMLElement | null) {
      const target = opener.current;
      requestAnimationFrame(() => (target?.isConnected ? target : fallback)?.focus());
    },
  }).current;
}
