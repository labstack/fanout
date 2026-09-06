import { ayu, bad, brand, chart, fonts, info, ok, warn } from "./tokens";

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
  colors: { dark: ayu, brand, ok, warn, bad, info },
  defaultRadius: "md",
  fontFamily: fonts.body,
  fontFamilyMonospace: fonts.display,
  /* Headings share the body family and differ by weight and size only; 600
     rather than bold, so a heading reads as a precise label instead of
     shouting. Mono is a data face now, not a display face. */
  headings: { fontFamily: fonts.body, fontWeight: "600" },
  cursorType: "pointer",
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
  light: { "--mantine-color-dimmed": chart.light.muted },
  dark: {},
});
