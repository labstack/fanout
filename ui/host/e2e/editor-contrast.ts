import type axe from "axe-core";

export function editorOverlapTargets(nodes: axe.NodeResult[]) {
  return nodes.filter(node => node.all.length === 0 && node.none.length === 0 && node.any.length > 0
    && node.any.every(check => (check.data as { messageKey?: string } | null)?.messageKey === "elmPartiallyObscuring")
    && node.target.length === 1 && typeof node.target[0] === "string").map(node => node.target[0] as string);
}

// Runs in the page. Compose CodeMirror's line/token colors over its opaque
// surface, or the validation list's translucent alert over the opaque dialog.
// axe compares whole element stacks at each wrapped text rect before resolving
// colors, so different dashboard elements behind the dialog can make a list
// item incomplete too. Validation text must also pass hit testing: an actual
// tooltip covering it must stay unresolved. Unknown effects/selections do too.
export function proveEditorText(selectors: string[]) {
  type Color = { alpha: number; parseString(value: string): void; toHexString(): string };
  const color = ((window as typeof window & { axe: typeof axe }).axe.commons as unknown as { color: {
    Color: new () => Color;
    flattenColors(fg: Color, bg: Color): Color;
    getContrast(bg: Color, fg: Color): number;
  } }).color;
  const parse = (value: string) => { const result = new color.Color(); result.parseString(value); return result; };
  return selectors.map(selector => {
    const target = JSON.stringify([selector]);
    const node = document.querySelector(selector);
    const editor = node?.closest("[data-sql-editor] .cm-editor");
    const problem = node?.matches("[data-panel-sql-editor] li.panel-sql-problem") ? node : null;
    const surface = editor ?? problem?.closest('[role="dialog"]');
    const samples: { text: string; fg: string | null; bg: string | null; ratio: number | null }[] = [];
    const unresolved = () => ({ target, samples, ratio: null });
    if (!node || !surface) return unresolved();
    const selection = window.getSelection();
    if (selection && !selection.isCollapsed && Array.from({ length: selection.rangeCount }, (_, i) => selection.getRangeAt(i)).some(range => range.intersectsNode(editor ?? problem!))) return unresolved();
    // Group opacity, images and blending need a different proof, even when an
    // opaque descendant would hide their background colors.
    for (let parent: Element | null = node; parent; parent = parent.parentElement) {
      const style = getComputedStyle(parent);
      if (Number(style.opacity || "1") !== 1 || (style.backgroundImage && style.backgroundImage !== "none")
        || (style.filter && style.filter !== "none") || (style.mixBlendMode && style.mixBlendMode !== "normal")) return unresolved();
    }
    const base = parse(getComputedStyle(surface).backgroundColor);
    if (base.alpha !== 1) return unresolved();
    const walker = document.createTreeWalker(node, NodeFilter.SHOW_TEXT);
    for (let text = walker.nextNode(); text; text = walker.nextNode()) {
      if (!text.textContent?.trim()) continue;
      const element = text.parentElement!;
      if (editor && !element.closest(".cm-line, .cm-gutterElement")) return unresolved();
      if (problem) {
        const range = document.createRange(); range.selectNodeContents(text);
        const rects = Array.from(range.getClientRects()).filter(rect => rect.width > 0 && rect.height > 0);
        if (!rects.length) return unresolved();
        for (const rect of rects) {
          // Test both ends and the middle of every wrapped run; don't prove
          // clipped/offscreen text or text underneath another surface.
          for (const x of [rect.left + Math.min(1, rect.width / 2), rect.left + rect.width / 2, rect.right - Math.min(1, rect.width / 2)]) {
            const y = rect.top + rect.height / 2;
            if (x < 0 || x >= window.innerWidth || y < 0 || y >= window.innerHeight) return unresolved();
            const hit = document.elementFromPoint(x, y);
            if (!hit || !problem.contains(hit)) return unresolved();
          }
        }
      }
      const layers: CSSStyleDeclaration[] = [];
      for (let parent: Element | null = element; parent && parent !== surface; parent = parent.parentElement) {
        const style = getComputedStyle(parent);
        if (Number(style.opacity || "1") !== 1 || (style.backgroundImage && style.backgroundImage !== "none")
          || (style.filter && style.filter !== "none") || (style.mixBlendMode && style.mixBlendMode !== "normal")) return unresolved();
        layers.unshift(style);
      }
      const bg = layers.reduce((background, style) => color.flattenColors(parse(style.backgroundColor), background), base);
      const fg = color.flattenColors(parse(getComputedStyle(element).color), bg);
      samples.push({ text: text.textContent, fg: fg.toHexString(), bg: bg.toHexString(), ratio: color.getContrast(bg, fg) });
    }
    return { target, samples, ratio: samples.length ? Math.min(...samples.map(sample => sample.ratio!)) : null };
  });
}
