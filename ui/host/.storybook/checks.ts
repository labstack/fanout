import { expect } from "storybook/test";

function visible(element: Element) {
  return (
    element.checkVisibility({ checkOpacity: true, checkVisibilityCSS: true }) &&
    !element.closest('[aria-hidden="true"]')
  );
}

function visuallyHidden(element: Element, root: HTMLElement) {
  for (
    let ancestor: Element | null = element;
    ancestor && root.contains(ancestor);
    ancestor = ancestor.parentElement
  ) {
    const style = getComputedStyle(ancestor);
    if (
      style.clipPath === "inset(50%)" ||
      /rect\(0px[, ]+0px[, ]+0px[, ]+0px\)/.test(style.clip) ||
      ((style.position === "absolute" || style.position === "fixed") &&
        ancestor.getBoundingClientRect().width <= 1 &&
        ancestor.getBoundingClientRect().height <= 1 &&
        /(hidden|clip)/.test(style.overflowX + style.overflowY))
    )
      return true;
  }
  return false;
}

/** Measure reachable text at the origin, or only the last visible modal. */
export function assertTextLayout(root: HTMLElement) {
  window.scrollTo(0, 0);
  const page = root;
  const modal = [...page.querySelectorAll<HTMLElement>('[aria-modal="true"]')]
    .filter(visible)
    .at(-1);
  root = modal || page;
  // Read each element's layout once per pass. Full console stories contain
  // many text nodes sharing the same clipping/scrolling ancestors.
  const layouts = new Map<Element, ReturnType<typeof measure>>();
  function measure(element: Element) {
    const style = getComputedStyle(element);
    return {
      bounds: element.getBoundingClientRect(),
      clipsX: /^(hidden|clip)$/.test(style.overflowX),
      clipsY: /^(hidden|clip)$/.test(style.overflowY),
      scrollsX:
        /^(auto|scroll)$/.test(style.overflowX) &&
        element.scrollWidth > element.clientWidth,
      scrollsY:
        /^(auto|scroll)$/.test(style.overflowY) &&
        element.scrollHeight > element.clientHeight,
      truncates:
        style.textOverflow === "ellipsis" ||
        Number.parseInt(style.webkitLineClamp, 10) > 0,
    };
  }
  function layout(element: Element) {
    let result = layouts.get(element);
    if (!result) {
      result = measure(element);
      layouts.set(element, result);
    }
    return result;
  }
  function hasFullTruncationValue(element: Element, clipper: Element) {
    const normalize = (value: string) => value.replace(/\s+/g, " ").trim();
    let fullText = "";
    const descriptions: string[] = [];
    for (
      let ancestor: Element | null = element;
      ancestor;
      ancestor = ancestor.parentElement
    ) {
      if (!fullText && layout(ancestor).truncates)
        fullText = normalize(ancestor.textContent || "");
      // Mantine 9.6 Tooltip has no persistent target marker when closed.
      for (const attribute of ["title", "aria-label", "aria-description"])
        descriptions.push(normalize(ancestor.getAttribute(attribute) || ""));
      if (ancestor === clipper) break;
    }
    return !!fullText && descriptions.some((value) => value.includes(fullText));
  }
  const textBoxes: { element: Element; rect: DOMRect }[] = [];
  const clipped: string[] = [];
  const walker = document.createTreeWalker(root, NodeFilter.SHOW_TEXT);
  for (let node = walker.nextNode(); node; node = walker.nextNode()) {
    const element = node.parentElement;
    if (
      !node.textContent?.trim() ||
      !element ||
      !visible(element) ||
      visuallyHidden(element, root) ||
      element.closest(
        "style, script, svg, .mantine-VisuallyHidden-root, .skip-link:not(:focus)",
      )
    )
      continue;
    const range = document.createRange();
    range.selectNodeContents(node);
    for (const rect of range.getClientRects()) {
      if (!rect.width || !rect.height) continue;
      let ancestor: Element | null = element;
      let exposed = rect;
      while (ancestor && root.contains(ancestor)) {
        const { bounds, scrollsX, scrollsY, clipsX, clipsY } = layout(ancestor);
        if (
          ((clipsX &&
            exposed.width > 0 &&
            (exposed.left < bounds.left - 2 ||
              exposed.right > bounds.right + 2)) ||
            (clipsY &&
              exposed.height > 0 &&
              (exposed.top < bounds.top - 2 ||
                exposed.bottom > bounds.bottom + 2))) &&
          !hasFullTruncationValue(element, ancestor)
        )
          clipped.push(node.textContent || "");
        // Only the scrolling axis is reachable. Keep checking outer clippers,
        // and compare collisions using only text exposed by this ancestor.
        const intersectX = scrollsX || clipsX,
          intersectY = scrollsY || clipsY;
        const left = intersectX
            ? Math.max(exposed.left, bounds.left)
            : exposed.left,
          top = intersectY ? Math.max(exposed.top, bounds.top) : exposed.top;
        exposed = new DOMRect(
          left,
          top,
          intersectX
            ? Math.max(0, Math.min(exposed.right, bounds.right) - left)
            : exposed.width,
          intersectY
            ? Math.max(0, Math.min(exposed.bottom, bounds.bottom) - top)
            : exposed.height,
        );
        ancestor = ancestor.parentElement;
      }
      if (exposed.width && exposed.height)
        textBoxes.push({ element, rect: exposed });
    }
  }
  expect(clipped, "Clipped text").toEqual([]);
  const collisions: string[] = [];
  // Sweep along y; later rows cannot collide. Preserve the original
  // two-pixel tolerance and containment exceptions without an all-pairs scan.
  textBoxes.sort((a, b) => a.rect.top - b.rect.top);
  for (let i = 0; i < textBoxes.length; i++)
    for (let j = i + 1; j < textBoxes.length; j++) {
      const a = textBoxes[i],
        b = textBoxes[j];
      if (b.rect.top >= a.rect.bottom - 2) break;
      if (
        a.element === b.element ||
        a.element.contains(b.element) ||
        b.element.contains(a.element)
      )
        continue;
      const overlaps =
        Math.min(a.rect.right, b.rect.right) -
          Math.max(a.rect.left, b.rect.left) >
          2 &&
        Math.min(a.rect.bottom, b.rect.bottom) -
          Math.max(a.rect.top, b.rect.top) >
          2;
      if (overlaps)
        collisions.push(`${a.element.textContent} / ${b.element.textContent}`);
    }
  expect(collisions, "Overlapping text").toEqual([]);
  expect(
    document.documentElement.scrollWidth,
    "Page must not scroll horizontally",
  ).toBeLessThanOrEqual(window.innerWidth);
}
