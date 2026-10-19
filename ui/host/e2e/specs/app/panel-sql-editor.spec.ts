import { test, expect } from "../../fixtures/test";
import { editedSQL, openSqlDashboard, openSqlEditor } from "../../support/panel-sql-editor-support";

test("edits an existing SQL panel, previews it and saves a new version in history", async ({ page, request }, info) => {
  const scripts: string[] = [];
  page.on("request", request => { if (request.resourceType() === "script") scripts.push(request.url()); });
  const { record, cleanup } = await openSqlDashboard(page, request, info);
  try {
    expect(scripts.some(url => /panel-sql-editor-.*\.js/.test(url))).toBe(false);
    const dialog = await openSqlEditor(page);
    expect(scripts.some(url => /panel-sql-editor-.*\.js/.test(url))).toBe(true);
    const editor = dialog.getByRole("textbox", { name: "Panel SQL" });
    await editor.fill(editedSQL);
    await editor.press("ControlOrMeta+Enter");
    await expect(dialog.getByText("Preview result", { exact: true })).toBeVisible();
    await expect(dialog.locator("[data-stat-value]")).toBeVisible();
    await editor.focus(); await editor.press("Escape");
    await expect(dialog.getByRole("button", { name: "Run", exact: true })).toBeFocused();
    await expect(dialog).toBeVisible();
    await expect(editor).toHaveText(editedSQL);
    await expect(dialog.getByRole("button", { name: "Discard changes", exact: true })).toHaveCount(0);
    await editor.focus(); await editor.press("Tab");
    await expect(dialog.getByRole("button", { name: "Run", exact: true })).toBeFocused();
    await dialog.getByRole("button", { name: "Save SQL", exact: true }).click();
    await expect(dialog).toBeHidden();
    await page.getByRole("button", { name: "History", exact: true }).click();
    const history = page.locator("[data-version-history]");
    const currentVersion = history.getByRole("listitem").filter({ has: page.getByRole("button", { name: `Version ${record.version + 1}`, exact: true }) });
    await expect(currentVersion.getByText("Current", { exact: true })).toBeVisible();
    await expect(currentVersion).toContainText("Edited SQL for SQL requests");
    const saved = await request.get(`/api/dashboards/${record.id}`);
    expect(saved.ok()).toBe(true);
    const next = await saved.json();
    expect(next.version).toBe(record.version + 1);
    expect(next.spec).toEqual({ ...record.spec, panels: record.spec.panels.map(p => p.id === "sql_count" ? { ...p, sql: editedSQL } : p) });
  } finally { await cleanup(); }
});

test("cancel discards SQL edits without creating a version", async ({ page, request }, info) => {
  const { record, cleanup } = await openSqlDashboard(page, request, info);
  try {
    const dialog = await openSqlEditor(page);
    await dialog.getByRole("textbox", { name: "Panel SQL" }).fill(editedSQL);
    await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
    await expect(dialog.getByText("Discard SQL changes?", { exact: true })).toBeVisible();
    await dialog.getByRole("button", { name: "Keep editing", exact: true }).click();
    await expect(dialog.getByRole("textbox", { name: "Panel SQL" })).toHaveText(editedSQL);
    await dialog.getByRole("button", { name: "Cancel", exact: true }).click();
    await dialog.getByRole("button", { name: "Discard changes", exact: true }).click();
    await expect(dialog).toBeHidden();
    const response = await request.get(`/api/dashboards/${record.id}`);
    const next = await response.json();
    expect(next.version).toBe(record.version); expect(next.spec).toEqual(record.spec);
    await expect(page.getByRole("button", { name: "SQL requests menu", exact: true })).toBeFocused();
  } finally { await cleanup(); }
});
