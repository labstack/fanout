// @vitest-environment node
import { mkdtempSync, writeFileSync, mkdirSync, rmSync, readFileSync, readdirSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { build } from "vite";
import { expect, it } from "vitest";
import { appBuildBoundary, blobWorkerOnly } from "../build/apps";

it.each(["auth.ts", "auth-session.ts", "api.ts", "observability.ts", "mcp-app-frame.tsx", "router.ts", "routes/index.ts", "App.tsx", "main.tsx", "app-context.tsx", "provider-context.tsx", "dashboards/api.ts"].flatMap(file => [false, true].map(transitive => ({ file, transitive }))))("rejects forbidden runtime imports: $file transitive=$transitive", async ({ file, transitive }) => {
  const root = mkdtempSync(resolve(tmpdir(), "fanout-graph-"));
  try {
    mkdirSync(resolve(root, "src/mcp-apps"), { recursive: true }); mkdirSync(resolve(root, "src/dashboards"));
    mkdirSync(resolve(root, "src", file, ".."), { recursive: true });
    writeFileSync(resolve(root, "src", file), "export const value=1;");
    writeFileSync(resolve(root, "src/dashboards/shared.ts"), `export {value} from '../${file}';`);
    writeFileSync(resolve(root, "src/mcp-apps/main.ts"), `import {value} from '${transitive ? "../dashboards/shared" : `../${file}`}';console.log(value);`);
    await expect(build({ configFile: false, root, plugins: [appBuildBoundary()], build: { write: false, rollupOptions: { input: "src/mcp-apps/main.ts" } } })).rejects.toThrow(/Forbidden MCP app import/);
  } finally { rmSync(root, { recursive: true, force: true }); }
});
it("permits shared rendering and erased API contracts", async () => {
  const root = mkdtempSync(resolve(tmpdir(), "fanout-graph-"));
  try {
    mkdirSync(resolve(root, "src/mcp-apps"), { recursive: true }); mkdirSync(resolve(root, "src/dashboards"));
    writeFileSync(resolve(root, "src/dashboards/api.ts"), "export type QueryBody={name:string}; export const secret=1;");
    writeFileSync(resolve(root, "src/dashboards/shared.ts"), "export const render=()=>1;");
    writeFileSync(resolve(root, "src/mcp-apps/main.ts"), "import type {QueryBody} from '../dashboards/api';import {render} from '../dashboards/shared';console.log(render());");
    await expect(build({ configFile: false, root, plugins: [appBuildBoundary()], build: { write: false, rollupOptions: { input: "src/mcp-apps/main.ts" } } })).resolves.toBeDefined();
  } finally { rmSync(root, { recursive: true, force: true }); }
});
it("builds the real app graph into one self-contained HTML artifact", async () => {
  const dir = mkdtempSync(resolve(tmpdir(), "fanout-app-"));
  const previousEnvironment = process.env.NODE_ENV; process.env.NODE_ENV = "production";
  try {
    await build({ configFile: resolve("vite.apps.config.ts"), build: { outDir: dir } });
    expect(readdirSync(dir)).toEqual(["panels.html"]);
    const html = readFileSync(resolve(dir, "panels.html"), "utf8");
    const markup = html.replace(/(<script\b[^>]*>)[\s\S]*?<\/script[^>]*>/gi, "$1</script>").replace(/(<style\b[^>]*>)[\s\S]*?<\/style>/g, "$1</style>");
    expect(/<script[^>]+src=|<link[^>]+(?:stylesheet|modulepreload)/.test(markup)).toBe(false);
    expect(/(?:src|href)=["']https?:/.test(markup)).toBe(false);
    expect(html).not.toContain("data:text/javascript");
    expect(html).toContain("createObjectURL"); expect(html).toContain("revokeObjectURL"); expect(html).toContain("data:font/");
    expect(html).not.toContain("chat-composer"); expect(html).not.toContain("rail-row");
  } finally { if (previousEnvironment === undefined) delete process.env.NODE_ENV; else process.env.NODE_ENV = previousEnvironment; rmSync(dir, { recursive: true, force: true }); }
}, 30_000);

it("executes the Vite-emitted worker wrapper using a Blob URL and surfaces initialization failure", async () => {
  const root = mkdtempSync(resolve(tmpdir(), "fanout-worker-"));
  try {
    writeFileSync(resolve(root, "map.worker.ts"), "self.onmessage=()=>self.postMessage({nodes:[]});");
    writeFileSync(resolve(root, "main.ts"), "import MapWorker from './map.worker?worker&inline';new MapWorker();");
    const result = await build({ configFile: false, root, plugins: [blobWorkerOnly()], build: { write: false, minify: false, rollupOptions: { input: "main.ts", output: { format: "iife" } } } });
    if (Array.isArray(result) || !("output" in result)) throw new Error("Expected one bundle");
    const chunk = result.output.find(o => o.type === "chunk");
    if (!chunk || chunk.type !== "chunk") throw new Error("Expected a worker wrapper chunk");
    expect(chunk.code).not.toContain("data:text/javascript"); expect(result.output).toHaveLength(1);
    const urls: string[] = []; const URL = { createObjectURL: () => "blob:map", revokeObjectURL() {} };
    class Worker { constructor(url: string) { urls.push(url); } addEventListener() {} }
    const self = { Blob, URL };
    new Function("self", "Blob", "Worker", chunk.code)(self, Blob, Worker);
    expect(urls).toEqual(["blob:map"]);
    self.URL = { ...URL, createObjectURL: () => { throw new Error("Blob blocked"); } };
    expect(() => new Function("self", "Blob", "Worker", chunk.code)(self, Blob, Worker)).toThrow("Blob blocked");
    expect(urls).toEqual(["blob:map"]);
  } finally { rmSync(root, { recursive: true, force: true }); }
});
