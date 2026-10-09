import { defineConfig } from "@playwright/test";

export default defineConfig({
  testDir: "./e2e",
  testMatch: "**/*.spec.ts",
  globalSetup: "./e2e/global-setup.ts",
  workers: 1,
  retries: 0,
  forbidOnly: Boolean(process.env.CI),
  timeout: 120_000,
  globalTimeout: 12 * 60_000,
  expect: { timeout: 30_000 },
  reporter: [["list"], ["./e2e/failure-report.ts", { outputFolder: "playwright-report" }]],
  outputDir: "test-results",
  use: {
    channel: "chrome",
    headless: true,
    actionTimeout: 15_000,
    navigationTimeout: 30_000,
    trace: "retain-on-failure",
    screenshot: "only-on-failure",
  },
  projects: [1100, 1440].flatMap(width => (["light", "dark"] as const).map(colorScheme => ({
    name: `${width}-${colorScheme}`,
    use: { viewport: { width, height: 900 }, colorScheme },
  }))),
});
