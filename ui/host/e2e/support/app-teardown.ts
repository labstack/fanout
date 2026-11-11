import { stopAppServers } from "./app-lifecycle";

export default async function teardown() {
  const path = process.env.PITOT_E2E_STATE;
  if (path) await stopAppServers(path);
}
