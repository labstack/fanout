import { test as base, expect, type Locator, type Page } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";
import spec from "./fixtures/all-panels.json" with { type: "json" };
import { inside, overlaps, type Rect } from "./geometry";
import { typeScale } from "../../tokens";

type ObservedResult = { id: string; status: string; diagnosis?: string };
const root = fileURLToPath(new URL("../../../", import.meta.url));
const test = base.extend<{ faults: string[] }>({
  baseURL: async ({}, use) => { await use(process.env.FANOUT_E2E_BASE_URL); },
  storageState: async ({}, use) => {
    const state = process.env.FANOUT_E2E_STORAGE_STATE;
    if (!state) throw new Error("Global setup did not provide a session");
    await use(state);
  },
  faults: [async ({ page }, use) => {
    const faults: string[] = [];
    page.on("pageerror", error => faults.push(`pageerror: ${error.message}`));
    page.on("console", message => {
      if (message.type() === "error" || message.type() === "warning") faults.push(`${message.type()}: ${message.text()}`);
    });
    await use(faults);
    expect(faults, "No page errors, console errors or warnings (including iframe)").toEqual([]);
  }, { auto: true }],
});

const expectedElements: Record<string, string> = {
  stat: "[data-stat-value]", gauge: "canvas", timeseries: "canvas", bar: "canvas",
  table: "tbody tr", text: "[data-panel-body] p", heatmap: "canvas", histogram: "canvas",
  scatter: "canvas", state_timeline: "canvas", logs: "tbody tr [data-field=body]",
  log_patterns: "tbody tr [data-field=body_template]", traces: "tbody tr [data-trace-id]",
  service_map: '[role=region][aria-label*="service dependency graph"] button', health: "[data-health-tiles]",
};

async function rendered(card: Locator, viz: string, result?: ObservedResult) {
  await expect(card.locator('[aria-label="Loading panel"], [aria-label="Refreshing"]')).toHaveCount(0);
  await expect(card.locator("[data-panel-error]")).toHaveCount(0);
  if (result?.status === "empty") {
    expect(result.diagnosis, "Empty panels must give an authored diagnosis").toBeTruthy();
    await expect(card.locator("[data-panel-body]")).toContainText(result.diagnosis!);
  } else {
    await expect(card.locator(expectedElements[viz]).first(), `Expected ${viz} element`).toBeVisible();
  }
}

async function bounds(locator: Locator): Promise<Rect> {
  const rect = await locator.boundingBox();
  expect(rect, "Element has measurable geometry").not.toBeNull();
  expect(rect!.width).toBeGreaterThan(0);
  expect(rect!.height).toBeGreaterThan(0);
  return rect!;
}

async function cardGeometry(card: Locator) {
  const cardBox = await bounds(card);
  // The view group lives in the menu in compact/text cards; only visible chrome participates.
  const chrome = card.locator('[data-panel-title], [data-panel-subtitle], [role=group][aria-label$=" view"], [role=status], button[aria-label$=" menu"]');
  const boxes: { label: string; rect: Rect }[] = [];
  for (const element of await chrome.all()) {
    if (!await element.isVisible()) continue;
    // Notes have role=status too, but are below the body rather than header chrome.
    if (await element.evaluate(el => Boolean(el.closest("[data-panel-notes]")))) continue;
    const rect = await bounds(element);
    expect(inside(rect, cardBox), "Header chrome inside card").toBe(true);
    boxes.push({ label: await element.getAttribute("aria-label") ?? await element.textContent() ?? "chrome", rect });
  }
  for (let i = 0; i < boxes.length; i++) for (let j = i + 1; j < boxes.length; j++) {
    expect(overlaps(boxes[i].rect, boxes[j].rect), `${boxes[i].label} overlaps ${boxes[j].label}`).toBe(false);
  }
  const body = await bounds(card.locator("[data-panel-body]"));
  expect(inside(body, cardBox), "Panel body inside card").toBe(true);
  for (const { rect } of boxes) expect(body.y + 1, "Body below header").toBeGreaterThanOrEqual(rect.y + rect.height);
}

async function mapGeometry(card: Locator) {
  const region = card.locator('[role=region][aria-label*="service dependency graph"]');
  const viewport = await bounds(region.locator(":scope > div").first());
  const nodes = region.locator("button");
  await expect.poll(() => nodes.count()).toBeGreaterThan(0);
  const boxes: Rect[] = [];
  for (const node of await nodes.all()) {
    const rect = await bounds(node);
    expect(inside(rect, viewport), `${await node.getAttribute("aria-label")} clipped by map viewport`).toBe(true);
    for (const other of boxes) expect(overlaps(rect, other), "Service nodes overlap").toBe(false);
    boxes.push(rect);
    const fontSize = await node.locator(":scope > span:first-child > span:last-child").evaluate(el => parseFloat(getComputedStyle(el).fontSize));
    expect(fontSize, "Service name font meets micro floor").toBeGreaterThanOrEqual(typeScale.micro);
  }
}

test("all fifteen dashboard panels settle with valid geometry", async ({ page }) => {
  const results = new Map<string, ObservedResult>();
  page.on("response", async response => {
    if (new URL(response.url()).pathname !== "/api/panels/query") return;
    try {
      const body = await response.json();
      for (const result of body.results ?? []) results.set(result.id, result);
    } catch { /* An aborted response cannot establish a terminal state. */ }
  });
  // Mantine persists its own preference; force the exact project theme before the SPA mounts.
  await page.addInitScript(theme => localStorage.setItem("mantine-color-scheme-value", theme), test.info().project.use.colorScheme as "light" | "dark");
  await page.goto(`/dashboards/${process.env.FANOUT_E2E_DASHBOARD_ID}`);
  await expect(page.locator("html")).toHaveAttribute("data-mantine-color-scheme", test.info().project.use.colorScheme!);
  const cards: Rect[] = [];
  for (const panel of spec.panels) {
    const card = page.locator(`.mantine-Paper-root[data-panel="${panel.id}"]`);
    await card.scrollIntoViewIfNeeded();
    if (panel.viz !== "text") {
      await expect.poll(() => results.get(panel.id)?.status, { timeout: 60_000 }).toMatch(/^(ok|empty)$/);
    }
    await rendered(card, panel.viz, results.get(panel.id));
    await cardGeometry(card);
    if (panel.viz === "service_map") await mapGeometry(card);
  }
  await expect(page.locator('[aria-label="Loading panel"], [aria-label="Refreshing"]')).toHaveCount(0);
  // Compare every card in one coordinate system after lazy content has settled.
  for (const panel of spec.panels) cards.push(await bounds(page.locator(`.mantine-Paper-root[data-panel="${panel.id}"]`)));
  for (let i = 0; i < cards.length; i++) for (let j = i + 1; j < cards.length; j++) {
    expect(overlaps(cards[i], cards[j]), `${spec.panels[i].id} overlaps ${spec.panels[j].id}`).toBe(false);
  }
});

async function mountApp(page: Page, html: string, fragment: unknown, theme: "light" | "dark") {
  // A same-origin harness document owns the host-side transport; the iframe has an opaque origin.
  await page.route("http://fanout-harness.test/**", route => route.fulfill({ contentType: "text/html", body: "<!doctype html><html><body style='margin:0'><iframe title='Fragment smoke' sandbox='allow-scripts' style='display:block;width:100%;height:240px;border:0'></iframe></body></html>" }));
  await page.goto("http://fanout-harness.test/");
  await page.evaluate(({ html, fragment, theme }) => {
    const iframe = document.querySelector("iframe")!;
    const host = window as typeof window & { smokeSizes: number[]; smokeDelivered: boolean };
    host.smokeSizes = []; host.smokeDelivered = false;
    const send = (message: unknown) => iframe.contentWindow!.postMessage(message, "*");
    window.addEventListener("message", event => {
      if (event.source !== iframe.contentWindow || !event.data || event.data.jsonrpc !== "2.0") return;
      const message = event.data;
      if (message.method === "ui/initialize") {
        send({ jsonrpc: "2.0", id: message.id, result: { protocolVersion: message.params.protocolVersion,
          hostInfo: { name: "Fanout smoke", version: "1.0.0" }, hostCapabilities: { serverTools: {}, logging: {} },
          hostContext: { theme, displayMode: "inline", availableDisplayModes: ["inline", "fullscreen"] } } });
      } else if (message.method === "ui/notifications/initialized") {
        send({ jsonrpc: "2.0", method: "ui/notifications/tool-input", params: { arguments: {} } });
        send({ jsonrpc: "2.0", method: "ui/notifications/tool-result", params: { content: [{ type: "text", text: JSON.stringify(fragment) }], structuredContent: fragment, isError: false } });
        host.smokeDelivered = true;
      } else if (message.method === "ui/notifications/size-changed") {
        host.smokeSizes.push(performance.now());
        if (message.params.height) iframe.style.height = `${Math.min(12000, Math.max(240, Math.round(message.params.height)))}px`;
      } else if (message.id !== undefined) {
        send({ jsonrpc: "2.0", id: message.id, error: { code: -32601, message: "Smoke harness has no interactive tools" } });
      }
    });
    iframe.srcdoc = html;
  }, { html, fragment, theme });
}

for (const name of ["overview", "performance", "topology", "logs", "trace"]) {
  test(`chat fragment ${name} renders and size messages converge`, async ({ page }) => {
    const html = await readFile(`${root}/internal/mcp/apps/panels.html`, "utf8");
    const fragment = JSON.parse(await readFile(`${root}/ui/host/tests/go-fragments/${name}.json`, "utf8")) as { dashboard: { panels: { id: string; viz: string }[] } };
    await mountApp(page, html, fragment, test.info().project.use.colorScheme as "light" | "dark");
    const frame = page.frameLocator("iframe");
    await expect.poll(() => page.evaluate(() => (window as typeof window & { smokeDelivered: boolean }).smokeDelivered)).toBe(true);
    await expect(frame.locator("html")).toHaveAttribute("data-mantine-color-scheme", test.info().project.use.colorScheme!);
    for (const panel of fragment.dashboard.panels) {
      const card = frame.locator(`.mantine-Paper-root[data-panel="${panel.id}"]`);
      await card.scrollIntoViewIfNeeded();
      await rendered(card, panel.viz);
    }
    await expect(frame.getByRole("alert")).toHaveCount(0);
    await expect(frame.locator('[aria-label="Loading panel view"], [data-panel-error]')).toHaveCount(0);
    // Wait for the first settled frame: at least one size report, then a full quiet second.
    await expect.poll(() => page.evaluate(() => {
      const times = (window as typeof window & { smokeSizes: number[] }).smokeSizes;
      return times.length > 0 && performance.now() - times[times.length - 1] >= 1000;
    }), { timeout: 30_000 }).toBe(true);
    const before = await page.evaluate(() => (window as typeof window & { smokeSizes: number[] }).smokeSizes.length);
    await page.waitForTimeout(2_000);
    const after = await page.evaluate(() => (window as typeof window & { smokeSizes: number[] }).smokeSizes.length);
    expect(after - before, "Zero size messages in two seconds after settled frame").toBe(0);
  });
}
