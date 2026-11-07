import { createLabstackTheme, mergeLabstackTheme } from "@labstack/ui";
import { bad, brand, chartSeries, fonts, info, ok, surfaces, typeScale, warn } from "./tokens";

// 2026.11.4 limits section/page inputs to >=20/28 and validates control borders
// as 3:1 marks, and muted text on every status tint. Console specifies
// 18/24, quiet decorative borders and its own muted ink. Apply
// those explicit product values after the factory, leaving its component and
// semantic contrast handling intact. Focus and selected edges use iris.
const { controlBorder: _lightBorder, muted: _lightMuted, ...light } = surfaces.light;
const { controlBorder: _darkBorder, muted: _darkMuted, ...dark } = surfaces.dark;
const shared = createLabstackTheme({
  primary: "ink",
  colors: { iris: [...brand], ok: [...ok], warn: [...warn], bad: [...bad], info: [...info] },
  tones: { success: "ok", warning: "warn", danger: "bad", info: "info" },
  fontFamily: fonts.body,
  fontFamilyMonospace: fonts.display,
  displayWeight: 600,
  radius: "soft",
  elevation: "bordered",
  density: "compact",
  controlSizes: { sm: 32, md: 36, lg: 40 },
  focus: "ring",
  surfaces: { light, dark },
  typeScale: { small: typeScale.small, body: typeScale.body, title: typeScale.title },
  chart: { series: chartSeries },
});

export const fanoutTheme = mergeLabstackTheme(shared.theme, {
  headings: { fontFamily: fonts.body, sizes: {
    h1: { fontSize: `${typeScale.page / 16}rem` },
    h2: { fontSize: `${typeScale.section / 16}rem` },
  } },
  fontSizes: { xl: `${typeScale.section / 16}rem` },
  other: { labstack: { typeScale } },
  components: {
    Anchor: { defaultProps: { c: "iris" } },
    NavLink: { defaultProps: { color: "iris" } },
    Tabs: { defaultProps: { color: "iris" } },
  },
});

export const fanoutCssVariables: typeof shared.cssVariablesResolver = theme => {
  const resolved = shared.cssVariablesResolver(theme);
  for (const scheme of ["light", "dark"] as const) Object.assign(resolved[scheme], {
    "--labstack-control-border": surfaces[scheme].controlBorder,
    "--labstack-muted": surfaces[scheme].muted,
    // Generic focus/selection variables follow iris; filled Button still
    // resolves its default brand palette to the library's ink primary.
    "--mantine-primary-color-filled": `var(--mantine-color-iris-${scheme === "dark" ? 5 : 7})`,
    "--mantine-primary-color-light": "var(--mantine-color-iris-light)",
    "--mantine-primary-color-light-hover": "var(--mantine-color-iris-light-hover)",
    "--mantine-primary-color-light-color": "var(--mantine-color-iris-light-color)",
    "--mantine-color-anchor": `var(--mantine-color-iris-${scheme === "dark" ? 5 : 7})`,
    "--mantine-color-iris-text": brand[scheme === "dark" ? 5 : 7],
    "--mantine-color-iris-outline": brand[scheme === "dark" ? 5 : 7],
  });
  return resolved;
};

export const chart = {
  light: { ...shared.chart.light, muted: surfaces.light.muted },
  dark: { ...shared.chart.dark, muted: surfaces.dark.muted },
};
export const series = { light: chart.light.series, dark: chart.dark.series };
