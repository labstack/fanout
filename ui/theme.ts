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
/* Mantine reads the text colour for a filled surface from parseThemeColor,
   which resolves a two-shade primaryShade against the light scheme whatever
   scheme is actually rendering. With { light: 7, dark: 5 } the decision is made
   against #7c4dcc while the CSS paints #a97ce0: white on a fill light enough to
   need black, measured at 3.16:1 on the most-used button in the product.

   --mantine-primary-color-contrast is emitted per scheme and is already right,
   so a filled primary surface defers to it: near-black on the dark scheme's
   #a97ce0 and white on the light scheme's #7c4dcc. Every other colour keeps
   Mantine's own answer.

   The default resolver is passed in because this directory is shared with the
   embedded views and has no node_modules to import Mantine from. Both apps wire
   it, so a filled button reads the same inside a chat card as outside one. */
export function schemeAwareFilledText<Input extends { color?: string; variant?: string }, Result extends { color: string }>(
  base: (input: Input) => Result,
) {
  return (input: Input): Result => {
    // Components pass `color || theme.primaryColor`, so this is normally set;
    // Mantine's own resolver throws on undefined rather than falling back.
    const color = input.color ?? fanoutThemeConfig.primaryColor;
    const resolved = base({ ...input, color });
    const isFilled = input.variant === "filled" || input.variant === undefined;
    return color === fanoutThemeConfig.primaryColor && isFilled
      ? { ...resolved, color: "var(--mantine-primary-color-contrast)" }
      : resolved;
  };
}

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
  /* #a97ce0 has a relative luminance of 0.282, just under Mantine's default
     threshold of 0.3. The threshold is what decides the text colour on a
     virtual colour and on --mantine-primary-color-contrast; the accent needs it
     lowered to be read as light. Only fills between 0.25 and 0.3 change hands,
     and the accent is the palette's only one: ok, warn, bad and info all sit
     above 0.31. */
  luminanceThreshold: 0.25,
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
