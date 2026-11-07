import { DEFAULT_THEME, defaultCssVariablesResolver, mergeMantineTheme } from "@mantine/core";
import { contrastRatio } from "@labstack/ui";
import { describe, expect, it } from "vitest";
import { fanoutCssVariables, fanoutTheme, chart } from "../../theme";
import { brand, fonts, surfaces, typeScale } from "../../tokens";

const theme = mergeMantineTheme(DEFAULT_THEME, fanoutTheme);
function variables(scheme: "light" | "dark") {
  const base = defaultCssVariablesResolver(theme), custom = fanoutCssVariables(theme);
  const values: Record<string, string> = { ...base.variables, ...base[scheme], ...custom.variables, ...custom[scheme] };
  const resolve = (value: string): string => value.startsWith("var(") ? resolve(values[value.slice(4, -1)]) : value;
  return (name: string) => resolve(values[name]);
}

it("uses Geist for UI and headings and Geist Mono for data", () => {
  expect(theme.fontFamily).toBe(fonts.body);
  expect(theme.headings.fontFamily).toBe(fonts.body);
  expect(theme.headings.fontWeight).toBe("600");
  expect(theme.fontFamilyMonospace).toBe(fonts.display);
});
it("sets the Console scale, soft radii and compact controls", () => {
  expect(typeScale).toEqual({ micro: 11, small: 12, body: 14, title: 16, section: 20, page: 28 });
  expect(theme.headings.sizes.h1.fontSize).toBe("1.75rem");
  expect(theme.headings.sizes.h2.fontSize).toBe("1.25rem");
  expect(theme.radius).toMatchObject({ sm: "4px", md: "8px", lg: "12px" });
  expect(theme.components.Button.defaultProps).toMatchObject({ size: "sm", radius: 4 });
});
describe.each(["light", "dark"] as const)("%s scheme", scheme => {
  const value = variables(scheme);
  it("keeps the specified surfaces and chart chrome in sync", () => {
    expect(value("--mantine-color-body")).toBe(surfaces[scheme].ground);
    expect(value("--mantine-color-default")).toBe(chart[scheme].surface);
    expect(value("--mantine-color-dimmed")).toBe(surfaces[scheme].muted);
    expect(value("--labstack-control-border")).toBe(surfaces[scheme].controlBorder);
  });
  it("reads iris text at 4.5:1 and selected edges at 3:1", () => {
    const accent = brand[scheme === "dark" ? 5 : 7];
    for (const background of Object.values(surfaces[scheme]).slice(0, 4)) {
      expect(contrastRatio(accent, background)).toBeGreaterThanOrEqual(4.5);
      expect(contrastRatio(accent, background)).toBeGreaterThanOrEqual(3);
    }
    expect(value("--mantine-color-anchor")).toBe(accent);
    expect(value("--mantine-primary-color-filled")).toBe(surfaces[scheme].text);
    expect(value("--fanout-focus")).toBe(accent);
    expect(contrastRatio(surfaces[scheme].muted, value("--mantine-color-iris-light"))).toBeGreaterThanOrEqual(4.5);
  });
  it("uses token surfaces for neutral variants and hover", () => {
    expect(value("--mantine-color-default-hover")).toBe(surfaces[scheme].hover);
    for (const color of ["brand", "gray", "dark", "ink"]) {
      expect(value(`--mantine-color-${color}-light`)).toBe(surfaces[scheme].inset);
      expect(value(`--mantine-color-${color}-light-hover`)).toBe(surfaces[scheme].hover);
      expect(value(`--mantine-color-${color}-light-color`)).toBe(surfaces[scheme].text);
    }
  });
  it("reads ink and every semantic filled surface at AA at rest and hover", () => {
    for (const color of ["brand", "ok", "warn", "bad", "info"]) {
      const text = value(`--labstack-color-${color}-contrast`);
      for (const suffix of ["filled", "filled-hover"]) expect(contrastRatio(text, value(`--mantine-color-${color}-${suffix}`))).toBeGreaterThanOrEqual(4.5);
    }
    expect(value("--mantine-color-brand-filled")).toBe(surfaces[scheme].text);
  });
  it("reads Degraded and all status labels at AA", () => {
    for (const color of ["ok", "warn", "bad", "info"]) {
      for (const background of [surfaces[scheme].ground, surfaces[scheme].raised, value(`--mantine-color-${color}-light`), value(`--mantine-color-${color}-light-hover`)]) {
        expect(contrastRatio(value(`--mantine-color-${color}-text`), background)).toBeGreaterThanOrEqual(4.5);
      }
    }
  });
});
