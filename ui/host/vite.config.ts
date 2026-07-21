import react from "@vitejs/plugin-react";
import { tanstackRouter } from "@tanstack/router-plugin/vite";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [tanstackRouter({ target: "react", autoCodeSplitting: true }), react()],
  server: {
    proxy: {
      "/api": { target: "https://demo.fanout.test", changeOrigin: true, secure: false },
      "/mcp": { target: "https://demo.fanout.test", changeOrigin: true, secure: false },
    },
  },
  build: { outDir: "../../internal/ui/dist", emptyOutDir: true },
});
