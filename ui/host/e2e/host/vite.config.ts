import { defineConfig } from "vite";
export default defineConfig({ root: "e2e/host", publicDir: false, build: { emptyOutDir: true } });
