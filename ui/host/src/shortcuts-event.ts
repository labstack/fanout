import { useEffect, useSyncExternalStore } from "react";

/** Window event the header help menu sends to open the current page's keyboard shortcuts. */
export const shortcutsEvent = "pitot:shortcuts";

let handlers = 0;
const subscribers = new Set<() => void>();
const notify = () => { for (const subscriber of subscribers) subscriber(); };

/** A page that has keyboard shortcuts registers the dialog that lists them.
 * The help menu offers the item only while such a page is mounted, so it is
 * never a dead entry during loading, not-found or error states. */
export function useShortcutsHandler(open: () => void) {
  useEffect(() => {
    window.addEventListener(shortcutsEvent, open);
    handlers += 1; notify();
    return () => { window.removeEventListener(shortcutsEvent, open); handlers -= 1; notify(); };
  }, [open]);
}

export function useShortcutsAvailable(): boolean {
  return useSyncExternalStore(subscriber => { subscribers.add(subscriber); return () => subscribers.delete(subscriber); }, () => handlers > 0, () => false);
}
