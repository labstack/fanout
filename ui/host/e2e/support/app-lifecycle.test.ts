import { afterEach, expect, it } from "vitest";
import { spawn } from "node:child_process";
import { mkdtemp, readFile, readdir, rm, stat, writeFile } from "node:fs/promises";
import { join } from "node:path";
import { tmpdir } from "node:os";
import { startAppServer, stopAppServers, writeAppState } from "./app-lifecycle";

const states: string[] = [];
const directories: string[] = [];
afterEach(async () => {
  for (const state of states.splice(0)) await stopAppServers(state);
  for (const directory of directories.splice(0)) await rm(directory, { recursive: true, force: true });
});

async function fixture(mode = "ready") {
  const directory = await mkdtemp(join(tmpdir(), "fanout-lifecycle-test-"));
  directories.push(directory);
  const state = join(directory, "state.json");
  states.push(state);
  const script = join(directory, "manager.mjs");
  const started = join(directory, "started.json");
  await writeFile(script, `
    import { mkdirSync, writeFileSync } from "node:fs";
    import { join } from "node:path";
    const [id, dir] = process.argv.slice(-2);
    writeFileSync(${JSON.stringify(started)}, JSON.stringify({ pid: process.pid, dir }));
    mkdirSync(join(dir, "data"));
    writeFileSync(join(dir, "session.json"), "synthetic session");
    const mode = ${JSON.stringify(mode)};
    if (mode === "exit") process.exit(1);
    if (mode !== "silent") writeFileSync(join(dir, "ready.json"), JSON.stringify({ id: mode === "wrong_id" ? "previous" : id, pid: mode === "wrong_pid" ? process.pid + 1 : process.pid, env: { FANOUT_E2E_STORAGE_STATE: join(dir, "session.json") } }));
    process.on("SIGTERM", () => { if (mode !== "stubborn") process.exit(0); });
    setInterval(() => {}, 60000);
  `);
  return { state, started, options: { command: [process.execPath, script], timeout: 30_000 } };
}
async function exists(path: string) { return stat(path).then(() => true, () => false); }
function running(pid: number) { try { process.kill(pid, 0); return true; } catch { return false; } }

it("stops the previous owned server and removes its session/data before replacement", async () => {
  const { state, options } = await fixture();
  const first = await startAppServer(state, options);
  const second = await startAppServer(state, options);
  expect(running(first.pid)).toBe(false);
  expect(await exists(first.dir)).toBe(false);
  expect(second.pid).not.toBe(first.pid);
  expect(await exists(second.env.FANOUT_E2E_STORAGE_STATE)).toBe(true);
  expect(JSON.parse(await readFile(state, "utf8")).pid).toBe(second.pid);
  await stopAppServers(state);
  expect(running(second.pid)).toBe(false);
  expect(await exists(second.dir)).toBe(false);
  expect(await exists(state)).toBe(false);
  expect(await exists(`${state}.instances`)).toBe(false);
});

it.each(["wrong_pid", "wrong_id"])("rejects %s readiness and cleans up the failed launch", async mode => {
  const { state, started, options } = await fixture(mode);
  await expect(startAppServer(state, options)).rejects.toThrow("Seeded server reported readiness for another launch");
  const instance = JSON.parse(await readFile(started, "utf8"));
  expect(running(instance.pid)).toBe(false);
  expect(await exists(instance.dir)).toBe(false);
  expect(await exists(state)).toBe(false);
  expect(await exists(`${state}.instances`)).toBe(false);
});

it("times out and cleans up when the manager never reports readiness", async () => {
  const { state, options } = await fixture("silent");
  await expect(startAppServer(state, { ...options, timeout: 200 })).rejects.toThrow("Timed out waiting for the spawned seeded server");
  expect(await exists(state)).toBe(false);
  expect(await exists(`${state}.instances`)).toBe(false);
});

it("cleans the registered instance and temporary directory when setup exits early", async () => {
  const { state, started, options } = await fixture("exit");
  await expect(startAppServer(state, options)).rejects.toThrow("exited before readiness");
  const instance = JSON.parse(await readFile(started, "utf8"));
  expect(running(instance.pid)).toBe(false);
  expect(await exists(instance.dir)).toBe(false);
  expect(await exists(state)).toBe(false);
  expect(await exists(`${state}.instances`)).toBe(false);
});

it("tears down every registered instance even when shared readiness was replaced", async () => {
  const firstFixture = await fixture();
  const secondFixture = await fixture();
  const first = await startAppServer(firstFixture.state, firstFixture.options);
  const second = await startAppServer(secondFixture.state, secondFixture.options);
  const [entry] = await readdir(`${secondFixture.state}.instances`);
  await writeFile(join(`${firstFixture.state}.instances`, entry), await readFile(join(`${secondFixture.state}.instances`, entry)));
  await rm(`${secondFixture.state}.instances`, { recursive: true });
  await writeAppState(firstFixture.state, { pid: second.pid, env: second.env });
  await stopAppServers(firstFixture.state);
  for (const instance of [first, second]) {
    expect(running(instance.pid)).toBe(false);
    expect(await exists(instance.dir)).toBe(false);
  }
});

it("preserves unrelated PIDs and ownership records while cleaning other owned instances", async () => {
  const { state, options } = await fixture();
  const instance = await startAppServer(state, options);
  const dir = await mkdtemp(join(tmpdir(), "fanout-unowned-test-"));
  directories.push(dir);
  const other = spawn(process.execPath, ["-e", "setInterval(() => {}, 60000)"], { stdio: "ignore" });
  const entry = join(`${state}.instances`, "unowned");
  try {
    await writeAppState(entry, { id: "not-this-process", pid: other.pid, dir, command: "manager.mjs" });
    await expect(stopAppServers(state)).rejects.toThrow("Could not stop every owned seeded server");
    expect(running(other.pid!)).toBe(true);
    expect(await exists(entry)).toBe(true);
    expect(await exists(dir)).toBe(true);
    expect(running(instance.pid)).toBe(false);
    expect(await exists(instance.dir)).toBe(false);
  } finally {
    await rm(entry, { force: true });
    if (other.exitCode === null && other.signalCode === null) await new Promise<void>(resolve => { other.once("exit", () => resolve()); other.kill("SIGTERM"); });
  }
});

it("ignores stale shared readiness and publishes only the new instance", async () => {
  const { state, options } = await fixture();
  await writeAppState(state, { pid: process.pid, env: { FANOUT_E2E_STORAGE_STATE: "previous" } });
  const instance = await startAppServer(state, options);
  const ready = JSON.parse(await readFile(state, "utf8"));
  expect(ready.pid).toBe(instance.pid);
  expect(ready.id).toBe(instance.id);
  expect(ready.env.FANOUT_E2E_STORAGE_STATE).not.toBe("previous");
});

it("removes launch metadata when the manager cannot be spawned", async () => {
  const { state, options } = await fixture();
  await expect(startAppServer(state, { ...options, command: ["/nonexistent/fanout-test-manager", options.command[1]] })).rejects.toThrow("Could not start seeded server setup");
  expect(await exists(state)).toBe(false);
  expect(await exists(`${state}.instances`)).toBe(false);
});

it("forces an owned manager to exit after its shutdown deadline and removes its directory", async () => {
  const { state, options } = await fixture("stubborn");
  const instance = await startAppServer(state, options);
  await stopAppServers(state, 250);
  expect(running(instance.pid)).toBe(false);
  expect(await exists(instance.dir)).toBe(false);
  expect(await exists(`${state}.instances`)).toBe(false);
});
