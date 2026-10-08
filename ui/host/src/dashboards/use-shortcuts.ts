import { useEffect, useRef, type RefObject } from "react";
import { shortcutKey } from "./shortcuts";

/** Mount only in the active surface; the dashboard never owns chat events. */
export function useShortcuts(region: RefObject<HTMLElement | null>, actions: Partial<Record<string, (target: Element) => void>>, modalOpen = false, fragment = false) {
  const latest = useRef({actions, modalOpen}); latest.current = {actions, modalOpen};
  useEffect(() => {
    const listener = (event: KeyboardEvent) => {
      const root = region.current, target = event.target;
      if (!root || !(target instanceof Element) || !root.contains(target) || !root.contains(document.activeElement)) return;
      if (!fragment && target.closest("[data-dashboard-fragment]")) return;
      const key = shortcutKey(event, latest.current.modalOpen || Boolean(document.querySelector('[role="dialog"]')));
      if (!key || fragment && ["r", "e", "h"].includes(key)) return;
      const action = latest.current.actions[key];
      if (!action) return;
      event.preventDefault(); action(target);
    };
    document.addEventListener("keydown", listener);
    return () => document.removeEventListener("keydown", listener);
  }, [region, fragment]);
}
