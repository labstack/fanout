import { readdirSync, readFileSync } from "node:fs";
import { join, resolve } from "node:path";
import { DEFAULT_THEME } from "@mantine/core";
import { expect, it } from "vitest";
import { fanoutTheme } from "./theme";

it("uses only defined theme colors in host props and Mantine palette references", () => {
  const palettes = new Set([...Object.keys(DEFAULT_THEME.colors), ...Object.keys(fanoutTheme.colors ?? {})]);
  const root = resolve("src");
  const failures: string[] = [];
  function visit(dir: string) {
    for (const item of readdirSync(dir, { withFileTypes: true })) {
      const path = join(dir, item.name);
      if (item.isDirectory()) { visit(path); continue; }
      if (!/\.(tsx?|css)$/.test(path)) continue;
      const source = readFileSync(path, "utf8");
      for (const match of source.matchAll(/\bcolor\s*=\s*(?:\{\s*)?["']([^"']+)["']/g)) {
        const value = match[1];
        if (value.startsWith("var(")) continue;
        if (!palettes.has(value) && !["white", "black"].includes(value)) failures.push(`${path}: color=${value}`);
      }
      for (const match of source.matchAll(/var\(--mantine-color-([a-z][a-z0-9]*)-([a-z0-9-]+)/g)) {
        // Mantine's default surface variables are semantic roles, not palettes.
        if (!palettes.has(match[1]) && match[1] !== "default") failures.push(`${path}: ${match[0]}`);
      }
    }
  }
  visit(root);
  expect(failures).toEqual([]);
});
