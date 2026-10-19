import { join } from "node:path";
import setup from "./global-setup";
import { writeAppState } from "./app-lifecycle";

const [id, dir] = process.argv.slice(2);
const ready = join(dir, "ready.json");
await writeAppState(ready, { id, pid: process.pid });
const cleanup = await setup({ signalCleanup: "startup", directory: dir });
await writeAppState(ready, { id, pid: process.pid, env: Object.fromEntries(Object.entries(process.env).filter(([name]) => name.startsWith("FANOUT_E2E_") && name !== "FANOUT_E2E_STATE")) });
let stopping = false;
async function stop() {
  if (stopping) return;
  stopping = true;
  await cleanup?.();
  process.exit(0);
}
process.on("SIGTERM", stop);
process.on("SIGINT", stop);
setInterval(() => {}, 60_000);
