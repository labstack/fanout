import { test, expect } from "../../fixtures/test";
import recordings from "../../data/mcp.json" with { type: "json" };

for (const theme of ["light", "dark"] as const) test.describe(theme, () => {
  test("announces loading before the host delivers a result", async ({ mcpHost, page }) => {
    const frame = await mcpHost("overview", theme, { hold: true });
    await expect(frame.getByRole("status", { name: "Loading panel view" })).toBeVisible();
    await page.evaluate(() => (window as typeof window & { deliver(): Promise<void> }).deliver());
    await expect(frame.getByRole("status", { name: "Loading panel view" })).toHaveCount(0);
    await expect(frame.getByRole("main", { name: "Fanout analysis view" })).toContainText("System health");
  });

  for (const scenario of ["overview", "topology", "performance", "trace", "logs", "custom"] as const) {
    test(`${scenario} renders recorded content with accessible controls`, async ({ mcpHost, scanApp, tabTo }) => {
      const frame = await mcpHost(scenario, theme);
      const main = frame.getByRole("main", { name: "Fanout analysis view" });
      await expect(main).toBeVisible();
      await expect(frame.locator("html")).toHaveAttribute("data-mantine-color-scheme", theme);
      await expect(frame.getByRole("status", { name: "Loading panel view" })).toHaveCount(0);
      for (const panel of recordings[scenario].result.structuredContent.dashboard.panels) await expect(main.getByText(panel.title, { exact: true })).toBeVisible();
      if (scenario === "overview") await expect(main.getByRole("region", { name: /Service health: .+; 6 services/ })).toBeVisible();
      if (scenario === "topology") {
        await expect(main.getByRole("region", { name: /service dependency graph/ })).toBeVisible();
        await expect(main.getByRole("button", { name: /checkout/ }).first()).toBeVisible();
      }
      if (scenario === "performance") {
        await expect(main.getByRole("application", { name: /^p95 latency: time series: [1-9]/ })).toBeVisible();
        await expect(main.getByRole("columnheader", { name: /operation/i })).toBeVisible();
      }
      if (scenario === "trace") {
        await expect(main.getByRole("button", { name: "Waterfall", exact: true })).toHaveAttribute("aria-pressed", "true");
        await expect(main.getByText("Correlated logs", { exact: true })).toBeVisible();
        await expect(main.getByRole("cell", { name: "payment failed", exact: true })).toBeVisible();
        await main.getByRole("button", { name: "Flame graph", exact: true }).click();
        await expect(main.getByRole("button", { name: "Flame graph", exact: true })).toHaveAttribute("aria-pressed", "true");
        await expect(main.getByRole("img", { name: /GET \/frontend/ }).first()).toBeVisible();
      }
      if (scenario === "logs") await expect(main.getByRole("columnheader", { name: /body/i })).toBeVisible();
      if (scenario === "custom") {
        await expect(main.getByText("8,640", { exact: true })).toBeVisible();
        await expect(main.getByRole("img", { name: "Value over this panel's time range" })).toBeVisible();
      }
      await scanApp(frame);
      await tabTo(main.getByRole("button").first());
      if (scenario === "trace") {
        await main.getByRole("button", { name: recordings.trace.result.structuredContent.trace.data.trace_id.slice(0, 16), exact: true }).click();
        const detail = frame.getByRole("dialog", { name: "Slow or erroring traces · traces" });
        await expect(detail.getByRole("button", { name: "Waterfall", exact: true })).toBeVisible();
        await expect(detail.getByRole("alert")).toHaveCount(0);
      }
    });
  }

  test("announces recorded no-data guidance", async ({ mcpHost, scanApp }) => {
    const frame = await mcpHost("no_data", theme);
    await expect(frame.getByRole("alert")).toContainText("No matching data.");
    await expect(frame.getByRole("alert")).toContainText("No logs match the selected filters");
    await scanApp(frame);
  });
  test("announces a tool problem", async ({ mcpHost, scanApp }) => {
    const frame = await mcpHost("error", theme);
    await expect(frame.getByRole("alert")).toHaveText("This view could not be loaded. Please try again.");
    await scanApp(frame);
  });
  test("announces host cancellation", async ({ mcpHost, scanApp }) => {
    const frame = await mcpHost("cancelled", theme);
    await expect(frame.getByRole("alert")).toHaveText("View cancelled.");
    await scanApp(frame);
  });
  test("shows its fallback when no host connects", async ({ mcpHost, page }) => {
    const frame = await mcpHost("unavailable", theme, { clock: true });
    await expect(frame.getByRole("status", { name: "Loading panel view" })).toBeVisible();
    await page.clock.runFor(60_001);
    await expect(frame.getByRole("alert")).toHaveText("This view could not be loaded. Please try again.");
  });
});
