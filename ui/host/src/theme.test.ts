import { describe, expect, it } from "vitest";
import { fanoutCssVariables, fanoutThemeConfig } from "../../theme";
import { chart, fonts, typeScale } from "../../tokens";
import { fanoutTheme, filledTextFollowsTheScheme } from "./theme";

describe("typeface", () => {
  it("sets one grotesk for UI and headings and mono for data", () => {
    expect(fonts.body).toMatch(/^"Geist Variable"/);
    expect(fonts.display).toMatch(/^"Geist Mono Variable"/);
    expect(fanoutThemeConfig.fontFamily).toBe(fonts.body);
    expect(fanoutThemeConfig.headings.fontFamily).toBe(fonts.body);
    expect(fanoutThemeConfig.headings.fontWeight).toBe("600");
    expect(fanoutThemeConfig.fontFamilyMonospace).toBe(fonts.display);
  });
});

describe("light scheme", () => {
  it("reads dimmed text at the chart palette's muted grey rather than Mantine's", () => {
    expect(fanoutCssVariables().light["--mantine-color-dimmed"]).toBe(chart.light.muted);
    expect(fanoutCssVariables().dark).toEqual({});
  });
});

describe("filled surfaces", () => {
  // Mantine decides filled text from the light-scheme shade whatever scheme is
  // rendering, which put white on the dark scheme's #a97ce0 at 3.16:1 — under
  // WCAG AA, on the most-used control in the product.
  it("defers primary filled text to the per-scheme contrast variable", () => {
    const filled = filledTextFollowsTheScheme({ color: "brand", variant: "filled", theme: fanoutTheme } as never);
    expect(filled.color).toBe("var(--mantine-primary-color-contrast)");

    const implicit = filledTextFollowsTheScheme({ variant: "filled", theme: fanoutTheme } as never);
    expect(implicit.color).toBe("var(--mantine-primary-color-contrast)");
  });

  it("leaves every other colour and variant to Mantine", () => {
    const badge = filledTextFollowsTheScheme({ color: "bad", variant: "filled", theme: fanoutTheme } as never);
    expect(badge.color).not.toBe("var(--mantine-primary-color-contrast)");

    const light = filledTextFollowsTheScheme({ color: "brand", variant: "light", theme: fanoutTheme } as never);
    expect(light.color).not.toBe("var(--mantine-primary-color-contrast)");
  });
});

describe("type scale", () => {
  // A survey of one screen found a 9px badge and 10px axis labels.
  it("sets a floor no smaller than 11px", () => {
    expect(Math.min(...Object.values(typeScale))).toBe(11);
    expect(new Set(Object.values(typeScale)).size).toBe(Object.values(typeScale).length);
  });
});
