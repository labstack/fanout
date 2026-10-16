import { expect, type Locator, type Page } from "@playwright/test";
import { mkdir } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { cardSelector, readyCard, settle, test } from "./accessibility-support";
import type { DashboardSpec } from "../../panels/types";
import type { Result, TraceDetail } from "../../contracts";
import { compareTracePages, scoreTracePage } from "./landing-trace-support";

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

test("capture the overview, service map and trace waterfall for the landing page", async ({ page, request }, info) => {
  // The normal matrix also includes 1100px. Write each stable asset only once.
  test.skip(info.project.use.viewport?.width !== 1440, "landing captures use the 1440px projects");
  const theme = info.project.use.colorScheme as "light" | "dark";
  const directory = fileURLToPath(new URL("../../../site/src/assets/landing/", import.meta.url));
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
  await capture("overview");

  const mapPanel = dashboard.spec.panels.find(panel => panel.viz === "service_map");
  if (!mapPanel) throw new Error("System overview has no service map");
  const map = await readyCard(page, mapPanel);
  await map.getByRole("button", { name: `${mapPanel.title} menu`, exact: true }).click();
  await page.getByRole("menuitem", { name: "View", exact: true }).click();
  const fullView = page.locator("[data-panel-fullscreen]");
  await expect(fullView).toBeVisible();
  const fullMap = fullView.locator(cardSelector(mapPanel.id));
  await expect(fullMap.locator('[aria-label="Loading panel"], [aria-label="Refreshing"]')).toHaveCount(0);
  await expect(fullMap.getByRole("region", { name: /service dependency graph/ }).getByRole("button").first()).toBeVisible();
  await settle(page);
  await page.waitForTimeout(250);
  const wholeGraph = fullMap.getByRole("button", { name: `Show whole ${mapPanel.title} graph`, exact: true });
  if (await wholeGraph.isVisible()) await wholeGraph.click();
  await expect(fullMap.getByRole("region", { name: /service dependency graph/ }).getByRole("button").first()).toBeVisible();
  await expect(fullMap.locator("[data-panel-title]")).toHaveText(mapPanel.title);
  await capture("service-map", fullMap, async () => {
    // Crop the settled graph itself: resizing the card refits and recentres
    // its contents, which can reintroduce the whitespace being removed.
    const clip = await fullMap.getByRole("region", { name: /service dependency graph/ }).evaluate(region => {
      const boxes = [...region.querySelectorAll("button, svg > g > path, span")]
        .map(node => node.getBoundingClientRect()).filter(box => box.width > 0 && box.height > 0);
      if (!boxes.length) throw new Error("The service graph has no rendered bounds");
      const left = Math.min(...boxes.map(box => box.left)), right = Math.max(...boxes.map(box => box.right));
      const top = Math.min(...boxes.map(box => box.top)), bottom = Math.max(...boxes.map(box => box.bottom));
      // At least 24px on every side, centred with symmetric padding; expand
      // the shorter dimension only as needed for a 16:9 landscape frame.
      const width = Math.ceil(Math.max(right - left + 48, (bottom - top + 48) * 16 / 9));
      const height = Math.ceil(width * 9 / 16);
      return { x: (left + right - width) / 2, y: (top + bottom - height) / 2, width, height };
    });
    const viewport = page.viewportSize()!;
    expect(clip.x).toBeGreaterThanOrEqual(0); expect(clip.x + clip.width).toBeLessThanOrEqual(viewport.width);
    expect(clip.y).toBeGreaterThanOrEqual(0); expect(clip.y + clip.height).toBeLessThanOrEqual(viewport.height);
    return clip;
  });

  await page.getByRole("button", { name: "Close panel view", exact: true }).click();
  await expect(fullView).toHaveCount(0);
  // The drawer needs enough vertical room for all eight visible waterfall
  // spans, pagination and the first correlated log rows.
  await page.setViewportSize({ width: 1440, height: 1200 });
  // Open a service, then one of its server-returned exemplar traces.
  const inlineMap = page.locator(cardSelector(mapPanel.id));
  const inlineWholeGraph = inlineMap.getByRole("button", { name: `Show whole ${mapPanel.title} graph`, exact: true });
  if (await inlineWholeGraph.isVisible()) await inlineWholeGraph.click();
  const drawer = page.getByRole("dialog");
  const traceRows = drawer.getByRole("button", { name: / · .* · [\d.]+ ms$/ });
  if (externalBaseURL) {
    const services = inlineMap.getByRole("region", { name: /service dependency graph/ }).getByRole("button");
    const labels = await services.evaluateAll(nodes => nodes.map(node => node.getAttribute("aria-label")!));
    // Prefer the demo's checkout path, then its frontend, before other services.
    const priority = (label: string) => /checkout/i.test(label.split(",")[0]) ? 0 : /frontend/i.test(label.split(",")[0]) ? 1 : 2;
    labels.sort((a, b) => priority(a) - priority(b) || a.localeCompare(b));
    if (!labels.length) throw new Error("The live service map has no services");
    const exemplarsReady = page.waitForResponse(response => new URL(response.url()).pathname === "/api/panels/exemplars" && response.request().method() === "POST");
    await inlineMap.getByRole("button", { name: labels[0], exact: true }).click();
    const exemplarsResponse = await exemplarsReady;
    expect(exemplarsResponse.status()).toBe(200);
    const { traces } = await exemplarsResponse.json() as { traces: { trace_id: string; namespace: string }[] };
    const selection = exemplarsResponse.request().postDataJSON() as { time: { from: string; to: string } };
    const candidates = await Promise.all(traces.map(async (trace, index) => {
      const params = new URLSearchParams({ namespace: trace.namespace, from: selection.time.from, to: selection.time.to, limit: "200" });
      const response = await request.get(`/api/traces/${encodeURIComponent(trace.trace_id)}?${params}`);
      expect(response.status()).toBe(200);
      const { data } = await response.json() as Result<TraceDetail>;
      return { index, data };
    }));
    // Rank the actual first waterfall page of complete, error-free traces.
    const ranked = candidates.flatMap(({ index, data }) => {
      const score = scoreTracePage(data);
      return score ? [{ index, score }] : [];
    }).sort((a, b) => compareTracePages(a.score, b.score));
    if (!ranked.length) throw new Error("No complete, error-free live exemplar has at least 3 services on its first waterfall page and a positive duration; send more workload before capturing");
    await expect(drawer).toBeVisible();
    await expect(traceRows).toHaveCount(traces.length);
    await traceRows.nth(ranked[0].index).click();
  } else {
    await inlineMap.getByRole("button", { name: /^checkout, / }).click();
    await expect(drawer).toBeVisible();
    await traceRows.first().click();
  }
  await expect(drawer.getByRole("button", { name: "Waterfall", exact: true })).toHaveAttribute("aria-pressed", "true");
  // Capture-only width gives the waterfall and logs a landscape frame without
  // changing the product drawer or shrinking its text to fit a portrait crop.
  await drawer.evaluate(dialog => Object.assign(dialog.style, { flex: "0 0 1200px", width: "1200px", maxWidth: "100vw" }));
  await settle(page);
  await expect(drawer.getByRole("columnheader", { name: "Timeline", exact: true })).toBeVisible();
  await expect(drawer.getByRole("img", { name: / took / }).first()).toBeVisible();
  await expect(drawer.getByRole("img", { name: / took / }).last()).toBeInViewport({ ratio: 1 });
  await expect(drawer.getByText("Correlated logs", { exact: true })).toBeVisible();
  const logs = drawer.locator("tbody tr").filter({ has: page.locator("[data-field=body]") });
  await expect(logs.first()).toBeVisible();
  const lastLog = logs.nth(Math.min(2, await logs.count() - 1));
  await expect(lastLog).toBeInViewport({ ratio: 1 });
  await expect(drawer.getByRole("alert")).toHaveCount(0);
  await capture("trace", drawer, async () => {
    const dialog = (await drawer.boundingBox())!, last = (await lastLog.boundingBox())!;
    const clip = { x: dialog.x, y: dialog.y, width: dialog.width, height: Math.ceil(last.y + last.height - dialog.y) };
    expect(clip.width).toBeGreaterThanOrEqual(clip.height);
    return clip;
  });
});
