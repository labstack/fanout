import { execFile, spawn } from "node:child_process";
import { randomUUID } from "node:crypto";
import { mkdir, mkdtemp, readFile, readdir, rename, rm, writeFile } from "node:fs/promises";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";
import { promisify } from "node:util";
import { waitUntilReady } from "./setup-support";

type Instance = { id: string; pid: number; dir: string; command: string };
type Ready = { id: string; pid: number; env?: Record<string, string> };
const execute = promisify(execFile);
const manager = join(dirname(fileURLToPath(import.meta.url)), "app-server.ts");

async function readJSON<T>(path: string): Promise<T | undefined> {
  try { return JSON.parse(await readFile(path, "utf8")); }
  catch (error) { if ((error as NodeJS.ErrnoException).code !== "ENOENT") throw error; }
}

export async function writeAppState(path: string, state: unknown) {
  await writeFile(`${path}.ready`, JSON.stringify(state), { mode: 0o600 });
  await rename(`${path}.ready`, path);
}

async function commandFor(pid: number) {
  try { return (await execute("ps", ["-p", String(pid), "-o", "args="])).stdout.trim() || undefined; }
  catch (error) {
    if ((error as { code?: number }).code === 1) return;
    throw new Error("Could not verify seeded server process ownership");
  }
}
function owns(instance: Instance, command: string) {
  return command.includes(instance.command) && command.includes(` ${instance.id} `) && command.includes(instance.dir);
}
async function stopInstance(instance: Instance, timeout: number) {
  const command = await commandFor(instance.pid);
  if (command) {
    if (!owns(instance, command)) throw new Error(`Refusing to stop unowned process ${instance.pid}`);
    try { process.kill(instance.pid, "SIGTERM"); }
    catch (error) { if ((error as NodeJS.ErrnoException).code !== "ESRCH") throw error; }
    const stopped = async () => {
      const current = await commandFor(instance.pid);
      return !current || !owns(instance, current) ? undefined : "Seeded server is still stopping";
    };
    try { await waitUntilReady(Date.now() + timeout, stopped, 50); }
    catch {
      if (await stopped()) {
        try { process.kill(instance.pid, "SIGKILL"); }
        catch (error) { if ((error as NodeJS.ErrnoException).code !== "ESRCH") throw error; }
      }
      await waitUntilReady(Date.now() + timeout, stopped, 50);
    }
  }
  await rm(instance.dir, { recursive: true, force: true });
}

export async function stopAppServers(path: string, timeout = 10_000) {
  const registry = `${path}.instances`;
  const entries = await readdir(registry).catch((error: NodeJS.ErrnoException) => { if (error.code !== "ENOENT") throw error; return []; });
  const failures: unknown[] = [];
  for (const name of entries) {
    try {
      const instance = await readJSON<Instance>(join(registry, name));
      if (instance) await stopInstance(instance, timeout);
      await rm(join(registry, name), { force: true });
    } catch (error) { failures.push(error); }
  }
  if (failures.length) throw new AggregateError(failures, "Could not stop every owned seeded server");
  await rm(registry, { recursive: true, force: true });
  await rm(path, { force: true });
  await rm(`${path}.ready`, { force: true });
}

export async function startAppServer(path: string, { command = ["bun", manager], timeout = 300_000 }: { command?: string[]; timeout?: number } = {}) {
  await stopAppServers(path);
  const registry = `${path}.instances`;
  await mkdir(registry, { recursive: true, mode: 0o700 });
  const dir = await mkdtemp(join(tmpdir(), "fanout-e2e-"));
  const id = randomUUID();
  const child = spawn(command[0], [...command.slice(1), id, dir], { stdio: ["ignore", "inherit", "inherit"] });
  let failed = false;
  child.once("error", () => { failed = true; });
  child.unref();
  try {
    if (!child.pid) throw new Error("Could not start seeded server setup");
    const instance: Instance = { id, pid: child.pid, dir, command: command[1] };
    // Register before polling; teardown retains every launch even if shared state is replaced.
    await writeAppState(join(registry, id), instance);
    let ready: Ready;
    const deadline = Date.now() + timeout;
    for (;;) {
      if (failed || child.exitCode !== null || child.signalCode !== null) throw new Error("Seeded server setup exited before readiness");
      const candidate = await readJSON<Ready>(join(dir, "ready.json"));
      // The launch directory is new, so another launch's readiness can never become valid.
      if (candidate && (candidate.id !== id || candidate.pid !== child.pid)) throw new Error("Seeded server reported readiness for another launch");
      if (candidate?.env) { ready = candidate; break; }
      if (Date.now() >= deadline) throw new Error("Timed out waiting for the spawned seeded server");
      await new Promise(resolve => setTimeout(resolve, 50));
    }
    await writeAppState(path, { ...instance, env: ready.env });
    return { ...instance, env: ready.env! };
  } catch (error) {
    if (child.pid) await stopInstance({ id, pid: child.pid, dir, command: command[1] }, 10_000);
    await rm(join(registry, id), { force: true });
    await rm(join(registry, `${id}.ready`), { force: true });
    await stopAppServers(path);
    await rm(dir, { recursive: true, force: true });
    throw error;
  }
}
