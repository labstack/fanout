import { test, expect } from "../../fixtures/test";

for (const theme of ["light", "dark"] as const) for (const scenario of ["overview", "performance", "trace"]) {
  test(`${scenario} ${theme}`, async ({ mcpHost, page }) => {
    await mcpHost(scenario, theme);
    await expect(page).toHaveScreenshot(`${scenario}-${theme}.png`, { fullPage: true });
  });
}
