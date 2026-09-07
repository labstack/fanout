import { createTheme, defaultVariantColorsResolver, type VariantColorsResolver } from "@mantine/core";
import { fanoutThemeConfig } from "../../theme";

/* Mantine reads the text colour for a filled surface from `parsed.isLight`, and
   parseThemeColor resolves a two-shade primaryShade against the light scheme
   whatever scheme is actually rendering. With { light: 7, dark: 5 } the decision
   is therefore made against #7c4dcc while the CSS paints #a97ce0: white on a
   fill light enough to need black, measured at 3.16:1 on the New chat button —
   under WCAG AA, on the most-used control in the product.

   --mantine-primary-color-contrast is emitted per scheme and is already right,
   so a filled primary surface defers to it: near-black on the dark scheme's
   #a97ce0 (6.11:1) and white on the light scheme's #7c4dcc (5.54:1). Every
   other colour keeps Mantine's own answer.

   This lives here rather than in ../../theme because that directory is shared
   with the embedded views and has no node_modules to import Mantine from. */
export const filledTextFollowsTheScheme: VariantColorsResolver = (input) => {
  // Components pass `color || theme.primaryColor`, so this is normally set;
  // Mantine's own resolver throws on undefined rather than falling back, so the
  // default is applied here before delegating.
  const color = input.color ?? fanoutThemeConfig.primaryColor;
  const resolved = defaultVariantColorsResolver({ ...input, color });
  const isFilled = input.variant === "filled" || input.variant === undefined;
  return color === fanoutThemeConfig.primaryColor && isFilled
    ? { ...resolved, color: "var(--mantine-primary-color-contrast)" }
    : resolved;
};

export const fanoutTheme = createTheme({ ...fanoutThemeConfig, variantColorResolver: filledTextFollowsTheScheme });
