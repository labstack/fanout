import { configDefaults, defineConfig } from "vitest/config";

export default defineConfig({
  server: { fs: { allow: [".."] } },
  test: { environment: "happy-dom", globals: true, include: ["**/*.{test,spec}.?(c|m)[jt]s?(x)", "../panels/**/*.test.ts"], setupFiles: ["./tests/setup.ts"], exclude: [...configDefaults.exclude, "e2e/**/*.spec.ts"] },
});
