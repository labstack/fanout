import type { Plugin } from "vite";

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
        if (/\/src\/(?:auth[\w-]*(?:\.[jt]sx?$|\/)|router(?:\.[jt]sx?|\/)|routes\/|(?:App|main)\.[jt]sx?$|(?:app-context|provider-context)(?:\.[jt]sx?|\/)|(?:app|providers?)\/|(?:api|observability|mcp-app-frame)\.[jt]sx?$|dashboards\/api\.[jt]sx?$)/.test(path)) this.error(`Forbidden MCP app import: ${[...chain, id].join(" -> ")}`);
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
