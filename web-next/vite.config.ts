import path from "path";
import { defineConfig } from "vite";
import react from "@vitejs/plugin-react";
import tailwindcss from "@tailwindcss/vite";

export default defineConfig({
  plugins: [react(), tailwindcss()],
  server: {
    port: 7523,
    host: true,
    proxy: {
      "/api": { target: "http://localhost:7520", changeOrigin: true },
      "/mcp": { target: "http://localhost:7520", changeOrigin: true },
    },
  },
  resolve: { alias: { "@": path.resolve(__dirname, "./src") } },
});
