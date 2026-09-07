import { ayu, bad, brand, chart, fonts, info, ok, typeScale, warn } from "./tokens";

/* The Mantine binding for the tokens in ./tokens.ts.
 *
 * Colours are registered under what they mean rather than what hue they are:
 * `brand` is the interactive accent, `ok`/`warn`/`bad` are the three health
 * states and `info` is the fourth severity. A component asks for `ok` and gets
 * whatever green the palette currently holds, so re-hueing the product is a
 * change to ./tokens.ts and nothing else — which is what made the previous
 * arrangement wrong, where "teal" was simultaneously the primary color and the
 * literal a health badge asked for.
 */
/* Mantine picks the text colour for a filled surface from parseThemeColor,
   which resolves a two-shade primaryShade against the light scheme whatever
   scheme is actually rendering. With { light: 7, dark: 5 } the decision is made
   against #7c4dcc while the CSS paints #a97ce0: white on a fill light enough to
   need black, measured at 3.16:1 on the most-used button in the product. The
   same mistake was on every semantic colour — the dark scheme painted white on
   bad[5] #f26d78 at 2.90:1, which is what a Remove button and a failing
   waterfall span were drawn in.

   Mantine emits a per-scheme contrast variable for the primary colour only, so
   the other four get one here, decided per scheme against the shade that
   scheme actually paints. A filled surface in one of the five semantic colours
   defers to its variable; everything else keeps Mantine's own answer. */
const semanticColors = { brand, ok, warn, bad, info };

/** WCAG relative luminance, the quantity Mantine's own isLight compares
 *  against luminanceThreshold. Kept here rather than imported because this
 *  directory is shared with the embedded views and has no node_modules. */
export function relativeLuminance(hex: string) {
  const value = hex.replace("#", "");
  const channel = (offset: number) => {
    const part = parseInt(value.slice(offset, offset + 2), 16) / 255;
    return part <= 0.03928 ? part / 12.92 : ((part + 0.055) / 1.055) ** 2.4;
  };
  return 0.2126 * channel(0) + 0.7152 * channel(2) + 0.0722 * channel(4);
}

/** The text colour a filled surface of this hue needs: whichever of black and
 *  white reads better on it.
 *
 *  Mantine decides this against a luminance threshold, which answers "is this
 *  colour light" rather than "which text can be read on it". The two differ in
 *  the middle of the range, and info[7] #1e86bd is in it: below the threshold,
 *  so white, at 4.04:1 — under AA — where black reads 5.20:1. */
export function filledTextOn(hex: string) {
  const luminance = relativeLuminance(hex);
  const onWhite = 1.05 / (luminance + 0.05);
  const onBlack = (luminance + 0.05) / 0.05;
  return onBlack > onWhite ? "var(--mantine-color-black)" : "var(--mantine-color-white)";
}

export function filledContrastVariables(scheme: "light" | "dark") {
  const shade = scheme === "dark" ? 5 : 7;
  return Object.fromEntries(Object.entries(semanticColors).map(([name, ramp]) => [`--fanout-color-${name}-contrast`, filledTextOn(ramp[shade])]));
}

export function schemeAwareFilledText<Input extends { color?: string; variant?: string; autoContrast?: boolean }, Result extends { color: string }>(
  base: (input: Input) => Result,
) {
  return (input: Input): Result => {
    // Components pass `color || theme.primaryColor`, so this is normally set;
    // Mantine's own resolver throws on undefined rather than falling back.
    const color = input.color ?? fanoutThemeConfig.primaryColor;
    const resolved = base({ ...input, color });
    const isFilled = input.variant === "filled" || input.variant === undefined;
    // A component that has turned auto-contrast off has asked for Mantine's
    // white, and overriding it here would ignore the request.
    if (input.autoContrast === false || !isFilled || !(color in semanticColors)) return resolved;
    return { ...resolved, color: `var(--fanout-color-${color}-contrast)` };
  };
}

/* #a97ce0 has a relative luminance of 0.282, just under Mantine's default
   threshold of 0.3. The threshold decides the text colour on a virtual colour
   and on --mantine-primary-color-contrast, so the accent needs it lowered to
   be read as light. Three other stops sit in the 0.25-0.30 band and change
   with it, because the colour Mantine evaluates is shade 7, not shade 5:
   ok[7] #4f9c3a (0.257), warn[7] #c87d21 (0.271) and dark[3] #8b8e99 (0.271).
   Each flips from white to near-black text on a filled surface, and each is an
   improvement — black on #7fd962 reads 12.0:1 where white read 1.75:1. */
const luminanceThreshold = 0.25;

/* Mantine's Badge carries its own scale: xs is 9px and sm is 10px, both under
   the floor the type scale sets. A severity badge in a log table and the count
   on a tab were drawn at 9px. Each size is raised to the floor and no
   further, so md and up keep the size they had. */
const badgeFontSize: Record<string, number> = { xs: 9, sm: 10, md: 12, lg: 14, xl: 16 };

export const fanoutThemeConfig = {
  primaryColor: "brand",
  /* Shade 7 is the site's link color on a light ground and shade 5 is its
     color on a dark one, so each scheme picks the accent the documentation
     already uses. */
  primaryShade: { light: 7, dark: 5 },
  /* Mantine picks the text color for filled surfaces from the fill's own
     luminance, which the two-shade accent needs: white on #7c4dcc, near-black
     on #a97ce0. */
  autoContrast: true,
  luminanceThreshold,
  colors: { dark: ayu, brand, ok, warn, bad, info },
  defaultRadius: "md",
  fontFamily: fonts.body,
  fontFamilyMonospace: fonts.display,
  /* Headings share the body family and differ by weight and size only; 600
     rather than bold, so a heading reads as a precise label instead of
     shouting. Mono is a data face now, not a display face. */
  headings: { fontFamily: fonts.body, fontWeight: "600" },
  cursorType: "pointer",
  /* Tabs and Pagination do not go through variantColorResolver — they set their
     own text colour from Mantine's other auto-contrast path, which resolves the
     same two-shade primaryShade against the light scheme and lands on white
     over the dark accent at 3.16:1. Both are pointed at the same per-scheme
     variable schemeAwareFilledText gives filled surfaces, so one measurement
     covers the button, the active tab and the current page. */
  components: {
    Tabs: { styles: (_theme: unknown, props: { color?: string }) => ({ tab: primaryOnly(props.color, "--tabs-text-color") }) },
    Pagination: { styles: (_theme: unknown, props: { color?: string }) => ({ control: primaryOnly(props.color, "--pagination-active-color") }) },
    Badge: { vars: (_theme: unknown, props: { size?: string }) => ({ root: { "--badge-fz": `${Math.max(badgeFontSize[props.size ?? "md"] ?? badgeFontSize.md, typeScale.micro)}px` } }) },
  },
} as const;

/* Mantine derives a handful of variables from `red` no matter what the theme
   says — the required-field asterisk and every error outline among them. They
   are re-pointed at the palette's own red so an invalid field does not
   introduce a fourth red to the page. */
export const fanoutCssVariables = () => ({
  variables: { "--mantine-color-error": "var(--mantine-color-bad-filled)" },
  /* Mantine's light-scheme dimmed is #868e96, which is 3.3:1 on this ground —
     under WCAG AA, and dimmed carries real content here: page descriptions,
     timestamps, log times, healthy error rates. The chart palette's light muted
     is the same grey family at 4.8:1, and using it keeps a dimmed label and the
     axis beside it the same color. Dark already clears AA. */
  light: { "--mantine-color-dimmed": chart.light.muted, ...filledContrastVariables("light") },
  dark: filledContrastVariables("dark"),
});

/* The override is aimed at the accent Mantine gets wrong. A Tabs or Pagination
   given another colour keeps that colour's own answer rather than the
   accent's, which would be right only by luck. */
function primaryOnly(color: string | undefined, variable: string) {
  return !color || color === fanoutThemeConfig.primaryColor ? { [variable]: `var(--fanout-color-${fanoutThemeConfig.primaryColor}-contrast)` } : {};
}
