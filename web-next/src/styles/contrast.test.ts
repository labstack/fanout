// @vitest-environment node
//
// The suite defaults to jsdom (vitest.config.ts). Under jsdom, the global
// `URL` constructor resolves relative URLs against `window.location`
// (http://localhost:3000/) instead of the module's file location, so
// `new URL("./tokens.css", import.meta.url)` silently points at the wrong
// scheme. This file only reads tokens.css off disk, so run it under node.
import { describe, it, expect } from "vitest";
import { readFileSync } from "node:fs";
import { fileURLToPath } from "node:url";

// Minimal WCAG relative-luminance contrast, hex → ratio.
function ratio(hex1: string, hex2: string): number {
  const lum = (hex: string) => {
    const n = hex.replace("#", "");
    const ch = [0, 2, 4].map((i) => parseInt(n.slice(i, i + 2), 16) / 255);
    const lin = ch.map((c) => (c <= 0.03928 ? c / 12.92 : ((c + 0.055) / 1.055) ** 2.4));
    const [r, g, b] = lin as [number, number, number];
    return 0.2126 * r + 0.7152 * g + 0.0722 * b;
  };
  const a = lum(hex1), b = lum(hex2);
  const [hi, lo] = a > b ? [a, b] : [b, a];
  return (hi + 0.05) / (lo + 0.05);
}

// Pull a token value out of tokens.css by name within the :root{...} light block.
function token(css: string, name: string): string {
  const m = css.match(new RegExp(`${name}:\\s*(#[0-9a-fA-F]{6})`));
  if (!m) throw new Error(`token ${name} not found`);
  const hex = m[1];
  if (!hex) throw new Error(`token ${name} matched but captured no hex value`);
  return hex;
}

const css = readFileSync(
  fileURLToPath(new URL("./tokens.css", import.meta.url)),
  "utf8",
);

describe("light-mode token contrast (WCAG)", () => {
  it("body/muted text clears 4.5:1 on paper", () => {
    expect(ratio(token(css, "--ink"), token(css, "--paper"))).toBeGreaterThanOrEqual(4.5);
    expect(ratio(token(css, "--ink-2"), token(css, "--paper"))).toBeGreaterThanOrEqual(4.5);
  });
  it("status TEXT ramp clears 4.5:1 on paper", () => {
    for (const t of ["--ok-text", "--warn-text", "--crit-text", "--info-text"]) {
      expect(ratio(token(css, t), token(css, "--paper"))).toBeGreaterThanOrEqual(4.5);
    }
  });
});
