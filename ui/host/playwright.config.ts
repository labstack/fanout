import { defineConfig } from "@playwright/test";
import { randomUUID } from "node:crypto";
import { tmpdir } from "node:os";
import { join } from "node:path";

process.env.FANOUT_E2E_STATE ??= join(tmpdir(), `fanout-e2e-${randomUUID()}.json`);
const snapshotPathTemplate = "{testDir}/../../__screenshots__/{projectName}/{platform}/{testFilePath}/{arg}{ext}";
export default defineConfig({
  testDir: "./e2e/specs",
  testMatch: "**/*.spec.ts",
  testIgnore: process.env.FANOUT_E2E_SKIP_PERFORMANCE === "1" ? ["**/performance.spec.ts"] : [],
  globalTeardown: "./e2e/support/app-teardown.ts",
  fullyParallel: true,
  workers: 1,
  retries: 0,
  forbidOnly: !!process.env.CI,
  failOnFlakyTests: true,
  timeout: 120_000,
  globalTimeout: (process.env.FANOUT_LANDING_RECORDINGS === "1" ? 40 : 12) * 60_000,
  expect: { timeout: 30_000, toHaveScreenshot: { animations: "disabled" } },
  reporter: [["list"], ["./e2e/failure-report.ts", { outputFolder: "playwright-report" }]],
  outputDir: "test-results",
  webServer: {
    command: "bun e2e/host/server.mjs",
    wait: { stdout: /listening on (?<e2e_port>\d+)/ },
    timeout: 120_000,
    gracefulShutdown: { signal: "SIGTERM", timeout: 5000 },
  },
  use: {
    baseURL: `http://127.0.0.1:${process.env.E2E_PORT}`,
    headless: true,
    actionTimeout: 15_000,
    navigationTimeout: 30_000,
    trace: "on-first-retry",
    screenshot: "only-on-failure",
    reducedMotion: "reduce",
    locale: "en-US",
    timezoneId: "UTC",
  },
  projects: [
    { name: "setup", testDir: "./e2e/support", testMatch: "app.setup.ts", timeout: 300_000 },
    ...[1100, 1440].flatMap(width => (["light", "dark"] as const).map(colorScheme => ({
      name: `app-${width}-${colorScheme}`,
      testDir: "./e2e/specs/app", dependencies: ["setup"], workers: 1, fullyParallel: false,
      snapshotPathTemplate,
      use: { channel: "chrome", viewport: { width, height: 900 }, colorScheme },
    }))),
    ...["mcp", "visual"].map(name => ({
      name, testDir: `./e2e/specs/${name}`, snapshotPathTemplate,
      retries: process.env.CI ? 1 : 0,
      use: { viewport: { width: 1100, height: 900 } },
    })),
  ],
});
