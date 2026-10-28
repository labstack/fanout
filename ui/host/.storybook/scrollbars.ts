/** The part of a text box left after the clipping of its ancestors up to
 * the scroll area: a truncated title's range reaches past its ellipsis. */
function shown(r: DOMRect, from: Element, area: Element) {
  let { left, right, top, bottom } = r;
  for (let el: Element | null = from; el && el !== area; el = el.parentElement) {
    const style = getComputedStyle(el);
    if (style.overflowX === "visible" && style.overflowY === "visible") continue;
    if (el.classList.contains("mantine-ScrollArea-viewport")) continue;
    const c = el.getBoundingClientRect();
    left = Math.max(left, c.left); right = Math.min(right, c.right);
    top = Math.max(top, c.top); bottom = Math.min(bottom, c.bottom);
  }
  return { left, right, top, bottom };
}

export function assertScrollbarsClear(root: HTMLElement) {
  const covered: string[] = [];
  for (const bar of root.querySelectorAll<HTMLElement>('.mantine-ScrollArea-scrollbar[data-orientation="vertical"]')) {
    const b = bar.getBoundingClientRect();
    if (b.width === 0 || b.height === 0 || !bar.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true })) continue;
    const area = bar.closest(".mantine-ScrollArea-root");
    if (!area) continue;
    const box = area.closest("article, section, [role=dialog], aside");
    const where = box?.getAttribute("aria-label") ?? box?.querySelector("h1, h2, h3, h4")?.textContent ?? "a scroll area";
    // A wide table scrolls sideways: the columns under the bar come into
    // view when it is scrolled, so measure it at its end, where the offset
    // must leave the last column clear.
    const viewport = area.querySelector<HTMLElement>(".mantine-ScrollArea-viewport");
    const left = viewport?.scrollLeft ?? 0;
    if (viewport) viewport.scrollLeft = viewport.scrollWidth;
    const walker = document.createTreeWalker(area, NodeFilter.SHOW_TEXT);
    for (let node = walker.nextNode(); node; node = walker.nextNode()) {
      const text = node.textContent?.trim();
      if (!text || !node.parentElement) continue;
      // Text for screen readers only sits in a 1 px box nobody sees.
      const own = node.parentElement.getBoundingClientRect();
      if (own.width <= 1 || own.height <= 1) continue;
      const range = document.createRange();
      range.selectNodeContents(node);
      for (const rect of range.getClientRects()) {
        const r = shown(rect, node.parentElement, area);
        if (r.right <= r.left || r.bottom <= r.top || r.bottom <= b.top || r.top >= b.bottom) continue;
        if (r.right > b.left && r.left < b.right) { covered.push(`"${text.slice(0, 40)}" in ${where}`); break; }
      }
    }
    if (viewport) viewport.scrollLeft = left;
  }
  if (covered.length) throw new Error(`Text under a scrollbar: ${covered.join("; ")}`);
}
