import { expect, type Response } from "@playwright/test";
import { cardSelector, chooseView, readyCard, test } from "./accessibility-support";
import { chartLabel } from "./setup-support";
import type { DashboardTime, Panel, PanelResult } from "../../panels/types";
import { frameRows } from "../../panels/rows";
import { statusFor } from "../../panels/thresholds";

const panels: Panel[] = [
  { id: "check_states", title: "Is it reachable?", viz: "state_timeline" },
  { id: "service_map", title: "Services and external dependencies", viz: "service_map" },
  { id: "hosts", title: "Is anything running out? Hosts", viz: "table" },
  { id: "versions", title: "What changed?", viz: "table" },
];

test("default system overview shows all six self-observation scenarios within two refreshes", async ({ page }) => {
  test.setTimeout(60_000); // Two of the default dashboard's 30-second intervals.
  // Reuse the seed's exact end, so time spent in global setup or other specs
  // cannot turn the live fixtures into stopped sources. Timers still run.
  const window = JSON.parse(process.env.FANOUT_E2E_PERFORMANCE_WINDOW ?? "null") as { to: string } | null;
  if (!window) throw new Error("Global setup did not provide the seed window");
  await page.clock.setFixedTime(new Date(window.to));
  await page.addInitScript(theme => localStorage.setItem("mantine-color-scheme-value", theme), test.info().project.use.colorScheme!);
  await page.goto("/");
  await expect(page).toHaveURL(/\/dashboards\/[^/?]+/);
  // The server resolves a relative range with its own clock, which keeps
  // moving while other specs run, so pin the hour ending at the seed's end.
  const pinned = new URL(page.url());
  pinned.searchParams.set("from", new Date(new Date(window.to).getTime() - 3_600_000).toISOString());
  pinned.searchParams.set("to", new Date(window.to).toISOString());
  // Only collect requests for the pinned document. The first document's
  // relative-range responses can lose their bodies during navigation. Attach
  // before navigating so the pinned document's initial results are included.
  const responses: Response[] = [];
  const collectResponse = (response: Response) => {
    if (new URL(response.url()).pathname !== "/api/panels/query" || !response.ok()) return;
    const time = (response.request().postDataJSON() as { time?: DashboardTime }).time;
    if (time?.from === pinned.searchParams.get("from") && time?.to === pinned.searchParams.get("to")) responses.push(response);
  };
  page.on("response", collectResponse);
  await page.goto(pinned.pathname + pinned.search);
  await expect(page.getByText("System overview", { exact: true }).first()).toBeVisible();
  // The harness scrolls each ordinary saved panel into view to trigger its
  // existing visibility-based query and verifies that its renderer is populated.
  for (const panel of panels) await readyCard(page, panel);
  for (let refresh = 0; refresh < 2; refresh++) {
    const response = page.waitForResponse(response => new URL(response.url()).pathname === "/api/panels/query" && response.ok());
    await page.getByRole("button", { name: "Refresh now", exact: true }).click();
    await response;
    for (const panel of panels) await readyCard(page, panel);
  }
  page.off("response", collectResponse);
  const results = new Map<string, PanelResult>();
  for (const response of responses) {
    const body = await response.json() as { results: PanelResult[] };
    for (const result of body.results) results.set(result.id, result);
  }

  const hosts = page.locator(cardSelector("hosts"));
  await hosts.scrollIntoViewIfNeeded();
  const full = hosts.locator("tbody tr").filter({ has: page.locator('td[data-field="host"]').getByText("disk-full-host", { exact: true }) });
  await expect(full.locator('[data-field="filesystem_percent"]')).toHaveText("95.0%");
  await expect(full.locator('[data-field="mountpoint"]')).toHaveText("/data");
  await expect(full.locator('[data-field="status"]')).toHaveText(/◆.*95/);
  const stopped = hosts.locator("tbody tr").filter({ has: page.locator('td[data-field="host"]').getByText("stopped-host", { exact: true }) });
  await expect(stopped.locator('[data-field="reporting"]')).toHaveText("not reporting");
  await expect(stopped.locator('[data-field="status"]')).toHaveText(/◆.*100/);

  const map = page.locator(cardSelector("service_map"));
  await map.scrollIntoViewIfNeeded();
  const wholeGraph = map.getByRole("button", { name: "Show whole Services and external dependencies graph", exact: true });
  if (await wholeGraph.isVisible()) await wholeGraph.click();
  await expect(map.getByRole("button", { name: /^warehouse-api\.example\.te.*, unhealthy/ })).toBeVisible();

  const versions = page.locator(cardSelector("versions"));
  await versions.scrollIntoViewIfNeeded();
  const deployed = versions.locator("tbody tr").filter({ has: page.locator('td[data-field="service"]').getByText("checkout", { exact: true }) });
  await expect(deployed.locator('[data-field="version"]')).toHaveText("1.1.0");

  const checks = page.locator(cardSelector("check_states"));
  await checks.scrollIntoViewIfNeeded();
  await expect(checks.getByRole("application")).toHaveAttribute("aria-label", chartLabel(panels[0].title, "state_timeline", true)!);
  // Canvas colours aren't DOM text. Check the real rendered frame against the
  // shared threshold rule, then expose the exact endpoint/state in its Data view.
  const result = results.get("check_states");
  expect(result?.status).toBe("ok");
  const rows = frameRows(result!.frame!);
  for (const [target, state, failure] of [["down", "down", 1], ["stopped", "no recent result", 2]] as const) {
    const row = rows.filter(row => String(row.endpoint_from).includes(`https://${target}.example.test/health`)).at(-1);
    expect(row?.state, target).toBe(state);
    expect(row?.failure, target).toBe(failure);
    expect(statusFor(Number(row?.failure), [{ value: 1, status: "bad" }], "lower"), target).toBe("bad");
  }
  await chooseView(page, checks, "Data");
  for (const [target, state] of [["down", "down"], ["stopped", "no recent result"]]) {
    const row = checks.locator("tbody tr").filter({ hasText: `https://${target}.example.test/health` }).last();
    await expect(row.getByRole("cell", { name: state, exact: true })).toBeVisible();
    await expect(row).toContainText("outside-collector");
  }
});
