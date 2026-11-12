import { test } from "../fixtures/test";
import { startAppServer } from "./app-lifecycle";

test("start disposable seeded app", async () => {
  if (process.env.PITOT_LANDING_BASE_URL || process.env.PITOT_LANDING_CAPTURES === "1") return;
  await startAppServer(process.env.PITOT_E2E_STATE!);
});
