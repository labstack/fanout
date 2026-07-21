import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";
import { defineConfig } from "vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    proxy: {
      "/api": { target: "https://demo.fanout.test", changeOrigin: true, secure: false },
      "/mcp": { target: "https://demo.fanout.test", changeOrigin: true, secure: false },
    },
  },
  build: { outDir: "../../internal/ui/dist", emptyOutDir: true },
});
