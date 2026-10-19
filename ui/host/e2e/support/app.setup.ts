import { test, expect } from "../fixtures/test";
import { spawn } from "node:child_process";
import { readFile } from "node:fs/promises";
import { fileURLToPath } from "node:url";

test("start disposable seeded app", async () => {
  if (process.env.FANOUT_LANDING_BASE_URL) return;
  const child = spawn("bun", [fileURLToPath(new URL("./app-server.ts", import.meta.url))], { stdio: ["ignore", "inherit", "inherit"] });
  child.unref();
  await expect.poll(async () => {
    if (child.exitCode !== null) throw new Error(`Seeded server setup exited ${child.exitCode}`);
    return readFile(process.env.FANOUT_E2E_STATE!, "utf8").then(text => Boolean(JSON.parse(text).env), () => false);
  }, { timeout: 300_000 }).toBe(true);
});
