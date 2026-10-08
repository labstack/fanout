import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import { viteSingleFile } from "vite-plugin-singlefile";

import { appBuildBoundary, blobWorkerOnly } from "./build/apps.ts";

export default defineConfig({
  plugins: [react(), appBuildBoundary(), blobWorkerOnly(), viteSingleFile(), {
    name: "strip-trailing-whitespace", enforce: "post",
    generateBundle(_options, bundle) { for (const output of Object.values(bundle)) { if (output.type === "asset" && typeof output.source === "string") output.source = output.source.replace(/[ \t]+$/gm, ""); } },
  }],
  publicDir: false,
  build: { outDir: process.env.FANOUT_APPS_OUT ?? "../../.superpowers/build/m3-apps", emptyOutDir: true, assetsInlineLimit: 100_000, cssMinify: true, minify: true, rollupOptions: { input: "panels.html" } },
});
