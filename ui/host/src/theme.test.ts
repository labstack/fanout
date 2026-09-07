import { describe, expect, it } from "vitest";
import { fanoutCssVariables, fanoutThemeConfig, relativeLuminance } from "../../theme";
import { bad, brand, chart, fonts, info, ok, typeScale, warn } from "../../tokens";
import { fanoutTheme, filledTextFollowsTheScheme } from "./theme";

/* WCAG contrast between two hex colours, so a claim about a filled control is
   a number this suite computes rather than a variable name it recognises. */
function contrast(foreground: string, background: string) {
  const [light, dark] = [relativeLuminance(foreground), relativeLuminance(background)].sort((a, b) => b - a);
  return (light + 0.05) / (dark + 0.05);
}

const black = "#000000";
const white = "#ffffff";

function filledText(color: string, scheme: "light" | "dark") {
  const variables = scheme === "light" ? fanoutCssVariables().light : fanoutCssVariables().dark;
  const value = (variables as Record<string, string>)[`--fanout-color-${color}-contrast`];
  return value === "var(--mantine-color-black)" ? black : white;
}

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
  });
});

describe("filled surfaces", () => {
  // Mantine decides filled text from the light-scheme shade whatever scheme is
  // rendering, which put white on the dark scheme's #a97ce0 at 3.16:1 and on
  // #f26d78 at 2.90:1 — under WCAG AA, on the button that removes a widget and
  // on the most-used control in the product.
  const ramps: Record<string, readonly string[]> = { brand, ok, warn, bad, info };

  it("clears AA on every semantic filled surface, in both schemes", () => {
    for (const [color, ramp] of Object.entries(ramps)) {
      for (const scheme of ["light", "dark"] as const) {
        const background = ramp[scheme === "dark" ? 5 : 7];
        const ratio = contrast(filledText(color, scheme), background);
        expect(`${color} ${scheme} ${ratio.toFixed(2)}`).toBe(`${color} ${scheme} ${Math.max(ratio, 4.5).toFixed(2)}`);
      }
    }
  });

  it("routes a semantic filled surface to its own per-scheme variable", () => {
    expect(filledTextFollowsTheScheme({ color: "brand", variant: "filled", theme: fanoutTheme } as never).color).toBe("var(--fanout-color-brand-contrast)");
    expect(filledTextFollowsTheScheme({ color: "bad", variant: "filled", theme: fanoutTheme } as never).color).toBe("var(--fanout-color-bad-contrast)");
    // Components pass no variant for their default, which is filled.
    expect(filledTextFollowsTheScheme({ variant: "filled", theme: fanoutTheme } as never).color).toBe("var(--fanout-color-brand-contrast)");
  });

  it("leaves other colours, other variants and an opted-out component to Mantine", () => {
    const gray = filledTextFollowsTheScheme({ color: "gray", variant: "filled", theme: fanoutTheme } as never);
    expect(gray.color).toBe("var(--mantine-color-white)");

    const light = filledTextFollowsTheScheme({ color: "brand", variant: "light", theme: fanoutTheme } as never);
    expect(light.color).toBe("var(--mantine-color-brand-light-color)");

    // autoContrast={false} is a component saying it wants Mantine's white.
    const optedOut = filledTextFollowsTheScheme({ color: "brand", variant: "filled", autoContrast: false, theme: fanoutTheme } as never);
    expect(optedOut.color).toBe("var(--mantine-color-white)");
  });

  it("points the active tab and page control at the accent's contrast, and only the accent's", () => {
    const tabs = fanoutThemeConfig.components.Tabs.styles(fanoutTheme, {});
    expect(tabs.tab["--tabs-text-color"]).toBe("var(--fanout-color-brand-contrast)");
    expect(fanoutThemeConfig.components.Tabs.styles(fanoutTheme, { color: "warn" }).tab).toEqual({});

    const pagination = fanoutThemeConfig.components.Pagination.styles(fanoutTheme, {});
    expect(pagination.control["--pagination-active-color"]).toBe("var(--fanout-color-brand-contrast)");
  });
});

describe("type scale", () => {
  // A survey of one screen found a 9px badge and 10px axis labels.
  it("sets a floor no smaller than 11px", () => {
    expect(Math.min(...Object.values(typeScale))).toBe(11);
    expect(new Set(Object.values(typeScale)).size).toBe(Object.values(typeScale).length);
  });

  it("raises Mantine's own badge sizes to that floor without shrinking the larger ones", () => {
    const size = (props: { size?: string }) => Number(fanoutThemeConfig.components.Badge.vars(fanoutTheme, props).root["--badge-fz"].replace("px", ""));
    // Mantine's xs badge is 9px and its sm is 10px; a severity badge in a log
    // table and a tab count were drawn at those sizes.
    expect(size({ size: "xs" })).toBe(typeScale.micro);
    expect(size({ size: "sm" })).toBe(typeScale.micro);
    expect(size({ size: "lg" })).toBe(14);
    expect(size({})).toBe(12);
  });
});
