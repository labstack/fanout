import { afterEach, expect, test } from "bun:test";
import { mkdtempSync, mkdirSync, rmSync, writeFileSync } from "node:fs";
import { tmpdir } from "node:os";
import { dirname, join } from "node:path";

const roots: string[] = [];
const checker = join(import.meta.dir, "ui-boundaries.mjs");
afterEach(() => { for (const root of roots.splice(0)) rmSync(root, { recursive: true, force: true }); });

function check(files: Record<string, string>) {
  const root = mkdtempSync(join(tmpdir(), "fanout-boundary-"));
  roots.push(root);
  for (const [path, source] of Object.entries(files)) {
    const file = join(root, "ui", path);
    mkdirSync(dirname(file), { recursive: true });
    writeFileSync(file, source);
  }
  return Bun.spawnSync([process.execPath, checker, root]);
}

test.each([
  ["shared/test.css", '@import "../host/src/x.css";'],
  ["shared/test.css", '@import url(../host/src/x.css);'],
  ["shared/test.html", '<script src="../host/src/x.js"></script>'],
  ["shared/test.html", '<script>import "react";</script>'],
  ["panels/test.ts", 'import type { X } from "../host/node_modules/react";'],
  ["panels/test.ts", 'const x=require("react");'],
  ["panels/test.ts", 'const x=import(`react`);'],
  ["panels/test.ts", 'type X = import("react").ReactNode;'],
  ["panels/test.ts", 'import type { X } from "react";'],
  ["panels/test.ts", 'export type { X } from "@mantine/core";'],
  ["panels/test.ts", 'import "react";'],
  ["panels/test.ts", 'const x = import("echarts/core");'],
  ["panels/test.ts", 'import { X } from "../host/src/x";'],
  ["tokens.ts", 'import { X } from "react";'],
  ["shared/test.ts", 'import { X } from "react";'],
])("rejects forbidden imports in %s: %s", (file, source) => {
  const result = check({ [file]: source });
  expect(result.exitCode).toBe(1);
  expect(result.stderr.toString()).toContain(`ui/${file}`);
});

test("allows workspace packages and package-free shared imports; skips comments and dependencies", () => {
  expect(check({
    "host/src/test.ts": 'import { X } from "react"; import { Y } from "../../panels/test";',
    "host/src/mcp-apps/test.ts": 'import { X } from "@mantine/core"; import { Y } from "../dashboards/test";',
    "panels/test.ts": 'export type { Y } from "./types"; import { X } from "../tokens"; // import "react";\n/* export { X } from "react"; */ const text = \'import "react";\';',
    "tokens.ts": 'export const X = 1; const regex = /[&<>"\']/g; const template = `${X} text import "react";`;',
    "host/src/test.css": '.x { color: #fff; } /* @import "../outside/x.css"; */',
    "host/node_modules/test/index.ts": 'import { X } from "../../../outside/src/x";',
  }).exitCode).toBe(0);
});
