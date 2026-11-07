import { themeAlias } from "../../build/theme.ts";
import { defineConfig } from "vite";
export default defineConfig({ resolve: { alias: themeAlias }, root: "e2e/host", publicDir: false, build: { emptyOutDir: true } });
