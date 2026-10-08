import { useEffect, useRef, type RefObject } from "react";
import { shortcutKey } from "./shortcuts";

type Options = {modalOpen?: boolean; fragment?: boolean; fullscreenScope?: string; enabled?: boolean};
/** Dashboard body focus and its own fullscreen portal belong to this surface. */
export function useShortcuts(region: RefObject<HTMLElement | null>, actions: Partial<Record<string, (target: Element) => void>>, options: Options = {}) {
  const latest = useRef({actions, options}); latest.current = {actions, options};
  useEffect(() => {
    const listener = (event: KeyboardEvent) => {
      const root = region.current, target = event.target;
      const {actions, options} = latest.current;
      if (!root || !(target instanceof Element) || options.enabled === false) return;
      const fullscreen = options.fullscreenScope ? [...document.querySelectorAll<HTMLElement>("[data-shortcut-scope]")].find(el => el.dataset.shortcutScope === options.fullscreenScope && (el.hasAttribute("data-panel-fullscreen") || el.hasAttribute("data-fragment-fullscreen"))) : undefined;
      const within = root.contains(target) && root.contains(document.activeElement);
      const fromBody = !options.fragment && target === document.body && document.activeElement === document.body;
      const fromFullscreen = fullscreen?.contains(target) && fullscreen.contains(document.activeElement);
      if (!within && !fromBody && !fromFullscreen) return;
      if (!options.fragment && target.closest("[data-dashboard-fragment]") && !fromFullscreen) return;
      const blocked = options.modalOpen || [...document.querySelectorAll('[role="dialog"]')].some(dialog => dialog !== fullscreen);
      const key = shortcutKey(event, Boolean(blocked), Boolean(fullscreen));
      if (!key || fullscreen && !["r", "f", "?"].includes(key)) return;
      if (options.fragment && ["r", "e", "h"].includes(key) && !(fullscreen && key === "r")) return;
      const action = actions[key];
      if (!action) return;
      event.preventDefault(); action(target);
    };
    document.addEventListener("keydown", listener);
    return () => document.removeEventListener("keydown", listener);
  }, [region]);
}
