import { test as base, expect, type Frame } from "@playwright/test";

export type Theme = "light" | "dark";
export const test = base.extend<{ mcpHost: (scenario: string, theme: Theme, options?: { hold?: boolean; clock?: boolean }) => Promise<Frame> }>({
  mcpHost: async ({ page }, use) => {
    const faults: string[] = [];
    page.on("pageerror", error => faults.push(error.message));
    await use(async (scenario, theme, options) => {
      if (options?.clock) await page.clock.install();
      await page.goto(`/?${new URLSearchParams({ scenario, theme, ...(options?.hold ? { hold: "1" } : {}) })}`);
      await expect(page.locator("body")).toHaveAttribute("data-ready", "true");
      const frame = page.frames().find(frame => frame.parentFrame() === page.mainFrame());
      if (!frame) throw new Error("MCP App frame is missing");
      if (!options?.hold && scenario !== "unavailable") {
        await expect(frame.getByRole("status", { name: "Loading panel view" })).toHaveCount(0);
        await frame.evaluate(async () => { await document.fonts.ready; });
      }
      return frame;
    });
    expect(faults, "Mock host rejects unexpected requests and browser errors").toEqual([]);
  },
});
