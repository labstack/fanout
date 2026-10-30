import { afterEach, expect, test } from "bun:test";
import { copyFileSync, mkdirSync, mkdtempSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { join, resolve } from "node:path";

const roots: string[] = [];
afterEach(() => {
  for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true });
});

function check(html: string) {
  const root = mkdtempSync(join(tmpdir(), "fanout-site-tables-"));
  roots.push(root);
  mkdirSync(join(root, "scripts"));
  mkdirSync(join(root, "dist"));
  const script = join(root, "scripts/check-tables.mjs");
  copyFileSync(resolve(import.meta.dir, "../site/scripts/check-tables.mjs"), script);
  writeFileSync(join(root, "dist/reference.html"), html);
  return Bun.spawnSync([process.execPath, script]);
}

test("table checker accepts Markdown whitespace inside a scroll wrapper", () => {
  const result = check('<div class="table-wrap" tabindex="0" role="region" aria-label="Reference table, scrollable">' + "\n".repeat(300) + "<table><tr><td>field</td></tr></table></div>");
  expect(result.exitCode).toBe(0);
  expect(result.stdout.toString()).toContain("1 table(s) across 1 page(s), all wrapped");
});

test("table checker rejects a bare table after a closed scroll wrapper", () => {
  const result = check('<div class="table-wrap"></div>' + "\n".repeat(300) + "<table><tr><td>field</td></tr></table>");
  expect(result.exitCode).toBe(1);
  expect(result.stderr.toString()).toContain("reference.html: 1 of 1");
});
