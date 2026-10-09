import { request } from "@playwright/test";
import { spawn, type ChildProcess } from "node:child_process";
import { randomBytes } from "node:crypto";
import { once } from "node:events";
import { mkdtemp, readFile, rm, writeFile } from "node:fs/promises";
import { createServer } from "node:net";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";
import { fileURLToPath } from "node:url";

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

function seed(binary: string | undefined, base: string, tokenFile: string): Promise<void> {
  const args = ["-endpoint", base, "-token-file", tokenFile];
  const child = binary
    ? spawn(resolve(binary), args, { cwd: root, stdio: ["ignore", "ignore", "ignore"] })
    : spawn("bash", ["scripts/with-duckdb.sh", "go", "run", "./internal/cmd/e2eseed", ...args], { cwd: root, stdio: ["ignore", "ignore", "ignore"] });
  return new Promise((ok, fail) => {
    const timer = setTimeout(() => { child.kill("SIGKILL"); fail(new Error("Seed timed out")); }, 120_000);
    child.once("error", () => { clearTimeout(timer); fail(new Error("Could not start seed command")); });
    child.once("exit", code => { clearTimeout(timer); code === 0 ? ok() : fail(new Error(`Seed command exit ${code}`)); });
  });
}

export default async function globalSetup() {
  const dir = await mkdtemp(join(tmpdir(), "fanout-e2e-"));
  let child: ChildProcess | undefined;
  let api: Awaited<ReturnType<typeof request.newContext>> | undefined;
  const cleanup = async () => {
    try { if (child) await stop(child); } finally {
      try { await api?.dispose(); } finally { await rm(dir, { recursive: true, force: true }); }
    }
  };
  try {
    const base = `http://127.0.0.1:${await freePort()}`;
    // Ignore inherited product configuration and keys; this instance is disposable.
    const env = Object.fromEntries(Object.entries(process.env).filter(([key]) => !key.startsWith("FANOUT_") && !/(?:API_KEY|TOKEN|SECRET)$/.test(key)));
    child = spawn(resolve(process.env.FANOUT_E2E_BINARY ?? join(root, "bin/fanout")), [], {
      cwd: dir,
      env: { ...env, FANOUT_DATA_DIR: join(dir, "data"), FANOUT_ADDR: new URL(base).host,
        FANOUT_AUTH_CODE_SECRET: randomBytes(32).toString("hex"), FANOUT_AI_API_KEY: "",
        FANOUT_ROLLUP_INTERVAL: "1s" },
      stdio: ["ignore", "pipe", "pipe"],
    });
    // Fanout prints the setup banner (with the one-time credential) to stderr.
    // Keep it private and bounded; stdout carries nothing this setup needs.
    let stderr = "";
    let spawnFailed = false;
    child.once("error", () => { spawnFailed = true; });
    child.stderr!.on("data", (chunk: Buffer) => { stderr = (stderr + chunk.toString()).slice(-32_768); });
    child.stdout!.resume();
    const startDeadline = Date.now() + 30_000;
    let token: string | undefined;
    while (Date.now() < startDeadline) {
      token = stderr.match(/setup_token=([A-Za-z0-9_%.-]+)/)?.[1];
      if (token) break;
      if (spawnFailed || child.exitCode !== null || child.signalCode !== null) throw new Error("Fanout exited before setup");
      await pause(100);
    }
    if (!token) throw new Error("No setup URL in server stderr within 30 seconds");
    api = await request.newContext({ baseURL: base, extraHTTPHeaders: { "Fanout-Request": "1" }, timeout: 25_000 });
    const setup = await api.post("/api/auth/setup", { data: { email: "smoke@example.test", name: "Browser smoke", setup_token: decodeURIComponent(token) } });
    if (!setup.ok()) throw new Error(`Admin setup HTTP ${setup.status()}`);
    const { ingest_token: ingestToken } = await setup.json() as { ingest_token?: string };
    if (!ingestToken) throw new Error("Setup returned no ingest credential");
    const tokenFile = join(dir, "ingest-token");
    // The command contract requires a temporary file; never put credentials in argv.
    await writeFile(tokenFile, ingestToken, { mode: 0o600 });
    try { await seed(process.env.FANOUT_E2E_SEED_BINARY, base, tokenFile); }
    finally { await rm(tokenFile, { force: true }); }

    const dashboard = { version: 1, name: "Readiness", time: { range: "3h", refresh: "off" }, panels: [
      { id: "count", title: "Count", viz: "stat", query: { from: "spans", measures: ["count()"] } },
      { id: "log_count", title: "Log count", viz: "stat", query: { from: "logs", measures: ["count()"] } },
      { id: "map", title: "Map", viz: "service_map", query: { from: "spans" } },
    ] };
    const deadline = Date.now() + 90_000;
    let ready = false;
    while (Date.now() < deadline) {
      const response = await api.post("/api/panels/query", { data: { dashboard }, timeout: Math.min(20_000, deadline - Date.now()) });
      if (!response.ok()) throw new Error(`Readiness panel HTTP ${response.status()}`);
      const body = await response.json();
      const results = body.results as { id: string; status: string; frame?: { rows: number; values: unknown[][] } }[] | undefined;
      if (results?.some(result => result.status === "error")) throw new Error("Readiness panel failed");
      // Counts alone can precede log flushes and edge-rollup publication. Wait for both signals and all services.
      const countsReady = ["count", "log_count"].every(id => {
        const result = results?.find(result => result.id === id);
        return result?.status === "ok" && result.frame!.rows > 0 && result.frame!.values.some(col => col.some(value => typeof value === "number" && value > 0));
      });
      const map = results?.find(result => result.id === "map");
      if (countsReady && map?.status === "ok" && map.frame!.rows >= 6) { ready = true; break; }
      await pause(Math.min(500, Math.max(0, deadline - Date.now())));
    }
    if (!ready) throw new Error("No seeded rows within 90 seconds");
    const spec = JSON.parse(await readFile(join(root, "ui/host/e2e/fixtures/all-panels.json"), "utf8"));
    const created = await api.post("/api/dashboards", { data: { spec } });
    if (created.status() !== 201) throw new Error(`Create fixture HTTP ${created.status()}`);
    const record = await created.json();
    if (typeof record.id !== "string") throw new Error("Created dashboard has no id");
    const state = join(dir, "storage-state.json");
    await writeFile(state, JSON.stringify(await api.storageState()), { mode: 0o600 });
    process.env.FANOUT_E2E_BASE_URL = base;
    process.env.FANOUT_E2E_DASHBOARD_ID = record.id;
    process.env.FANOUT_E2E_STORAGE_STATE = state;
    return cleanup;
  } catch (error) { await cleanup(); throw error; }
}
