import { request } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { randomBytes } from "node:crypto";
import { once } from "node:events";
import { closeSync, openSync, rmSync } from "node:fs";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";
import { diagnosticTail, installSignalCleanup, readinessCause, safeRequest, waitUntilReady } from "./setup-support";

const root = fileURLToPath(new URL("../../../", import.meta.url));
const pause = (ms: number) => new Promise(r => setTimeout(r, ms));

async function freePort(): Promise<number> {
  const server = createServer();
  server.listen(0, "127.0.0.1");
  await once(server, "listening");
  const address = server.address();
  if (!address || typeof address === "string") throw new Error("No loopback port");
  await new Promise<void>((ok, fail) => server.close(error => error ? fail(error) : ok()));
  return address.port;
}

async function stop(child: ChildProcess) {
  if (!child.pid || child.exitCode !== null || child.signalCode !== null) return;
  const exited = once(child, "exit");
  child.kill("SIGTERM");
  const timer = setTimeout(() => child.kill("SIGKILL"), 5_000);
  try { await exited; } finally { clearTimeout(timer); }
}

function seed(binary: string | undefined, base: string, tokenFile: string, log: string, track: (child: ChildProcess) => void): Promise<void> {
  const args = ["-endpoint", base, "-token-file", tokenFile];
  const fd = openSync(log, "a", 0o600);
  const child = binary
    ? spawn(resolve(binary), args, { cwd: root, stdio: ["ignore", fd, fd] })
    : spawn("bash", ["scripts/with-duckdb.sh", "go", "run", "./internal/cmd/e2eseed", ...args], { cwd: root, stdio: ["ignore", fd, fd] });
  closeSync(fd);
  track(child);
  return new Promise((ok, fail) => {
    const timer = setTimeout(() => { child.kill("SIGKILL"); fail(new Error("Seed timed out")); }, 120_000);
    child.once("error", () => { clearTimeout(timer); fail(new Error("Could not start seed command")); });
    child.once("exit", code => { clearTimeout(timer); code === 0 ? ok() : fail(new Error(`Seed command exit ${code}`)); });
  });
}

export default async function globalSetup() {
  const dir = await mkdtemp(join(tmpdir(), "fanout-e2e-"));
  let child: ChildProcess | undefined;
  let seedChild: ChildProcess | undefined;
  let api: Awaited<ReturnType<typeof request.newContext>> | undefined;
  const serverLog = join(dir, "server.log");
  const seedLog = join(dir, "seed.log");
  const removeSignalHandlers = installSignalCleanup(() => {
    for (const process of [child, seedChild]) {
      if (process?.pid && process.exitCode === null && process.signalCode === null) process.kill("SIGKILL");
    }
    rmSync(dir, { recursive: true, force: true });
  });
  const cleanup = async () => {
    try {
      if (seedChild) await stop(seedChild);
      if (child) await stop(child);
    } finally {
      try { await api?.dispose(); } finally { await rm(dir, { recursive: true, force: true }); }
      removeSignalHandlers();
    }
  };
  try {
    const base = `http://127.0.0.1:${await freePort()}`;
    // Ignore inherited product configuration and keys; this instance is disposable.
    const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith("FANOUT_") && !/(?:API_KEY|TOKEN|SECRET)$/.test(key)));
    const startDeadline = Date.now() + 30_000;
    const fd = openSync(serverLog, "a", 0o600);
    child = spawn(resolve(process.env.FANOUT_E2E_BINARY ?? join(root, "bin/fanout")), [], {
      cwd: dir,
      env: { ...env, FANOUT_DATA_DIR: join(dir, "data"), FANOUT_ADDR: new URL(base).host,
        FANOUT_AUTH_CODE_SECRET: randomBytes(32).toString("hex"), FANOUT_AI_API_KEY: "",
        FANOUT_ROLLUP_INTERVAL: "1s" },
      stdio: ["ignore", fd, fd],
    });
    closeSync(fd);
    // Fanout prints the setup banner (with the one-time credential) to stderr.
    // Both streams stay in a private file; only redacted tails reach diagnostics.
    let spawnFailed = false;
    child.once("error", () => { spawnFailed = true; });
    let token: string | undefined;
    while (Date.now() < startDeadline) {
      token = (await readFile(serverLog, "utf8")).match(/setup_token=([A-Za-z0-9_%.-]+)/)?.[1];
      if (token) break;
      if (spawnFailed || child.exitCode !== null || child.signalCode !== null) throw new Error("Fanout exited before setup");
      await pause(100);
    }
    if (!token) throw new Error("No setup URL in server stderr within 30 seconds");
    api = await request.newContext({ baseURL: base, extraHTTPHeaders: { "Fanout-Request": "1" }, timeout: 25_000 });
    const requestJSON = async (path: string, data: unknown, timeout = 25_000) => {
      const response = await safeRequest(path, () => api!.post(path, { data, timeout }));
      let body;
      try { body = await response.json(); }
      catch { throw new Error(`HTTP ${response.status()} ${path} invalid JSON`); }
      return { response, body };
    };
    await waitUntilReady(startDeadline, async () => {
      const response = await safeRequest("/readyz", () => api!.get("/readyz", { timeout: Math.max(1, Math.min(1000, startDeadline - Date.now())) }));
      return response.status() === 200 ? undefined : `HTTP ${response.status()} /readyz`;
    }, 100);
    const { body: setup } = await requestJSON("/api/auth/setup", { email: "smoke@example.test", name: "Browser smoke", setup_token: decodeURIComponent(token) });
    const { ingest_token: ingestToken } = setup as { ingest_token?: string };
    if (!ingestToken) throw new Error("Setup returned no ingest credential");
    const tokenFile = join(dir, "ingest-token");
    // The command contract requires a temporary file; never put credentials in argv.
    await writeFile(tokenFile, ingestToken, { mode: 0o600 });
    try { await seed(process.env.FANOUT_E2E_SEED_BINARY, base, tokenFile, seedLog, process => { seedChild = process; }); }
    finally { await rm(tokenFile, { force: true }); }

    const dashboard = { version: 1, name: "Readiness", time: { range: "3h", refresh: "off" }, panels: [
      { id: "count", title: "Count", viz: "stat", query: { from: "spans", measures: ["count()"] } },
      { id: "log_count", title: "Log count", viz: "stat", query: { from: "logs", measures: ["count()"] } },
      { id: "map", title: "Map", viz: "service_map", query: { from: "spans" } },
    ] };
    const deadline = Date.now() + 90_000;
    await waitUntilReady(deadline, async () => {
      const { body } = await requestJSON("/api/panels/query", { dashboard }, Math.max(1, Math.min(20_000, deadline - Date.now())));
      // Counts can precede log flushes and edge publication; inspect kind, not total rows.
      return readinessCause(body);
    });
    const spec = JSON.parse(await readFile(join(root, "ui/host/e2e/fixtures/all-panels.json"), "utf8"));
    const { response: created, body: record } = await requestJSON("/api/dashboards", { spec });
    if (created.status() !== 201) throw new Error(`HTTP ${created.status()} /api/dashboards`);
    if (typeof record.id !== "string") throw new Error("Created dashboard has no id");
    const state = join(dir, "storage-state.json");
    await writeFile(state, JSON.stringify(await api.storageState()), { mode: 0o600 });
    process.env.FANOUT_E2E_BASE_URL = base;
    process.env.FANOUT_E2E_DASHBOARD_ID = record.id;
    process.env.FANOUT_E2E_STORAGE_STATE = state;
    return cleanup;
  } catch (error) {
    try {
      for (const [name, path] of [["Server", serverLog], ["Seed", seedLog]]) {
        const output = await readFile(path, "utf8").catch(() => "No output captured");
        console.error(`${name} diagnostics (last 50 lines):\n${diagnosticTail(output)}`);
      }
    } finally { await cleanup(); }
    throw error;
  }
}
