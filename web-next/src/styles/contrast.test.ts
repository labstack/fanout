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

// Pull a token value out of a CSS block by name, e.g. "--ink: #14171d".
function token(css: string, name: string): string {
  const m = css.match(new RegExp(`${name}:\\s*(#[0-9a-fA-F]{6})`));
  if (!m) throw new Error(`token ${name} not found`);
  const hex = m[1];
  if (!hex) throw new Error(`token ${name} matched but captured no hex value`);
  return hex;
}

// Slice out the `{ ... }` body immediately following the first occurrence of
// `marker` — used to scope token() lookups to a specific :root block instead
// of matching the first (light-mode) definition in the file.
function block(css: string, marker: string): string {
  const markerIdx = css.indexOf(marker);
  if (markerIdx === -1) throw new Error(`marker ${marker} not found`);
  const start = css.indexOf("{", markerIdx);
  const end = css.indexOf("}", start);
  if (start === -1 || end === -1) throw new Error(`block for ${marker} not found`);
  return css.slice(start, end);
}

const css = readFileSync(
  fileURLToPath(new URL("./tokens.css", import.meta.url)),
  "utf8",
);

// The explicit `:root[data-theme="dark"]` toggle block — same values as the
// `@media (prefers-color-scheme: dark)` block, but a simpler, unambiguous
// anchor to slice on.
const darkCss = block(css, ':root[data-theme="dark"]');

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

describe("dark-mode token contrast (WCAG)", () => {
  it("body/muted text clears 4.5:1 on paper", () => {
    expect(ratio(token(darkCss, "--ink"), token(darkCss, "--paper"))).toBeGreaterThanOrEqual(4.5);
    expect(ratio(token(darkCss, "--ink-2"), token(darkCss, "--paper"))).toBeGreaterThanOrEqual(4.5);
  });
  it("status TEXT ramp clears 4.5:1 on paper", () => {
    for (const t of ["--ok-text", "--warn-text", "--crit-text", "--info-text"]) {
      expect(ratio(token(darkCss, t), token(darkCss, "--paper"))).toBeGreaterThanOrEqual(4.5);
    }
  });
});
