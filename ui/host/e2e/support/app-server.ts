import { rmSync } from "node:fs";
import { writeFile, rename, rm } from "node:fs/promises";
import setup from "./global-setup";

const state = process.env.FANOUT_E2E_STATE!;
await writeFile(state, JSON.stringify({ pid: process.pid }), { mode: 0o600 });
const cleanup = await setup({ signalCleanup: "startup", onSignal: () => rmSync(state, { force: true }) });
await writeFile(`${state}.ready`, JSON.stringify({ pid: process.pid, env: Object.fromEntries(Object.entries(process.env).filter(([name]) => name.startsWith("FANOUT_E2E_") && name !== "FANOUT_E2E_STATE")) }), { mode: 0o600 });
await rename(`${state}.ready`, state);
let stopping = false;
async function stop() {
  if (stopping) return;
  stopping = true;
  await cleanup?.();
  await rm(state, { force: true });
  process.exit(0);
}
process.on("SIGTERM", stop);
process.on("SIGINT", stop);
setInterval(() => {}, 60_000);
