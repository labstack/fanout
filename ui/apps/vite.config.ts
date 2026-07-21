import react from "@vitejs/plugin-react";
import { defineConfig } from "vite";
import { viteSingleFile } from "vite-plugin-singlefile";

const input = process.env.INPUT;
if (!input) throw new Error("INPUT is required");

const stripTrailingWhitespace = () => ({
  name: "strip-trailing-whitespace",
  enforce: "post" as const,
  generateBundle(_options: unknown, bundle: Record<string, { type: string; source?: string | Uint8Array }>) {
    for (const output of Object.values(bundle)) {
      if (output.type === "asset" && typeof output.source === "string") {
        output.source = output.source.replace(/[ \t]+$/gm, "");
      }
    }
  },
});

export default defineConfig({
  plugins: [react(), viteSingleFile(), stripTrailingWhitespace()],
  build: {
    cssMinify: true,
    minify: true,
    outDir: "dist",
    emptyOutDir: false,
    rollupOptions: { input },
  },
});
