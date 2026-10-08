import react from "@vitejs/plugin-react";
import { defineConfig, type Plugin } from "vite";
import { viteSingleFile } from "vite-plugin-singlefile";

export function appBuildBoundary(): Plugin {
  return {
    name: "mcp-app-runtime-boundary",
    generateBundle() {
      const entries = [...this.getModuleIds()].filter(id => /\/src\/mcp-apps\/main\.[jt]sx?(?:\?|$)/.test(id));
      if (!entries.length) this.error("MCP app entry was not found");
      const seen = new Set<string>();
      const walk = (id: string, chain: string[]) => {
        if (seen.has(id)) return; seen.add(id);
        const path = id.replaceAll("\\", "/").split("?")[0];
        if (/\/src\/(?:auth(?:\.[jt]sx?|\/)|router(?:\.[jt]sx?|\/)|routes\/|(?:App|main)\.[jt]sx?$|(?:app-context|provider-context)(?:\.[jt]sx?|\/)|(?:app|providers?)\/|dashboards\/api\.[jt]sx?$)/.test(path)) this.error(`Forbidden MCP app import: ${[...chain, id].join(" -> ")}`);
        const info = this.getModuleInfo(id);
        for (const next of [...(info?.importedIds ?? []), ...(info?.dynamicallyImportedIds ?? [])]) walk(next, [...chain, id]);
      };
      entries.forEach(id => walk(id, []));
    },
  };
}

// Vite's inline worker wrapper normally falls back to data: on a Blob error.
// Opaque-origin MCP frames permit only blob: workers: surface that failure to
// ServiceMapViz instead of attempting a forbidden second worker path.
export function blobWorkerOnly(): Plugin {
  return {
    name: "mcp-app-blob-worker-only", enforce: "post",
    transform(code, id) {
      if (!id.includes("?worker&inline") || !code.includes("data:text/javascript")) return;
      const next = code.replace(/catch\s*(?:\([^)]*\))?\s*\{\s*return new Worker\([\s\S]*?\);\s*\}/, 'catch (cause) { throw cause; }');
      if (next === code || next.includes("data:text/javascript")) this.error("Inline worker fallback could not be removed");
      return { code: next, map: null };
    },
  };
}

export default defineConfig({
  plugins: [react(), appBuildBoundary(), blobWorkerOnly(), viteSingleFile(), {
    name: "strip-trailing-whitespace", enforce: "post",
    generateBundle(_options, bundle) { for (const output of Object.values(bundle)) { if (output.type === "asset" && typeof output.source === "string") output.source = output.source.replace(/[ \t]+$/gm, ""); } },
  }],
  publicDir: false,
  build: { outDir: process.env.FANOUT_APPS_OUT ?? "../../.superpowers/build/m3-apps", emptyOutDir: true, assetsInlineLimit: 100_000, cssMinify: true, minify: true, rollupOptions: { input: "panels.html" } },
});
