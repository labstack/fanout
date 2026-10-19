import { test as base, expect } from "@playwright/test";

export const test = base.extend<{ faults: string[] }>({
  faults: [async ({}, use, info) => {
    const faults: string[] = [];
    await use(faults);
    if (info.file.endsWith("/smoke.spec.ts")) expect(faults, "No page errors, console errors or warnings (including iframe)").toEqual([]);
  }, { auto: true }],
  page: async ({ page, faults }, use, info) => {
    if (info.file.endsWith("/smoke.spec.ts")) {
      page.on("pageerror", error => faults.push(`pageerror: ${error.message}`));
      page.on("console", message => {
        if (message.type() === "error" || message.type() === "warning") faults.push(`${message.type()}: ${message.text()}`);
      });
    }
    await use(page);
  },
});
