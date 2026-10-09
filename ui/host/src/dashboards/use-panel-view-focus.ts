import { useEffect, useRef, type RefObject } from "react";

/** View replaces a plot, so restore the matching control if its DOM was rebuilt. */
export function usePanelViewFocus(region: RefObject<HTMLElement | null>) {
  const source = useRef<{element: HTMLElement; panel: string; selector?: string} | undefined>(undefined);
  const boardPanel = (id: string) => [...region.current?.querySelectorAll<HTMLElement>("[data-panel]") ?? []].find(el => el.dataset.panel === id && !el.closest('[data-panel-fullscreen],[data-fragment-fullscreen]'));
  useEffect(() => { if (source.current && !boardPanel(source.current.panel)) source.current = undefined; });
  return {
    remember(panel: string) {
      source.current = undefined;
      const element = document.activeElement;
      if (!(element instanceof HTMLElement) || !region.current?.contains(element) || element.closest<HTMLElement>("[data-panel]")?.dataset.panel !== panel) return;
      const view = element.getAttribute("data-panel-view"), label = element.getAttribute("aria-label");
      const selector = element.hasAttribute("data-chart-plot") ? "[data-chart-plot]" : view ? `[data-panel-view=${JSON.stringify(view)}]` : label ? `[aria-label=${JSON.stringify(label)}]` : undefined;
      source.current = {element, panel, selector};
    },
    restore(panel?: string) {
      const saved = source.current;
      source.current = undefined;
      if (!saved || panel !== undefined && saved.panel !== panel || !boardPanel(saved.panel)) return;
      if (saved.element.isConnected && !saved.element.closest('[hidden]')) return saved.element;
      return saved.selector ? boardPanel(saved.panel)?.querySelector<HTMLElement>(saved.selector) ?? undefined : undefined;
    },
  };
}
