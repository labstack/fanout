// Controller calibration: max of three ubuntu-24.04 run p95s (worse theme),
// plus 25%, rounded UP to 50 ms. Null records latency without enforcing it.
// Measured 1025, 1410 and 1239 ms. The spec's 1.5 s target is measured on real
// data locally, not on these smaller runners.
export const CI_RENDER_CEILING_MS: number | null = 1800;

export function p95(values: number[]): number {
  if (values.length < 20 || values.some(value => !Number.isFinite(value) || value < 0)) {
    throw new Error("Twenty finite nonnegative samples required");
  }
  const ordered = [...values].sort((a, b) => a - b);
  return ordered[Math.ceil(0.95 * ordered.length) - 1];
}

export function assertRefreshRequests(paths: string[]) {
  const count = (path: string) => paths.filter(value => value === path).length;
  if (count("/api/panels/query") !== 1 || count("/api/annotations") !== 1 || count("/api/panels/variables/resolve") !== 0) {
    throw new Error("Refresh request budget exceeded");
  }
}

export function performanceReadinessCause(body: { results?: { id: string; status: string; frame?: { rows: number; values: unknown[][]; totals?: unknown[] } }[] }, ids: string[]): string | undefined {
  const results = body.results ?? [];
  if (results.length !== ids.length || new Set(results.map(result => result.id)).size !== ids.length) return "Missing or duplicate performance panels";
  for (const id of ids) {
    const result = results.find(result => result.id === id);
    if (result?.status !== "ok" || !result.frame?.rows) return `Performance panel ${id} is not populated`;
    if ((id === "count" || id === "logs") && !result.frame.totals?.includes(8640)) return `Performance panel ${id} has not received all 8640 rows`;
  }
}

// Self-contained callback: Playwright serializes this into the page. No Node
// timestamps, locator assertions, scrolling, response parsing or DEV hooks.
export function allPanelsPainted(panels: { id: string; viz: string }[]): number | false {
  const state = window as typeof window & { __fanoutPerformanceReadyFrames?: number };
  const visible = (node: HTMLElement) => {
    const rect = node.getBoundingClientRect();
    if (rect.width <= 0 || rect.height <= 0 || rect.top < 0 || rect.left < 0 || rect.bottom > innerHeight || rect.right > innerWidth) return false;
    for (let ancestor: HTMLElement | null = node; ancestor; ancestor = ancestor.parentElement) {
      const style = getComputedStyle(ancestor);
      if (style.display === "none" || style.visibility !== "visible" || Number(style.opacity) === 0 || style.contentVisibility === "hidden") return false;
      const clip = ancestor.getBoundingClientRect();
      if (/(auto|scroll|hidden|clip)/.test(style.overflowX) && (rect.left < clip.left - 1 || rect.right > clip.right + 1)) return false;
      if (/(auto|scroll|hidden|clip)/.test(style.overflowY) && (rect.top < clip.top - 1 || rect.bottom > clip.bottom + 1)) return false;
    }
    return true;
  };
  const ready = document.fonts.status === "loaded" && panels.every(panel => {
    const card = document.querySelector<HTMLElement>(`.mantine-Paper-root[data-panel="${panel.id}"]`);
    if (!card || !visible(card) || card.querySelector('[aria-label="Loading panel"], [aria-label="Refreshing"], [data-panel-error], [data-refresh-error-message]')) return false;
    const selector = panel.viz === "table" ? "tbody tr" : panel.viz === "stat" ? "[data-stat-value]" : "canvas";
    const node = card.querySelector<HTMLElement>(selector);
    if (!node || !visible(node)) return false;
    if (node instanceof HTMLCanvasElement) {
      if (node.width <= 0 || node.height <= 0) return false;
      // A mounted, transparent canvas is not a painted chart. Downsample the
      // actual backing store to bound this check independently of pixel ratio.
      const probe = document.createElement("canvas");
      probe.width = probe.height = 32;
      const context = probe.getContext("2d", { willReadFrequently: true });
      if (!context) return false;
      context.drawImage(node, 0, 0, 32, 32);
      const pixels = context.getImageData(0, 0, 32, 32).data;
      if (!pixels.some((value, index) => index % 4 === 3 && value > 0)) return false;
    }
    return true;
  });
  state.__fanoutPerformanceReadyFrames = ready ? (state.__fanoutPerformanceReadyFrames ?? 0) + 1 : 0;
  return state.__fanoutPerformanceReadyFrames >= 2 ? performance.now() : false;
}
