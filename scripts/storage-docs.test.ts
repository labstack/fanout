import { expect, test } from "bun:test";
import { readFileSync } from "node:fs";
test("storage documentation describes trace caches and direct log and endpoint scans", () => {
  for (const file of ["site/src/content/docs/explanation/storage-model.mdx", "internal/config/config.go", "site/src/content/docs/reference/settings/storage.mdx"]) {
    const text = readFileSync(new URL("../" + file, import.meta.url), "utf8");
    expect(text).not.toMatch(/Completed-batch endpoint, log, and trace caches|Completed-batch endpoint, log,\s*\/\/ and trace caches|Log histograms merge minute counts|Endpoints merge fixed-boundary duration histograms|private cache stores minute aggregates/);
    expect(text).toMatch(/Completed-batch trace caches/);
  }
});
