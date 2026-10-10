import { expect } from "@playwright/test";
import { chooseView, openSurface, readyCard, spec, test, type Surface, type Scenario } from "./accessibility-support";
import type { Frame, Page } from "@playwright/test";

// An opaque descendant must not paint square corners over an unclipped rounded
// container, such as a panel body or notes footer over its card's bottom corners.
async function cornerIssues(target: Page | Frame): Promise<string[]> {
  return target.evaluate(() => {
    const out = new Set<string>();
    const name = (e: Element) => e.tagName.toLowerCase() + (e.getAttribute("data-panel") ? `[data-panel=${e.getAttribute("data-panel")}]` : "") + (e.getAttribute("data-panel-body") !== null ? "[data-panel-body]" : "") + "." + String(e.className).split(" ").slice(0, 2).join(".");
    for (const el of Array.from(document.querySelectorAll("*"))) {
      const cs = getComputedStyle(el);
      if (cs.overflowX !== "visible" || cs.overflowY !== "visible") continue;
      const box = el.getBoundingClientRect();
      if (box.width < 8 || box.height < 8) continue;
      const bw = parseFloat(cs.borderLeftWidth) || 0;
      const corners: [string, number, number, number][] = [
        ["top-left", parseFloat(cs.borderTopLeftRadius) || 0, box.left + bw + 1, box.top + bw + 1],
        ["top-right", parseFloat(cs.borderTopRightRadius) || 0, box.right - bw - 1, box.top + bw + 1],
        ["bottom-left", parseFloat(cs.borderBottomLeftRadius) || 0, box.left + bw + 1, box.bottom - bw - 1],
        ["bottom-right", parseFloat(cs.borderBottomRightRadius) || 0, box.right - bw - 1, box.bottom - bw - 1],
      ];
      if (corners.every(c => c[1] < 3)) continue;
      for (const d of Array.from(el.querySelectorAll("*"))) {
        const ds = getComputedStyle(d);
        const bg = ds.backgroundColor;
        const m = bg.match(/rgba?\(([^)]+)\)/);
        if (!m) continue;
        const parts = m[1].split(",").map(s => parseFloat(s));
        if (parts.length === 4 && parts[3] < 0.5) continue;
        const b = d.getBoundingClientRect();
        if (!b.width || !b.height) continue;
        const own: Record<string, number> = { "top-left": parseFloat(ds.borderTopLeftRadius) || 0, "top-right": parseFloat(ds.borderTopRightRadius) || 0, "bottom-left": parseFloat(ds.borderBottomLeftRadius) || 0, "bottom-right": parseFloat(ds.borderBottomRightRadius) || 0 };
        // Skip if an intermediate ancestor clips the descendant.
        let clipped = false;
        for (let a = d.parentElement; a && a !== el; a = a.parentElement) { const as = getComputedStyle(a); if (as.overflowX !== "visible" || as.overflowY !== "visible") { clipped = true; break; } }
        if (clipped) continue;
        for (const [corner, r, x, y] of corners) {
          if (r < 3) continue;
          if (x >= b.left && x <= b.right && y >= b.top && y <= b.bottom && own[corner] < r - bw - 1) out.add(`${name(el)} ${corner} r=${r} covered by ${name(d)} bg=${bg}`);
        }
      }
    }
    return [...out];
  });
}

for (const surface of ["dashboard", "chat"] as Surface[]) for (const scenario of [undefined] as (Scenario | undefined)[]) {
  test(`${surface} panels keep their rounded corners in every view`, async ({ page, request }, info) => {
    // Geometry does not depend on theme or width; one project keeps CI short.
    test.skip(info.project.name !== "1100-light", "checked once, at 1100-light");
    const panel = spec.panels.find(p => p.viz === "logs")!;
    const document = await openSurface(page, request, info, surface, scenario ? panel : undefined, scenario);
    const panels = scenario ? [panel] : spec.panels;
    const issues: string[] = [];
    for (const p of panels) {
      const card = await readyCard(document, p, scenario);
      for (const mode of (p.viz === "text" ? ["Chart", "Spec"] : ["Chart", "Data", "Spec"]) as ("Chart" | "Data" | "Spec")[]) {
        await chooseView(document, card, mode);
        for (const issue of await cornerIssues(document)) issues.push(`${p.id}/${mode}: ${issue}`);
      }
      await chooseView(document, card, "Chart");
    }
    for (const issue of await cornerIssues(page)) issues.push(`page: ${issue}`);
    await info.attach("corner-issues.json", { body: JSON.stringify([...new Set(issues)], null, 2), contentType: "application/json" });
    expect(issues).toEqual([]);
  });
}
