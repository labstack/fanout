import { configDefaults, defineConfig } from "vitest/config";

export default defineConfig({
  test: { environment: "happy-dom", setupFiles: ["./tests/setup.ts"], exclude: [...configDefaults.exclude, "e2e/**/*.spec.ts"] },
});
