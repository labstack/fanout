import { readFile, rm } from "node:fs/promises";
import { waitUntilReady } from "./setup-support";

export default async function teardown() {
  const path = process.env.FANOUT_E2E_STATE;
  if (!path) return;
  const state = await readFile(path, "utf8").then(JSON.parse, () => undefined);
  if (!state) return;
  try { process.kill(state.pid, "SIGTERM"); } catch { await rm(path, { force: true }); return; }
  await waitUntilReady(Date.now() + 10_000, async () => readFile(path).then(() => "Seeded server is still stopping", () => undefined), 100);
  await rm(path, { force: true });
}
