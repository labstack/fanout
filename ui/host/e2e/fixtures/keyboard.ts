import { test as base, expect, type Locator } from "@playwright/test";

export const test = base.extend<{ tabTo: (target: Locator) => Promise<void> }>({
  tabTo: async ({ page }, use) => {
    await use(async target => {
      for (let step = 0; step < 40; step++) {
        await page.keyboard.press("Tab");
        if (await target.evaluate(element => element === document.activeElement)) break;
      }
      await expect(target).toBeFocused();
    });
  },
});
