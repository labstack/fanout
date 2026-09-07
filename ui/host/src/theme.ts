import { createTheme, defaultVariantColorsResolver } from "@mantine/core";
import { fanoutThemeConfig, schemeAwareFilledText } from "../../theme";

/** See schemeAwareFilledText: Mantine decides filled text against the light
 *  shade whatever scheme is rendering, which left the dark accent under AA. */
export const filledTextFollowsTheScheme = schemeAwareFilledText(defaultVariantColorsResolver);

export const fanoutTheme = createTheme({ ...fanoutThemeConfig, variantColorResolver: filledTextFollowsTheScheme });
