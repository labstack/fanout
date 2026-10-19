import { test, expect, type Locator, type Page } from "../../fixtures/test";
import { mkdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { cardSelector, readyCard, settle } from "../../support/accessibility-support";
import type { DashboardSpec } from "../../../../panels/types";

// Use either the real seeded server or an existing instance, without intercepts.
const externalBaseURL = process.env.FANOUT_LANDING_BASE_URL;
test.skip(process.env.FANOUT_LANDING_CAPTURES !== "1", "set FANOUT_LANDING_CAPTURES=1 to refresh landing assets");
test.use({ viewport: { width: 1440, height: 900 }, deviceScaleFactor: 2 });
if (externalBaseURL) test.use({
  baseURL: externalBaseURL,
  storageState: async ({}, use) => {
    const cookie = process.env.FANOUT_LANDING_SESSION_COOKIE ?? "";
    const match = /^([!#$%&'*+.^_`|~\w-]+)=([^;\s]+)$/.exec(cookie);
    if (!match) throw new Error("FANOUT_LANDING_SESSION_COOKIE must be a nonempty name=value session cookie");
    const url = new URL(externalBaseURL);
    await use({ cookies: [{
      name: match[1], value: match[2], domain: url.hostname, path: "/",
      expires: -1, httpOnly: true, secure: url.protocol === "https:", sameSite: "Lax",
    }], origins: [] });
  },
});

async function readyOverviewViewport(page: Page) {
  // Recompute intersections on each poll without scrolling lazy panels into view.
  await expect.poll(() => page.locator(".mantine-Paper-root[data-panel]").evaluateAll(cards => cards
    .filter(card => {
      const bounds = card.getBoundingClientRect();
      return bounds.width > 0 && bounds.height > 0
        && bounds.left < window.innerWidth && bounds.right > 0
        && bounds.top < window.innerHeight && bounds.bottom > 0;
    })
    .filter(card => card.querySelector('[aria-label="Loading panel"], [aria-label="Refreshing"], [data-panel-error], [aria-label$=", refresh failed"]'))
    .map(card => card.getAttribute("data-panel"))), {
    message: "Every panel intersecting the overview viewport must finish loading without errors",
  }).toEqual([]);
}

test("capture the README overview", async ({ page, request }, info) => {
  // The normal matrix also includes 1100px. Write each stable asset only once.
  test.skip(info.project.use.viewport?.width !== 1440, "landing captures use the 1440px projects");
  const theme = info.project.use.colorScheme as "light" | "dark";
  const directory = fileURLToPath(new URL("../../../../../site/src/assets/landing/", import.meta.url));
  await mkdir(directory, { recursive: true });
  const capture = async (view: string, element?: Locator, clip?: () => Promise<{ x: number; y: number; width: number; height: number }>) => {
    await page.mouse.move(0, 0);
    await page.evaluate(() => {
      if (document.activeElement instanceof HTMLElement) document.activeElement.blur();
    });
    await settle(page);
    // Match the existing review captures' wait for chart compiler animations.
    await page.waitForTimeout(250);
    if (view === "overview") await readyOverviewViewport(page);
    else await expect((element ?? page).locator('[aria-label="Loading panel"], [aria-label="Refreshing"], [aria-label="Loading drill data"], [data-panel-error]')).toHaveCount(0);
    const options = {
      path: `${directory}${view}-${theme}.png`, animations: "disabled" as const, scale: "device" as const,
      // Capture-only styling: keep keyboard focus indicators in the product.
      style: "*:focus, *:focus-visible { outline: none !important; } [role=tooltip] { visibility: hidden !important; }",
    };
    if (clip) await page.screenshot({ ...options, clip: await clip() });
    else if (element) await element.screenshot(options);
    else await page.screenshot({ ...options, fullPage: false });
  };

  const seedWindow = externalBaseURL ? null : JSON.parse(process.env.FANOUT_E2E_PERFORMANCE_WINDOW ?? "null") as { to: string } | null;
  if (!externalBaseURL) {
    if (!seedWindow) throw new Error("Global setup did not provide the seed window");
    await page.clock.setFixedTime(new Date(seedWindow.to));
  }
  await page.addInitScript(value => localStorage.setItem("mantine-color-scheme-value", value), theme);
  await page.goto("/");
  await expect(page).toHaveURL(/\/dashboards\/[^/?]+/);
  const dashboardURL = new URL(page.url());
  if (seedWindow) {
    dashboardURL.searchParams.set("from", new Date(Date.parse(seedWindow.to) - 3_600_000).toISOString());
    dashboardURL.searchParams.set("to", seedWindow.to);
    await page.goto(dashboardURL.pathname + dashboardURL.search);
  }
  await expect(page.locator("html")).toHaveAttribute("data-mantine-color-scheme", theme);
  await expect(page.getByText("System overview", { exact: true }).first()).toBeVisible();
  await expect(page.getByRole("button", { name: `Switch to ${theme === "light" ? "dark" : "light"} theme` })).toBeVisible();

  const response = await request.get(`/api${dashboardURL.pathname}`);
  expect(response.status()).toBe(200);
  const dashboard = await response.json() as { is_default: boolean; spec: DashboardSpec };
  expect(dashboard.is_default).toBe(true);
  // Saved panels load when visible. Only panels in the shot gate the overview;
  // empty results are valid, and offscreen panels are left alone.
  await page.evaluate(() => window.scrollTo(0, 0));
  const firstFold = [
    ...dashboard.spec.panels.filter(panel => panel.viz === "stat"),
    ...dashboard.spec.panels.filter(panel => panel.viz === "timeseries").slice(0, 2),
  ];
  for (const panel of firstFold) await expect(page.locator(cardSelector(panel.id))).toBeInViewport({ ratio: 1 });
  const overview = page.getByRole("main", { name: "Dashboard", exact: true });
  await capture("overview", overview, async () => {
    const clip = await overview.evaluate(main => {
      const bounds = main.getBoundingClientRect();
      const x = Math.ceil(Math.max(0, bounds.left)), y = Math.ceil(Math.max(0, bounds.top));
      return { x, y, width: Math.floor(Math.min(window.innerWidth, bounds.right)) - x,
        height: Math.floor(Math.min(window.innerHeight, bounds.bottom)) - y };
    });
    expect(clip.width).toBeGreaterThanOrEqual(clip.height);
    expect(clip.height).toBeGreaterThan(0);
    return clip;
  });
});
