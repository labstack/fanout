// @vitest-environment node
import { mkdtempSync, writeFileSync, mkdirSync, rmSync, readFileSync, readdirSync, existsSync } from "node:fs";
import { tmpdir } from "node:os";
import { resolve } from "node:path";
import { build } from "vite";
import { expect, it } from "vitest";
import { appBuildBoundary, blobWorkerOnly } from "../../vite.apps.config";

it.each(["auth.ts", "router.ts", "routes/index.ts", "App.tsx", "main.tsx", "app-context.tsx", "provider-context.tsx", "dashboards/api.ts"].flatMap(file => [false, true].map(transitive => ({ file, transitive }))))("rejects forbidden runtime imports: $file transitive=$transitive", async ({ file, transitive }) => {
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
it("constructs emitted inline workers from Blob URLs and never falls back to data: under CSP", () => {
  const plugin = blobWorkerOnly();
  const transform = plugin.transform as (code: string, id: string) => { code: string } | undefined;
  const source = 'export default function WorkerWrapper(options) { let objURL;try { objURL=URL.createObjectURL(new Blob(["self.onmessage=()=>{}"],{type:"text/javascript"})); return new Worker(objURL,options); } catch { return new Worker("data:text/javascript;base64,AA", options); } finally { if(objURL)URL.revokeObjectURL(objURL); } }';
  const code = transform(source, "map.worker.ts?worker&inline")!.code.replace("export default", "return");
  const urls: string[] = []; const URL = { createObjectURL: () => "blob:map", revokeObjectURL: () => undefined };
  class Worker { constructor(url: string) { urls.push(url); } }
  new (new Function("Blob", "URL", "Worker", code)(Blob, URL, Worker))();
  expect(urls).toEqual(["blob:map"]);
  const failingURL = { ...URL, createObjectURL: () => { throw new Error("CSP blocked"); } };
  expect(() => new (new Function("Blob", "URL", "Worker", code)(Blob, failingURL, Worker))()).toThrow("CSP blocked");
  expect(urls).toEqual(["blob:map"]);
});
it.skipIf(!existsSync(resolve("../../.superpowers/build/m3-apps/panels.html")))("stages exactly one self-contained HTML artifact when built", () => {
  const dir = resolve("../../.superpowers/build/m3-apps");
  expect(readdirSync(dir)).toEqual(["panels.html"]);
  const html = readFileSync(resolve(dir, "panels.html"), "utf8");
  expect(html).not.toMatch(/<script[^>]+src=|<link[^>]+(?:stylesheet|modulepreload)/);
  expect(html).not.toMatch(/new Worker\([^)]*["']data:/);
  expect(html).toContain("createObjectURL"); expect(html).toContain("revokeObjectURL"); expect(html).toContain("data:font/");
});

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
