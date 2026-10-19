import { build } from "vite";
import { createServer } from "node:http";
import { mkdtemp, readFile, rm } from "node:fs/promises";
import { tmpdir } from "node:os";
import { join, resolve, extname } from "node:path";

const output = await mkdtemp(join(tmpdir(), "fanout-mcp-host-"));
process.env.FANOUT_APPS_OUT = join(output, "apps");
try {
  await build({ configFile: "vite.apps.config.ts" });
  await build({ configFile: "e2e/host/vite.config.ts", build: { outDir: join(output, "host") } });
} catch (error) { await rm(output, { recursive: true, force: true }); throw error; }
const types = { ".html": "text/html", ".js": "text/javascript", ".css": "text/css", ".json": "application/json" };
const server = createServer(async (request, response) => {
  response.setHeader("Cache-Control", "no-store");
  const pathname = new URL(request.url, "http://localhost").pathname;
  const file = pathname === "/panels.html" ? join(output, "apps/panels.html") : resolve(output, "host", `.${pathname === "/" ? "/index.html" : pathname}`);
  if (!file.startsWith(output + "/")) { response.writeHead(403).end(); return; }
  try { response.setHeader("Content-Type", types[extname(file)] ?? "application/octet-stream"); response.end(await readFile(file)); }
  catch { response.writeHead(404).end(); }
});
server.listen(0, "127.0.0.1", () => console.log(`listening on ${server.address().port}`));
process.on("SIGTERM", () => server.close(async () => { await rm(output, { recursive: true, force: true }); process.exit(0); }));
