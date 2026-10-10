import { expect } from "@playwright/test";
import { capture, chooseView, longError, openSurface, readyCard, settle, spec, staleSurface, test, type Capture, type Scenario, type Surface } from "./accessibility-support";

// Screenshots are review artifacts, never pixel baselines or automatic visual PASS,
// so they are captured only on request: FANOUT_E2E_SCREENSHOTS=1 just e2e.
test.skip(process.env.FANOUT_E2E_SCREENSHOTS !== "1", "set FANOUT_E2E_SCREENSHOTS=1 to capture review screenshots");
for (const surface of ["dashboard", "chat"] as Surface[]) {
  const groups = surface === "dashboard" ? [spec.panels] : spec.panels.map(panel => [panel]);
  for (const panels of groups) test(`capture ${surface} ${panels.length === 1 ? panels[0].viz : "all fifteen types"} for visual review`, async ({ page, request }, info) => {
    const manifest: Capture[] = [];
    try {
      const document = await openSurface(page, request, info, surface, panels[0]);
      for (const panel of panels) {
        const card = await readyCard(document, panel);
        manifest.push(await capture(info, card, surface, panel));
      }
    } finally {
      await info.attach("review-manifest.json", { body: JSON.stringify(manifest, null, 2), contentType: "application/json" });
    }
  });
}

for (const surface of ["dashboard", "chat"] as Surface[]) for (const scenario of ["error", "empty", "truncated", "stale"] as Scenario[]) {
  test(`capture ${surface} ${scenario} and long-error reachability`, async ({ page, request }, info) => {
    const panel = spec.panels.find(p => p.viz === "logs")!;
    const document = await openSurface(page, request, info, surface, panel, scenario);
    const card = await readyCard(document, panel, scenario);
    if (scenario === "stale") await staleSurface(page, document, surface, card);
    const manifest: Capture[] = [];
    try {
      manifest.push(await capture(info, card, surface, panel, scenario, scenario === "error" || scenario === "empty" ? scenario : "ok"));
      if (scenario === "error") {
        await card.getByRole("button", { name: / menu/ }).click();
        await expect(document.getByRole("menu")).toBeVisible();
        await settle(document);
        manifest.push(await capture(info, document.locator("body"), surface, panel, "error-menu", "error"));
        await page.keyboard.press("Escape");
        await chooseView(document, card, "Data");
        const detail = card.locator("[data-panel-error-detail]");
        await expect(detail).toHaveText(longError);
        await expect(detail).toBeVisible();
        manifest.push(await capture(info, card, surface, panel, "error-Data", "error"));
        // Scroll the real inspection body to prove the end can be reached.
        await card.locator("[data-panel-body]").evaluate(el => { el.scrollTop = el.scrollHeight; });
        await settle(document);
        expect(await card.locator("[data-panel-body]").evaluate(el => el.scrollTop + el.clientHeight >= el.scrollHeight - 1)).toBe(true);
        expect(await detail.evaluate(el => {
          const iterator = el.ownerDocument.createNodeIterator(el, NodeFilter.SHOW_TEXT);
          let node: Node | null;
          while ((node = iterator.nextNode())) {
            const at = node.textContent?.indexOf("END OF ERROR DETAIL") ?? -1;
            if (at < 0) continue;
            const range = el.ownerDocument.createRange(); range.setStart(node, at); range.setEnd(node, at + "END OF ERROR DETAIL".length);
            const marker = range.getBoundingClientRect(), body = el.closest("[data-panel-body]")!.getBoundingClientRect();
            return marker.top >= body.top && marker.bottom <= body.bottom;
          }
          return false;
        }), "The terminal error text is reachable inside Data's scroll viewport").toBe(true);
        manifest.push(await capture(info, card, surface, panel, "error-Data-end", "error"));
      }
    } finally {
      await info.attach("review-manifest.json", { body: JSON.stringify(manifest, null, 2), contentType: "application/json" });
    }
  });
}
