import { test as base, expect, type Frame } from "@playwright/test";
import axe from "axe-core";

export const test = base.extend<{ scanApp: (frame: Frame) => Promise<void> }>({
  scanApp: async ({}, use, info) => {
    await use(async frame => {
      await frame.evaluate(axe.source);
      const report = await frame.evaluate(() => (window as typeof window & { axe: typeof axe }).axe.run(document));
      await info.attach("mcp-axe.json", { body: JSON.stringify(report), contentType: "application/json" });
      expect(report.violations.filter(v => v.impact === "serious" || v.impact === "critical")).toEqual([]);
    });
  },
});
