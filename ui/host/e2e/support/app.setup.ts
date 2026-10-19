import { test } from "../fixtures/test";
import { startAppServer } from "./app-lifecycle";

test("start disposable seeded app", async () => {
  if (process.env.FANOUT_LANDING_BASE_URL) return;
  await startAppServer(process.env.FANOUT_E2E_STATE!);
});
