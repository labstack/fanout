import { test as base, expect, type APIRequestContext, type Frame, type Locator, type Page, type TestInfo } from "@playwright/test";
import { readFile } from "node:fs/promises";
import { createHash } from "node:crypto";
import { fileURLToPath } from "node:url";
import fixture from "./fixtures/all-panels.json" with { type: "json" };
import type { DashboardSpec, Panel, PanelResult } from "../../panels/types";
import type { PanelFragment } from "../../panels/fragment";
import { mountApp } from "./chat-harness";

export const spec = fixture as DashboardSpec;
export const test = base.extend({
  baseURL: async ({}, use) => { await use(process.env.FANOUT_E2E_BASE_URL); },
  storageState: async ({}, use) => {
    const state = process.env.FANOUT_E2E_STORAGE_STATE;
    if (!state) throw new Error("Global setup did not provide a session");
    await use(state);
  },
});
export type Surface = "dashboard" | "chat";
export type Scenario = "error" | "empty" | "truncated" | "stale";
export const longError = "Synthetic accessibility fixture: query could not bind the requested field. ".repeat(25) + "END OF ERROR DETAIL";
const elements: Record<string, string> = {
  stat: "[data-stat-value]", gauge: "canvas", timeseries: "canvas", bar: "canvas", table: "tbody tr",
  text: "[data-panel-body] p", heatmap: "canvas", histogram: "canvas", scatter: "canvas", state_timeline: "canvas",
  logs: "tbody tr [data-field=body]", log_patterns: "tbody tr [data-field=body_template]", traces: "tbody tr [data-trace-id]",
  service_map: '[role=region][aria-label*="service dependency graph"] button', health: "[data-health-tiles]",
};
export const cardSelector = (id: string) => `.mantine-Paper-root[data-panel="${id}"]`;
const themeFor = (info: TestInfo) => info.project.use.colorScheme as "light" | "dark";

export async function settle(surface: Page | Frame) {
  await surface.evaluate(async () => {
    await document.fonts.ready;
    // Mantine's modal, drawer and menu fade in over CSS transitions that start a
    // frame or two after mounting; contrast must be measured on the settled
    // frame, not mid-fade. Infinite animations (loaders) are not waited for.
    for (let frame = 0; frame < 20; frame++) {
      await new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve())));
      const running = document.getAnimations().filter(animation => animation.playState === "running" && animation.effect?.getComputedTiming().endTime !== Infinity);
      if (running.length === 0) break;
      await Promise.all(running.map(animation => animation.finished.catch(() => undefined)));
    }
  });
}
export async function readyCard(surface: Page | Frame, panel: Panel, scenario?: Scenario) {
  const card = surface.locator(cardSelector(panel.id));
  await card.scrollIntoViewIfNeeded();
  await expect(card.locator('[aria-label="Loading panel"], [aria-label="Refreshing"]')).toHaveCount(0);
  if (scenario === "error") await expect(card.locator("[data-panel-error]")).toBeVisible();
  else if (scenario === "empty") await expect(card.getByText("Synthetic fixture: no events in this window.")).toBeVisible();
  else await expect(card.locator(elements[panel.viz]).first()).toBeVisible();
  if (scenario === "truncated") await expect(card.getByText("Truncated: showing limited data")).toBeVisible();
  await settle(surface);
  // Compiler animations are 200 ms. Screenshots wait beyond that, without timing the app.
  await surface.waitForTimeout(250);
  return card;
}
function alter(result: PanelResult, scenario?: Scenario): PanelResult {
  if (scenario === "error") return { id: result.id, status: "error", elapsed_ms: 0, error: longError };
  if (scenario === "empty") return { id: result.id, status: "empty", elapsed_ms: 0, diagnosis: "Synthetic fixture: no events in this window." };
  if (scenario === "truncated") return { ...result, frame: { ...result.frame!, truncated: true } };
  return result;
}

export async function openSurface(page: Page, request: APIRequestContext, info: TestInfo, surface: Surface, panel?: Panel, scenario?: Scenario): Promise<Page | Frame> {
  const theme = themeFor(info);
  if (surface === "dashboard") {
    if (scenario && scenario !== "stale") await page.route("**/api/panels/query", async route => {
      const response = await route.fetch();
      const body = await response.json() as { results: PanelResult[] };
      body.results = body.results.map(result => result.id === panel!.id ? alter(result, scenario) : result);
      await route.fulfill({ response, json: body });
    });
    await page.addInitScript(value => localStorage.setItem("mantine-color-scheme-value", value), theme);
    await page.goto(`/dashboards/${process.env.FANOUT_E2E_DASHBOARD_ID}`);
    await expect(page.locator("html")).toHaveAttribute("data-mantine-color-scheme", theme);
    return page;
  }
  // Same spec and real server results, transported by the existing opaque-origin
  // bridge fixture: one panel, or all of them in one fragment when none is named.
  // State mutations below are explicitly synthetic, not telemetry.
  const panels = panel ? [panel] : spec.panels;
  const dashboard: DashboardSpec = { ...spec, variables: [{ name: "service", kind: "custom", options: ["checkout"], multi: true, include_all: true, default: "$__all" }], panels };
  const response = await request.post("/api/panels/query", { headers: { "Fanout-Request": "1" }, data: { dashboard, vars: { service: "$__all" } } });
  expect(response.status()).toBe(200);
  const body = await response.json() as { results: PanelResult[] };
  expect(body.results).toHaveLength(panels.length);
  for (const result of body.results) if (panels.find(p => p.id === result.id)?.viz !== "text") expect(result.status, result.id).toBe("ok");
  const fragment: PanelFragment = {
    view: { kind: "query", key: createHash("sha256").update(JSON.stringify(dashboard)).digest("hex") },
    dashboard, results: body.results.map(result => result.id === panel?.id ? alter(result, scenario) : result), vars: { service: "$__all" },
  };
  if (scenario === "stale") fragment.dashboard.variables!.push({ name: "probe", kind: "custom", options: ["before", "after"], default: "before" });
  const html = await readFile(fileURLToPath(new URL("../../../internal/mcp/apps/panels.html", import.meta.url)), "utf8");
  // Supplement the unchanged smoke bridge with the host's existing display-mode
  // protocol. Registered before mountApp's listener on the same window, so it
  // runs first and stops the bridge's generic unsupported-request response.
  await page.addInitScript(() => window.addEventListener("message", event => {
    const iframe = document.querySelector("iframe");
    const message = event.data;
    if (!iframe || event.source !== iframe.contentWindow || message?.method !== "ui/request-display-mode") return;
    event.stopImmediatePropagation();
    iframe.contentWindow!.postMessage({ jsonrpc: "2.0", id: message.id, result: { mode: message.params.mode } }, "*");
    iframe.contentWindow!.postMessage({ jsonrpc: "2.0", method: "ui/notifications/host-context-changed", params: { displayMode: message.params.mode } }, "*");
  }, true));
  await mountApp(page, html, fragment, theme);
  await expect.poll(() => page.evaluate(() => (window as typeof window & { smokeDelivered: boolean }).smokeDelivered)).toBe(true);
  const frame = page.frames().find(candidate => candidate.parentFrame() === page.mainFrame());
  if (!frame) throw new Error("Chat app iframe not mounted");
  await expect(frame.locator("html")).toHaveAttribute("data-mantine-color-scheme", theme);
  await expect(page.locator("iframe")).toHaveAttribute("sandbox", "allow-scripts");
  return frame;
}

export async function chooseView(surface: Page | Frame, card: Locator, mode: "Data" | "Spec" | "Chart") {
  const direct = card.locator(`button[data-panel-view="${mode}"]`);
  if (await direct.isVisible()) await direct.click();
  else {
    await card.getByRole("button", { name: / menu/ }).click();
    await surface.locator(`[role="menuitemradio"][data-panel-view="${mode}"]`).click();
  }
  await settle(surface);
}
export async function staleSurface(page: Page, surface: Page | Frame, kind: Surface, card: Locator) {
  if (kind === "dashboard") {
    await page.route("**/api/panels/query", route => route.fulfill({ status: 503, json: { code: "query_unavailable", message: "Synthetic fixture: refresh unavailable." } }));
    await page.getByRole("button", { name: "Refresh now", exact: true }).click();
    // Only panels in view refresh; clicking the toolbar scrolled the card away.
    await card.scrollIntoViewIfNeeded();
    await expect(card.getByRole("button", { name: /menu, refresh failed/ })).toBeVisible();
    // The refresh tooltip follows :focus-visible, which programmatic focus after
    // a mouse click does not match; arrive by keyboard instead.
    await card.getByRole("button", { name: /menu, refresh failed/ }).focus();
    await page.keyboard.press("Shift+Tab");
    await page.keyboard.press("Tab");
    await expect(card.getByRole("button", { name: /menu, refresh failed/ })).toBeFocused();
    await expect(surface.locator("[data-refresh-error-message]")).toBeVisible();
  } else {
    // The smoke bridge rejects tools/call; the actual app retains its prior data.
    await surface.getByRole("combobox", { name: "$probe", exact: true }).click();
    await surface.getByRole("option", { name: "after", exact: true }).click();
    await expect(surface.getByRole("alert")).toBeVisible();
    await expect(card.getByText(/Stale: last updated/)).toBeVisible();
  }
  await settle(surface);
}

export type Capture = { surface: Surface; theme: string; width: number; viz: string; panel_id: string; query_status: string; state: string; file: string; review: "pending" };
export async function capture(info: TestInfo, target: Locator, surface: Surface, panel: Panel, state = "chart", status = "ok"): Promise<Capture> {
  const name = `${surface}-${panel.viz}-${state}`;
  const file = info.outputPath(`${name}.png`);
  await target.screenshot({ path: file, animations: "disabled" });
  await info.attach(name, { path: file, contentType: "image/png" });
  return { surface, theme: themeFor(info), width: info.project.use.viewport!.width, viz: panel.viz, panel_id: panel.id, query_status: status, state, file: `${name}.png`, review: "pending" };
}
