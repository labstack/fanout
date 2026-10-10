import AxeBuilder from "@axe-core/playwright";
import axe from "axe-core";
import { expect, type Frame, type Locator, type Page, type TestInfo } from "@playwright/test";
import { cardSelector, chooseView, longError, openSurface, readyCard, settle, spec, staleSurface, test, type Scenario, type Surface } from "./accessibility-support";

const tags = ["wcag2a", "wcag2aa", "wcag21a", "wcag21aa"];
// Contrast and ARIA follow the theme, not these two desktop widths, so this spec
// runs at the narrower one; screenshots.spec.ts reviews both widths.
test.skip(({ viewport }) => viewport?.width !== 1100, "Accessibility runs once per theme at the narrower width");
async function scan(surface: Page | Frame, info: TestInfo, label: string, selector?: string) {
  let report: axe.AxeResults;
  await settle(surface);
  if ("mainFrame" in surface) {
    const builder = new AxeBuilder({ page: surface }).withTags(tags);
    if (selector) builder.include(selector);
    report = await builder.analyze();
  } else {
    // Playwright's test execution injects directly into the opaque-origin frame.
    // DOM script insertion would be blocked by product CSP. No CSP/sandbox changes.
    await surface.evaluate(axe.source);
    report = await surface.evaluate(async ({ tags, selector }) => {
      const api = (window as typeof window & { axe: typeof axe }).axe;
      return api.run(selector ? document.querySelector(selector)! : document, { runOnly: { type: "tag", values: tags } });
    }, { tags, selector });
  }
  await info.attach(`${label}-axe.json`, { body: JSON.stringify(report, null, 2), contentType: "application/json" });
  expect.soft(report.violations, `${label}: WCAG AA violations`).toEqual([]);
  const contrast = report.incomplete.filter(item => item.id === "color-contrast");
  const proven = await proveGlyphs(surface, contrast.flatMap(item => item.nodes));
  if (proven.length) await info.attach(`${label}-glyph-contrast.json`, { body: JSON.stringify(proven, null, 2), contentType: "application/json" });
  const passed = new Set(proven.filter(glyph => glyph.ratio !== null && glyph.ratio >= 3).map(glyph => glyph.target));
  const unresolved = contrast.map(item => ({ ...item, nodes: item.nodes.filter(node => !passed.has(JSON.stringify(node.target))) })).filter(item => item.nodes.length > 0);
  expect.soft(unresolved, `${label}: unresolved contrast requires computed-style proof or controller review`).toEqual([]);
}

// WCAG 1.4.11: a symbol-only status glyph is a shape marker beside a label that
// carries the same meaning, so it needs 3:1. axe never measures symbol-only text
// (nonBmp); measure it with axe's own colour code. Undetermined stays unresolved.
async function proveGlyphs(surface: Page | Frame, nodes: axe.NodeResult[]) {
  const glyphs = nodes.filter(node => node.all.length === 0 && node.none.length === 0 && node.any.length > 0
    && node.any.every(check => (check.data as { messageKey?: string } | null)?.messageKey === "nonBmp")
    && node.target.length === 1 && typeof node.target[0] === "string").map(node => node.target[0] as string);
  if (glyphs.length === 0) return [];
  if (!await surface.evaluate(() => "axe" in window)) await surface.evaluate(axe.source);
  return surface.evaluate(selectors => {
    const api = (window as typeof window & { axe: typeof axe }).axe;
    const color = (api.commons as unknown as { color: {
      getBackgroundColor(node: Element, bgNodes: Element[], shadowOutlineEmMax: number): { toHexString(): string } | null;
      getForegroundColor(node: Element, noScroll: boolean, bgColor: unknown): { toHexString(): string } | null;
      getContrast(bg: unknown, fg: unknown): number;
    } }).color;
    api.setup(document);
    try {
      return selectors.map(selector => {
        const node = document.querySelector(selector);
        const bg = node && color.getBackgroundColor(node, [], 0.1);
        const fg = node && bg && color.getForegroundColor(node, false, bg);
        return { target: JSON.stringify([selector]), glyph: node?.textContent ?? null, fg: fg?.toHexString() ?? null, bg: bg?.toHexString() ?? null, ratio: bg && fg ? color.getContrast(bg, fg) : null };
      });
    } finally { api.teardown(); }
  }, glyphs);
}

test("dashboard controls satisfy WCAG AA", async ({ page, request }, info) => {
  await openSurface(page, request, info, "dashboard");
  await expect(page.getByRole("group", { name: "Dashboard controls" })).toBeVisible();
  await settle(page);
  await scan(page, info, "dashboard-controls");
  await page.getByRole("button", { name: "History", exact: true }).click();
  await expect(page.locator("[data-version-history]")).toBeVisible();
  await expect(page.locator("[data-version-history]").getByText("Loading history…")).toHaveCount(0);
  await expect(page.locator("[data-version-history]").getByRole("button", { name: "Spec", exact: true })).toBeVisible();
  for (const name of ["Spec", "Data"]) {
    await page.locator("[data-version-history]").getByRole("button", { name, exact: true }).click();
    await settle(page);
    await scan(page, info, `history-${name}`, '[role="dialog"]');
  }
});

// One panel per renderer takes the menu and full-screen states: ECharts canvas,
// data table, log rows, DOM graph, DOM metrics, and markdown, whose menu also
// holds the view radio group. The other types draw through these same renderers.
const families = ["timeseries", "table", "logs", "service_map", "health", "text"];

for (const surface of ["dashboard", "chat"] as Surface[]) {
  test(`${surface} panels satisfy WCAG AA in chart, inspection, menu and full-screen states`, async ({ page, request }, info) => {
    // All fifteen types share one mounted surface and one scan per view, rather
    // than a navigation and a scan per card. Chat renders one real fragment of them.
    const document = await openSurface(page, request, info, surface);
    const cards = new Map<string, Locator>();
    for (const panel of spec.panels) cards.set(panel.id, await readyCard(document, panel));
    await scan(document, info, `${surface}-chart`);
    for (const mode of ["Data", "Spec"] as const) {
      for (const panel of spec.panels) if (mode === "Spec" || panel.viz !== "text") await chooseView(document, cards.get(panel.id)!, mode);
      await scan(document, info, `${surface}-${mode}`);
    }
    for (const panel of spec.panels) await chooseView(document, cards.get(panel.id)!, "Chart");
    for (const panel of spec.panels.filter(p => families.includes(p.viz))) {
      const card = cards.get(panel.id)!;
      await card.getByRole("button", { name: / menu/ }).click();
      await expect(document.getByRole("menu")).toBeVisible();
      await scan(document, info, `${surface}-${panel.viz}-menu`, '[role="menu"]');
      await document.getByRole("menuitem", { name: "View", exact: true }).click();
      const fullscreen = document.locator(surface === "chat" ? "[data-fragment-fullscreen]" : "[data-panel-fullscreen]");
      await expect(fullscreen).toBeVisible();
      await scan(document, info, `${surface}-${panel.viz}-fullscreen`, surface === "chat" ? "[data-fragment-fullscreen]" : "[data-panel-fullscreen]");
      if (surface === "dashboard") await document.getByRole("button", { name: "Close panel view", exact: true }).click();
      else await page.keyboard.press("Escape");
      await expect(fullscreen).toBeHidden();
    }
  });
}

for (const surface of ["dashboard", "chat"] as Surface[]) for (const scenario of ["error", "empty", "truncated", "stale"] as Scenario[]) {
  test(`${surface} ${scenario} state remains accessible`, async ({ page, request }, info) => {
    const panel = spec.panels.find(p => p.viz === "logs")!;
    const document = await openSurface(page, request, info, surface, panel, scenario);
    const card = await readyCard(document, panel, scenario);
    if (scenario === "stale") await staleSurface(page, document, surface, card);
    await scan(document, info, `${surface}-${scenario}`);
    if (scenario === "error") {
      await card.getByRole("button", { name: / menu/ }).click();
      await expect(document.getByRole("menu")).toBeVisible();
      await scan(document, info, `${surface}-error-menu`, '[role="menu"]');
      await page.keyboard.press("Escape");
      await chooseView(document, card, "Data");
      await expect(card.locator("[data-panel-error-detail]")).toHaveText(longError);
      await expect(card.locator("[data-panel-error-detail]")).toBeVisible();
      await scan(document, info, `${surface}-error-Data`, cardSelector(panel.id));
    }
  });
}
