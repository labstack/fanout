import { expect, type APIRequestContext, type Page, type TestInfo } from "@playwright/test";
import type { DashboardSpec } from "../../panels/types";

const initialSQL = "SELECT count(*) AS total FROM spans WHERE $__window(start_time) AND service = $service";
export const editedSQL = "SELECT count(*) + 1 AS total FROM spans WHERE $__window(start_time) AND service = $service";

/** Own dashboard per test: parallel saves never mutate another spec's fixture. */
export async function openSqlDashboard(page: Page, request: APIRequestContext, info: TestInfo) {
  const window = JSON.parse(process.env.FANOUT_E2E_PERFORMANCE_WINDOW ?? "null") as { from: string; to: string } | null;
  const spec: DashboardSpec = {
    version: 1, name: `SQL editor ${info.project.name} ${info.testId} ${info.retry}`,
    time: window ? { ...window, refresh: "off" } : { range: "3h", refresh: "off" },
    variables: [{ name: "service", kind: "custom", options: ["checkout", "frontend"], default: "checkout" }],
    panels: [{ id: "sql_count", title: "SQL requests", viz: "stat", unit: "count", sql: initialSQL, width: 6 }, { id: "notes", title: "Notes", viz: "text", content: "Keep this panel unchanged", width: 6 }],
  };
  const response = await request.post("/api/dashboards", { headers: { "Fanout-Request": "1" }, data: { spec } });
  expect(response.status()).toBe(201);
  const record = await response.json() as { id: string; version: number; spec: DashboardSpec };
  await page.addInitScript(theme => localStorage.setItem("mantine-color-scheme-value", theme), info.project.use.colorScheme ?? "light");
  await page.goto(`/dashboards/${record.id}`);
  const card = page.locator('.mantine-Paper-root[data-panel="sql_count"]');
  await expect(card.locator("[data-stat-value]")).toBeVisible();
  return { record, card, cleanup: async () => {
    const deleted = await request.delete(`/api/dashboards/${record.id}`, { headers: { "Fanout-Request": "1", "Fanout-Confirm-Delete": record.id } }).catch(() => undefined);
    // Record cleanup failures without replacing an assertion already unwinding
    // through the caller's finally block, including when the request rejects.
    expect.soft(deleted?.ok(), `Delete SQL dashboard ${record.id}: ${deleted ? `HTTP ${deleted.status()}` : "request failed"}`).toBe(true);
  } };
}

export async function openSqlEditor(page: Page) {
  await page.locator('.mantine-Paper-root[data-panel="sql_count"]').getByRole("button", { name: "SQL requests menu", exact: true }).click();
  await page.getByRole("menuitem", { name: "Edit SQL", exact: true }).click();
  const dialog = page.getByRole("dialog", { name: "Edit SQL · SQL requests" });
  await expect(dialog.getByRole("textbox", { name: "Panel SQL" })).toBeVisible();
  return dialog;
}
