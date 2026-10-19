import { stopAppServers } from "./app-lifecycle";

export default async function teardown() {
  const path = process.env.FANOUT_E2E_STATE;
  if (path) await stopAppServers(path);
}
