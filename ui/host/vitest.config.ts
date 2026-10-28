import { storybookTest } from "@storybook/addon-vitest/vitest-plugin";
import { playwright } from "@vitest/browser-playwright";
import { defineConfig } from "vitest/config";
import { reviewWidths } from "./.storybook/viewports.ts";

const cells = process.env.VITEST_STORYBOOK === "true" ? [undefined] : Object.keys(reviewWidths).flatMap(viewport => ["light", "dark"].map(colorScheme => ({ viewport, colorScheme })));

export default defineConfig({
  test: {
    maxWorkers: 1,
    fileParallelism: false,
    testTimeout: 60_000,
    projects: cells.map(cell => ({
      extends: true,
      plugins: [storybookTest({ configDir: ".storybook", ...(cell && { initialGlobals: { colorScheme: cell.colorScheme, pace: "fast", viewport: { value: cell.viewport, isRotated: false } } }) })],
      test: {
        name: cell ? `catalog-${cell.colorScheme}-${cell.viewport}` : "catalog",
        browser: { enabled: true, provider: playwright(), headless: true, instances: [{ browser: "chromium" as const }] },
      },
    })),
  },
});
