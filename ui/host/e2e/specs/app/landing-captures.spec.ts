import { test, expect, type Page } from "../../fixtures/test";
import { mkdir, readFile, writeFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import { cardSelector, readyCard, settle } from "../../support/accessibility-support";
import conversation from "../../data/landing-chat.json" with { type: "json" };
import type { DashboardSpec, PanelResult } from "../../../../panels/types";

// Only the disposable seeded instance is used. Never capture private telemetry.
test.skip(process.env.PITOT_LANDING_CAPTURES !== "1", "use just landing-captures to refresh all six images");
// Historical full-window sizes: overview 1920×1200; dashboard/chat 1280×800.
test.use({ viewport: { width: 1280, height: 800 }, deviceScaleFactor: 1.5 });

async function exportLogo(page: Page) {
  // Export the actual BrandLockup DOM, so the mark and type proportions cannot
  // drift into a second hand-drawn identity. Use the site's display font stack.
  const brand = page.getByLabel("Pitot", { exact: true }).first();
  const lockup = await brand.evaluate(element => {
    const svg = element.querySelector("svg")!, text = element.querySelector(".mantine-Text-root")!;
    const bounds = element.getBoundingClientRect(), mark = svg.getBoundingClientRect(), word = text.getBoundingClientRect();
    const style = getComputedStyle(text);
    svg.setAttribute("width", String(mark.width)); svg.setAttribute("height", String(mark.height));
    return { mark: svg.outerHTML, width: Math.ceil(bounds.width), height: bounds.height, x: word.left - bounds.left,
      fontSize: style.fontSize, weight: style.fontWeight, tracking: style.letterSpacing, word: text.textContent?.toUpperCase() };
  });
  expect(lockup.word).toBe("PITOT");
  const css = await readFile(fileURLToPath(new URL("../../../../../site/src/styles/pitot.css", import.meta.url)), "utf8");
  const font = css.match(/--fo-font-display:\s*([^;]+);/)![1].replaceAll('"', "&quot;");
  const svg = `<svg xmlns="http://www.w3.org/2000/svg" viewBox="0 0 ${lockup.width} ${lockup.height}" role="img" aria-label="Pitot">
<title>Pitot</title>
${lockup.mark}
<text x="${lockup.x}" y="${lockup.height / 2}" dominant-baseline="central" fill="currentColor" font-family="${font}" font-size="${lockup.fontSize}" font-weight="${lockup.weight}" letter-spacing="${lockup.tracking}">PITOT</text>
</svg>
`;
  await writeFile(fileURLToPath(new URL("../../../../../site/public/logo.svg", import.meta.url)), svg);
}

test("capture the seeded store dashboard and fixture conversation", async ({ page, request, baseURL }, info) => {
  test.skip(info.project.use.viewport?.width !== 1440, "write each theme once using the 1440px projects");
  test.setTimeout(180_000);
  if (process.env.PITOT_LANDING_BASE_URL) throw new Error("Landing stills require the offline seeded instance");
  const theme = info.project.use.colorScheme as "light" | "dark";
  const directory = fileURLToPath(new URL("../../../../../site/src/assets/landing/", import.meta.url));
  await mkdir(directory, { recursive: true });
  const blocked: string[] = [];
  await page.route("**/*", async route => {
    const url = new URL(route.request().url());
    if (url.origin !== new URL(baseURL!).origin || url.pathname === "/api/agent/runs") {
      blocked.push(url.pathname); await route.abort();
    } else await route.continue();
  });
  const window = JSON.parse(process.env.PITOT_E2E_PERFORMANCE_WINDOW ?? "null") as { from: string; to: string } | null;
  if (!window) throw new Error("Global setup did not provide the seed window");
  await page.clock.setFixedTime(new Date(window.to));
  await page.addInitScript(value => localStorage.setItem("mantine-color-scheme-value", value), theme);
  const id = process.env.PITOT_E2E_LANDING_DASHBOARD_ID;
  const response = await request.get(`/api/dashboards/${id}`);
  expect(response.status()).toBe(200);
  const { spec } = await response.json() as { spec: DashboardSpec };
  const preview = await request.post("/api/queries/run", { headers: { "Pitot-Request": "1" }, data: { dashboard: spec } });
  expect(preview.status()).toBe(200);
  const { results } = await preview.json() as { results: PanelResult[] };
  const value = (id: string) => {
    const frame = results.find(result => result.id === id)!.frame!;
    const measure = frame.columns.findIndex(column => column.role === "measure");
    return Number(frame.totals?.[measure] ?? frame.values[measure][0]);
  };
  expect(value("requests")).toBeGreaterThan(38); expect(value("requests")).toBeLessThan(40);
  expect(value("errors")).toBeGreaterThan(0.005); expect(value("errors")).toBeLessThan(0.02);
  expect(value("latency")).toBeGreaterThan(45); expect(value("latency")).toBeLessThan(56);
  for (const result of results) expect(result.status, result.id).toBe("ok");

  const capture = async (view: "overview" | "dashboard" | "chat") => {
    await expect(page.getByLabel("Pitot", { exact: true }).first()).toBeVisible();
    const navigation = page.getByRole("navigation", { name: "Navigation", exact: true });
    await expect(navigation).toBeVisible();
    await expect(navigation.getByText("Dashboards", { exact: true })).toBeVisible();
    await page.mouse.move(0, 0);
    await page.evaluate(() => { if (document.activeElement instanceof HTMLElement) document.activeElement.blur(); });
    await settle(page);
    await page.waitForTimeout(250);
    await page.screenshot({ path: `${directory}${view}-${theme}.png`, fullPage: false, animations: "disabled",
      scale: view === "overview" ? "device" : "css",
      style: "*:focus, *:focus-visible { outline: none !important; } [role=tooltip] { visibility: hidden !important; }" });
  };

  // The local server has no AI provider. Only saved-thread reads and the
  // capability flag are fixtures; the app, telemetry queries and MCP UI are real.
  await page.route("**/api/auth/status", async route => {
    const response = await route.fetch();
    await route.fulfill({ response, json: { ...await response.json(), agent_available: true } });
  });
  await page.route(/\/api\/agent\/threads(?:\/[^?]+)?(?:\?.*)?$/, async route => {
    expect(route.request().method()).toBe("GET");
    const path = new URL(route.request().url()).pathname;
    await route.fulfill({ json: path.endsWith("/threads")
      ? { items: [{ id: conversation.id, title: conversation.title, created_at: window.to, updated_at: window.to }], next_cursor: null }
      : conversation });
  });
  await page.goto(`/dashboards/${id}`);
  await expect(page.locator("html")).toHaveAttribute("data-mantine-color-scheme", theme);
  await expect(page.getByRole("heading", { name: "Store overview - last hour", exact: true })).toBeVisible();
  for (const panel of spec.panels) await readyCard(page, panel);
  await page.evaluate(() => globalThis.scrollTo(0, 0));
  for (const panel of spec.panels) await expect(page.locator(cardSelector(panel.id)).locator('[aria-label="Loading panel"], [aria-label="Refreshing"], [data-panel-error]')).toHaveCount(0);
  await expect(page.locator(cardSelector("errors")).getByText("Healthy", { exact: true })).toBeVisible();
  // The README hero must show every plot, not only its below-fold heading.
  for (const panel of spec.panels) await expect(page.locator(cardSelector(panel.id))).toBeInViewport({ ratio: 1 });
  await capture("overview");
  await capture("dashboard");
  if (theme === "light") await exportLogo(page);

  await page.goto(`/chat/${conversation.id}`);
  await expect(page.getByRole("log", { name: "Conversation" })).toContainText("Accounting is the slowest at p95 53.8 ms");
  const frame = page.frameLocator('iframe[title="Pitot analysis view"]');
  await expect(frame.getByText("Service latency", { exact: true })).toBeVisible();
  await expect(frame.locator("tbody tr")).toHaveCount(5);
  await expect(frame.locator('[data-panel-error], .mantine-Loader-root')).toHaveCount(0);
  await frame.locator("body").evaluate(async () => { await document.fonts.ready; });
  await settle(page);
  await expect(page.getByRole("log", { name: "Conversation" }).getByText(conversation.messages[0].content as string, { exact: true })).toBeInViewport({ ratio: 1 });
  await expect(page.getByRole("button", { name: "Jump to latest", exact: true })).toBeHidden();
  await expect(page.locator('[data-answer-position]')).toBeVisible();
  await expect.poll(() => page.locator('[data-answer-position]').evaluate(answer => {
    const pane = answer.closest(".chat-scroll")!;
    return answer.getBoundingClientRect().bottom <= pane.getBoundingClientRect().bottom + 1;
  })).toBe(true);
  await capture("chat");
  expect(blocked, "No external requests or model runs may occur during captures").toEqual([]);
});
