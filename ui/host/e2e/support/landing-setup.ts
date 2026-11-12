import setup from "./global-setup";
import { writeAppState } from "./app-lifecycle";

// The capture run owns these child handles until teardown; no detached manager
// or process-table lookup is needed to stop the disposable server safely.
export default async function landingSetup() {
  const cleanup = await setup();
  try {
    await writeAppState(process.env.PITOT_E2E_STATE!, {
      env: Object.fromEntries(Object.entries(process.env).filter(([name]) => name.startsWith("PITOT_E2E_") && name !== "PITOT_E2E_STATE")),
    });
  } catch (error) { await cleanup?.(); throw error; }
  return cleanup;
}
