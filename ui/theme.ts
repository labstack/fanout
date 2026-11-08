import { buildTheme } from "@labstack/ui";

/* Fanout uses the shared library's default look unchanged: ink actions, the
 * violet accent, status ramps, surfaces, compact controls and Geist. Brand
 * inputs belong here only when Fanout needs to differ from that default. */
const shared = buildTheme();

export const fanoutTheme = shared.theme;
export const fanoutCssVariables = shared.cssVariablesResolver;
export const chart = shared.chart;
export const series = { light: chart.light.series, dark: chart.dark.series };
