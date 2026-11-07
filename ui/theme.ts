import { createLabstackTheme, mergeLabstackTheme } from "@labstack/ui";
import { bad, brand, chartSeries, fonts, info, ok, surfaces, typeScale, warn } from "./tokens";

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
  surfaces,
  typeScale: { small: typeScale.small, body: typeScale.body, title: typeScale.title, section: typeScale.section, page: typeScale.page },
  chart: { series: chartSeries },
});

export const fanoutTheme = mergeLabstackTheme(shared.theme, {
  components: {
    Anchor: { defaultProps: { c: "iris" } },
    NavLink: { defaultProps: { color: "iris" } },
    Tabs: { defaultProps: { color: "iris" } },
    Checkbox: { defaultProps: { color: "iris" } },
    Radio: { defaultProps: { color: "iris" } },
    Switch: { defaultProps: { color: "iris" } },
    Pagination: { defaultProps: { color: "iris" } },
    SegmentedControl: {
      vars: () => ({ root: { "--sc-color": "var(--mantine-color-iris-light)", "--sc-label-color": "var(--mantine-color-iris-text)", "--sc-shadow": "none" } }),
      styles: { root: { backgroundColor: "var(--labstack-inset)" } },
    },
    Input: { vars: (_theme: unknown, props: { variant?: string }) => ({ wrapper: {
      "--input-bd-focus": "var(--fanout-focus)",
      ...props.variant === "filled" ? { "--input-bg": "var(--labstack-inset)" } : {},
    } }) },
    Table: { vars: () => ({ table: { "--table-striped-color": "var(--labstack-inset)", "--table-highlight-on-hover-color": "var(--labstack-hover)" } }) },
  },
});

export const fanoutCssVariables: typeof shared.cssVariablesResolver = theme => {
  const resolved = shared.cssVariablesResolver(theme);
  for (const scheme of ["light", "dark"] as const) Object.assign(resolved[scheme], {
    "--mantine-color-default-hover": surfaces[scheme].hover,
    ...Object.fromEntries(["brand", "gray", "dark", "ink"].flatMap(color => [
      [`--mantine-color-${color}-light`, surfaces[scheme].inset],
      [`--mantine-color-${color}-light-hover`, surfaces[scheme].hover],
      [`--mantine-color-${color}-light-color`, surfaces[scheme].text],
    ])),
    "--fanout-focus": brand[scheme === "dark" ? 5 : 7],
    "--mantine-color-anchor": `var(--mantine-color-iris-${scheme === "dark" ? 5 : 7})`,
    "--mantine-color-iris-text": brand[scheme === "dark" ? 5 : 7],
    "--mantine-color-iris-outline": brand[scheme === "dark" ? 5 : 7],
  });
  return resolved;
};

export const chart = shared.chart;
export const series = { light: chart.light.series, dark: chart.dark.series };
