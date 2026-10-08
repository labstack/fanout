import { afterEach, expect, test } from "bun:test";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";
const roots: string[] = [];
afterEach(() => roots.splice(0).forEach(root => rmSync(root, { recursive: true, force: true })));
function check(files: Record<string, string>) {
  const root = mkdtempSync(join(tmpdir(), "fanout-test-names-")); roots.push(root);
  for (const [file, text] of Object.entries(files)) { mkdirSync(dirname(join(root, file)), { recursive: true }); writeFileSync(join(root, file), text); }
  return Bun.spawnSync(["node", join(import.meta.dir, "test-names.mjs"), root]);
}
test("accepts behavioral names and specification scenario identifiers", () => {
  expect(check({ "panel.test.ts": 'it.each([1, 2])("S8 batches frames %s", () => {}); describe("service dependencies", () => {}); it("derives p99 and reveals the final action", () => {});', "scope_test.go": 'package scope\nfunc TestScopeRoundTripsValues() {}', ".scratch/final2.test.ts": 'test("F1 scratch", () => {})' }).exitCode).toBe(0);
});
test("allows fixture and finalize names but rejects complete review words", () => {
  expect(check({ "scope_test.go": 'package scope\nfunc TestFixtureX() {}\nfunc TestFinalizeX() {}' }).exitCode).toBe(0);
  for (const name of ["TestFinal", "TestFix", "BenchmarkFinal", "ExampleFix"]) {
    expect(check({ "scope_test.go": `package scope\nfunc ${name}() {}` }).exitCode).not.toBe(0);
  }
});
test("rejects review identifiers in direct and parameterized titles and Go names", () => {
  for (const id of ["V4", "V9", "P3a", "P3d", "Q1", "Q3", "H1", "F1", "F2", "F5", "W3", "I4", "R2", "B3", "N1", "Task 6", "final2", "fix3", "preview-part8"]) {
    const out = check({ "panel.test.ts": `it.each([1])("${id} panel behavior", () => {});` });
    expect(out.exitCode).not.toBe(0); expect(out.stderr.toString()).toContain("panel.test.ts");
  }
  expect(check({ "scope_test.go": 'package scope\nfunc TestI4RankingGuide() {}\nfunc TestScope(t *testing.T) { t.Run("Q1 scope", func(t *testing.T) {}) }' }).exitCode).not.toBe(0);
  expect(check({ "scope_test.go": 'package scope\nfunc TestTask2CacheBudget() {}' }).exitCode).not.toBe(0);
  for (const registration of ['describe.skipIf(false)("R2 titles", () => {})', 'it.each`values ${[1]}`("F2 titles", () => {})', 'test.concurrent.each([1])("I4 titles", () => {})']) {
    expect(check({ "panel.test.ts": registration }).exitCode).not.toBe(0);
  }
});
test("rejects review-named files while ignoring strings and comments outside test titles", () => {
  for (const file of ["fix1-table.test.tsx", "final2-map.test.tsx", "round2_test.go", "preview-part8.test.tsx", "provider_fix_test.go", "v4-units.test.ts", "p3a-grid.test.tsx", "task2-cache_test.go"]) {
    const out = check({ [file]: '// test fixture' }); expect(out.exitCode).not.toBe(0); expect(out.stderr.toString()).toContain(file);
  }
  expect(check({ "panel.test.ts": '// it("F1 comment", () => {});\nconst fixture = `it("Q1 fake", () => {})`; it("renders traces", () => {})' }).exitCode).toBe(0);
});
