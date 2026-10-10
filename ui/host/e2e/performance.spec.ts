import { test, expect, type Browser, type Page, type Request, type Response } from "@playwright/test";
import { createHash } from "node:crypto";
import type { PanelResult, Frame } from "../../panels/types";
import spec from "./fixtures/performance.json" with { type: "json" };
import { allPanelsPainted, assertRefreshRequests, CI_RENDER_CEILING_MS, p95 } from "./performance-support";

const viewport = { width: 1440, height: 3000 };
const panelIds = spec.panels.map(panel => panel.id).sort();
const loaders = '[aria-label="Loading panel"], [aria-label="Refreshing"]';
type Event = { path: string; request: Request; response?: Response; finished: boolean };
type Batch = { selected: string[]; results: { id: string; status: string; elapsed_ms: number; from_ms?: number; to_ms?: number; cells: number; sql?: string }[] };
type Evidence = { paths: string[]; completed_paths: string[]; batches: Batch[] };

function observe(page: Page) {
  const events: Event[] = [], faults: string[] = [];
  let lastActivity = Date.now();
  page.on("pageerror", error => faults.push(`pageerror: ${error.message}`));
  page.on("console", message => {
    if (["warning", "error"].includes(message.type())) faults.push(`${message.type()}: ${message.text()}`);
  });
  // During timing listeners only retain references. Parse responses, count
  // requests and assert correctness AFTER the page has captured its timestamp.
  page.on("request", request => {
    const path = new URL(request.url()).pathname;
    if (path.startsWith("/api/")) { events.push({ path, request, finished: false }); lastActivity = Date.now(); }
  });
  page.on("response", response => {
    const event = events.find(event => event.request === response.request());
    if (event) { event.response = response; lastActivity = Date.now(); }
  });
  page.on("requestfinished", request => {
    const event = events.find(event => event.request === request);
    if (event) { event.finished = true; lastActivity = Date.now(); }
  });
  page.on("requestfailed", request => {
    faults.push(`request failed: ${new URL(request.url()).pathname}`);
    const event = events.find(event => event.request === request);
    if (event) { event.finished = true; lastActivity = Date.now(); }
  });
  return { events, faults, lastActivity: () => lastActivity };
}

function cells(frame?: Frame): number {
  if (!frame) return 0;
  return frame.values.reduce((sum, column) => sum + column.length, 0) + (frame.totals?.length ?? 0) +
    Object.values(frame.trends ?? {}).reduce((sum, columns) => sum + columns.reduce((n, column) => n + column.length, 0), 0);
}

async function settled(page: Page, observed: ReturnType<typeof observe>) {
  await expect(page.locator(loaders)).toHaveCount(0);
  await expect(page.locator('[data-panel-error], [data-refresh-error-message]')).toHaveCount(0);
  // Also catch delayed duplicates: require complete bodies, a half second with
  // no API activity, then two browser frames. Controller verifies this quiet
  // interval against actual scheduling; it never contributes to paint timing.
  await expect.poll(() => ({ pending: observed.events.filter(event => !event.finished).length,
    quiet: Date.now() - observed.lastActivity() >= 500 })).toEqual({ pending: 0, quiet: true });
  await page.evaluate(() => new Promise<void>(resolve => requestAnimationFrame(() => requestAnimationFrame(() => resolve()))));
}

async function evidence(observed: ReturnType<typeof observe>, start = 0): Promise<Evidence> {
  const events = observed.events.slice(start);
  const batches: Batch[] = [];
  for (const event of events) {
    expect(event.finished, `Complete body: ${event.path}`).toBe(true);
    expect(event.response?.ok(), `HTTP success: ${event.path}`).toBe(true);
    if (event.path !== "/api/panels/query") continue;
    const body = await event.response!.json() as { results: PanelResult[] };
    const selected = (event.request.postDataJSON() as { panels?: string[] }).panels ?? panelIds;
    const returned = body.results.map(result => result.id).sort();
    // Array equality catches duplicate IDs that a Map would silently overwrite.
    expect(returned).toEqual([...selected].sort());
    expect([...new Set(returned)]).toEqual(returned);
    let frameCells = 0;
    const results = body.results.map(result => {
      expect(result.status, result.id).toBe("ok");
      expect(result.frame?.rows, `${result.id} populated`).toBeGreaterThan(0);
      expect(Number.isFinite(result.elapsed_ms), `${result.id} timing`).toBe(true);
      expect(result.elapsed_ms).toBeGreaterThanOrEqual(0);
      const count = cells(result.frame) + cells(result.previous);
      frameCells += count;
      return { id: result.id, status: result.status, elapsed_ms: result.elapsed_ms,
        from_ms: result.from_ms, to_ms: result.to_ms, cells: count, sql: result.sql?.slice(0, 4096) };
    });
    expect(frameCells, "Batch frame cells bounded").toBeLessThanOrEqual(200000);
    batches.push({ selected, results });
  }
  expect(observed.faults, "Zero console, page and network faults").toEqual([]);
  return { paths: events.map(event => event.path), completed_paths: events.filter(event => event.finished).map(event => event.path), batches };
}

async function assertInitial(page: Page, observed: ReturnType<typeof observe>) {
  await settled(page, observed);
  await expect.poll(() => observed.events.filter(event => event.path === "/api/panels/variables/resolve" && event.finished).length).toBe(1);
  await expect.poll(() => observed.events.filter(event => event.path === "/api/annotations" && event.finished).length).toBeGreaterThan(0);
  const initial = await evidence(observed);
  expect([...new Set(initial.batches.flatMap(batch => batch.results.map(result => result.id)))].sort()).toEqual(panelIds);
  const theme = test.info().project.use.colorScheme!;
  await expect(page.locator("html")).toHaveAttribute("data-mantine-color-scheme", theme);
  for (const panel of spec.panels) {
    const card = page.locator(`.mantine-Paper-root[data-panel="${panel.id}"]`);
    await expect(card.locator(panel.viz === "table" ? "tbody tr" : panel.viz === "stat" ? "[data-stat-value]" : "canvas").first()).toBeVisible();
  }
  return initial;
}

async function freshContext(browser: Browser) {
  const baseURL = process.env.FANOUT_E2E_BASE_URL;
  const storageState = process.env.FANOUT_E2E_STORAGE_STATE;
  if (!baseURL || !storageState || !process.env.FANOUT_E2E_PERFORMANCE_ID) throw new Error("Global performance setup missing");
  return browser.newContext({ baseURL, storageState, viewport, colorScheme: test.info().project.use.colorScheme });
}

async function open(page: Page) {
  await page.addInitScript(theme => localStorage.setItem("mantine-color-scheme-value", theme), test.info().project.use.colorScheme!);
  await page.goto(`/dashboards/${process.env.FANOUT_E2E_PERFORMANCE_ID}`, { waitUntil: "commit" });
  const timestamp = await page.waitForFunction(allPanelsPainted, spec.panels, { polling: "raf" });
  const paintedAt = await timestamp.jsonValue() as number;
  await timestamp.dispose();
  return paintedAt;
}

async function attach(name: string, data: unknown) {
  await test.info().attach(name, { contentType: "application/json", body: JSON.stringify(data, null, 2) });
}

test.describe.serial("twelve panels over twenty-four hours", () => {
  const samples: { sample: number; render_ms: number; browser_version: string; geometry: unknown; network: Evidence }[] = [];
  test.beforeEach(() => {
    test.skip(test.info().project.use.viewport?.width !== 1440, "Smoke and accessibility cover both widths; timing uses 1440");
  });
  // Four five-context tests PER THEME. No retries, page warmup, discarded
  // outliers or timeout inflation; each retains the configured 120 s budget.
  for (let group = 0; group < 4; group++) {
    test(`paint samples ${group * 5 + 1}–${group * 5 + 5}`, async ({ browser }) => {
      const groupSamples: typeof samples = [];
      for (let offset = 0; offset < 5; offset++) {
        const context = await freshContext(browser);
        try {
          const page = await context.newPage();
          const observed = observe(page);
          const render_ms = await open(page);
          // All assertions and Node response parsing start after the timestamp.
          const network = await assertInitial(page, observed);
          const geometry = await page.evaluate(panels => panels.map(panel => {
            const card = document.querySelector<HTMLElement>(`.mantine-Paper-root[data-panel="${panel.id}"]`)!;
            const node = card.querySelector(panel.viz === "table" ? "tbody tr" : panel.viz === "stat" ? "[data-stat-value]" : "canvas")!;
            return { id: panel.id, card: card.getBoundingClientRect().toJSON(), paint: node.getBoundingClientRect().toJSON() };
          }), spec.panels);
          const sample = { sample: group * 5 + offset + 1, render_ms, browser_version: browser.version(), geometry, network };
          samples.push(sample); groupSamples.push(sample);
        } finally { await context.close(); }
      }
      await attach(`paint-samples-${group + 1}.json`, groupSamples);
    });
  }
  test("reports p95 and enforces the calibrated CI ceiling", async () => {
    const render_p95_ms = p95(samples.map(sample => sample.render_ms));
    const measurement = { fixture: "performance.json", fixture_version: spec.version, seed: 13, spans: 8640, logs: 8640,
      fixture_sha256: createHash("sha256").update(JSON.stringify(spec)).digest("hex"),
      server: "global-setup disposable Fanout binary; warmed seeded query engine",
      candidate_sha: process.env.GITHUB_SHA ?? null, ci: Boolean(process.env.CI),
      dashboard_id: process.env.FANOUT_E2E_PERFORMANCE_ID, window: JSON.parse(process.env.FANOUT_E2E_PERFORMANCE_WINDOW ?? "null"),
      theme: test.info().project.use.colorScheme, viewport, browser: test.info().project.use.channel,
      method: "navigation time origin to in-page RAF all-twelve visible nonblank paint; fonts and two ready frames",
      samples, render_p95_ms, local_target_ms: 1500, meets_local_target: render_p95_ms <= 1500, ci_ceiling_ms: CI_RENDER_CEILING_MS };
    await attach("performance.json", measurement);
    // Compact line for three-run calibration; full raw evidence is attached.
    console.log(`FANOUT_PERFORMANCE ${JSON.stringify({ theme: measurement.theme, samples: samples.length, render_p95_ms,
      local_target_ms: 1500, ci_ceiling_ms: CI_RENDER_CEILING_MS })}`);
    if (!process.env.CI || CI_RENDER_CEILING_MS === null) return;
    expect(Number.isFinite(CI_RENDER_CEILING_MS) && CI_RENDER_CEILING_MS > 0, "Valid calibrated CI ceiling").toBe(true);
    expect(render_p95_ms).toBeLessThanOrEqual(CI_RENDER_CEILING_MS);
  });
});

// Separate from sampling so refresh/range correctness never consumes a timed
// sample group's budget. One fresh context per theme, three cycles per range.
test("refreshes once per batch and resolves variables once per range", async ({ browser }) => {
  test.skip(test.info().project.use.viewport?.width !== 1440, "All-visible refresh guard uses 1440");
  const context = await freshContext(browser);
  const cycles: { range: string; cycle: number; network: Evidence }[] = [];
  try {
    const page = await context.newPage();
    const observed = observe(page);
    await open(page);
    await assertInitial(page, observed);
    for (const range of ["24h", "12h"]) {
      if (range === "12h") {
        const start = observed.events.length;
        await page.getByRole("group", { name: "Dashboard controls", exact: true }).getByRole("button").first().click();
        await page.getByRole("menuitem", { name: "Last 12 hours", exact: true }).click();
        await expect.poll(() => observed.events.slice(start).filter(event => event.path === "/api/panels/variables/resolve" && event.finished).length).toBe(1);
        await page.waitForFunction(allPanelsPainted, spec.panels, { polling: "raf" }).then(handle => handle.dispose());
        await settled(page, observed);
        await expect.poll(() => observed.events.slice(start).filter(event => event.path === "/api/panels/variables/resolve").length).toBe(1);
        await attach("range-change.json", await evidence(observed, start));
      }
      for (let cycle = 1; cycle <= 3; cycle++) {
        const start = observed.events.length;
        await page.getByRole("button", { name: "Refresh now", exact: true }).click();
        for (const path of ["/api/panels/query", "/api/annotations"]) {
          await expect.poll(() => observed.events.slice(start).filter(event => event.path === path && event.finished).length).toBe(1);
        }
        await page.waitForFunction(allPanelsPainted, spec.panels, { polling: "raf" }).then(handle => handle.dispose());
        await settled(page, observed);
        // Poll BOTH started and complete requests after quiet settlement, so an
        // in-flight duplicate cannot escape the completed-response budget.
        await expect.poll(() => {
          const events = observed.events.slice(start);
          assertRefreshRequests(events.map(event => event.path));
          assertRefreshRequests(events.filter(event => event.finished).map(event => event.path));
          return true;
        }).toBe(true);
        const network = await evidence(observed, start);
        expect(network.batches[0].selected.slice().sort()).toEqual(panelIds);
        cycles.push({ range, cycle, network });
      }
    }
    await attach("refresh-cycles.json", { theme: test.info().project.use.colorScheme, viewport, cycles, faults: observed.faults });
  } finally { await context.close(); }
});
