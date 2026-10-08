import { useRef, type RefObject } from "react";

/** View replaces a plot, so restore the matching control if its DOM was rebuilt. */
export function usePanelViewFocus(region: RefObject<HTMLElement | null>) {
  const source = useRef<{element: HTMLElement; panel: string; selector?: string} | undefined>(undefined);
  return {
    remember(panel: string) {
      const element = document.activeElement;
      if (!(element instanceof HTMLElement) || !region.current?.contains(element)) return;
      const view = element.getAttribute("data-panel-view"), label = element.getAttribute("aria-label");
      const selector = element.hasAttribute("data-chart-plot") ? "[data-chart-plot]" : view ? `[data-panel-view=${JSON.stringify(view)}]` : label ? `[aria-label=${JSON.stringify(label)}]` : undefined;
      source.current = {element, panel, selector};
    },
    restore() {
      const saved = source.current;
      if (!saved) return;
      if (saved.element.isConnected && !saved.element.closest('[hidden]')) return saved.element;
      const panel = [...region.current?.querySelectorAll<HTMLElement>("[data-panel]") ?? []].find(el => el.dataset.panel === saved.panel && !el.closest('[hidden],[data-panel-fullscreen],[data-fragment-fullscreen]'));
      return saved.selector ? panel?.querySelector<HTMLElement>(saved.selector) ?? undefined : undefined;
    },
  };
}
