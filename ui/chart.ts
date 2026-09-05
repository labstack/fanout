/* Chart and status helpers shared by the browser host and the embedded views.
 * Pure functions over ./tokens only: this directory has no node_modules, so
 * nothing here may import a package. */
import { bad, chart, info, ok, series, warn } from "./tokens";

export function healthColor(health: string) {
  return health === "healthy" ? "ok" : health === "degraded" ? "warn" : "bad";
}

/* A chart is drawn into a canvas or SVG that cannot read CSS custom
   properties, so these hand ECharts resolved values from the same ramps
   Mantine gets. The shade differs by scheme for the same reason the accent
   does: the palette's own hue reads on Ayu, a darker stop is needed on white. */
export function chartTheme(dark: boolean) {
  return chart[dark ? "dark" : "light"];
}

export function statusHex(dark: boolean) {
  const shade = dark ? 5 : 7;
  return { ok: ok[shade], warn: warn[shade], bad: bad[shade], info: info[shade] };
}

/** One colour per service or metric, where the colour identifies rather than
 *  grades. Hashed so a service keeps its colour between renders, and drawn from
 *  a palette with no health hue in it. */
export function seriesColor(name: string, dark: boolean) {
  const palette = series[dark ? "dark" : "light"];
  let hash = 0;
  for (const character of name) hash = (hash * 31 + character.charCodeAt(0)) | 0;
  return palette[Math.abs(hash) % palette.length];
}

export function severityColor(value: string) {
  const severity = String(value).toUpperCase();
  if (severity === "ERROR" || severity === "FATAL") return "bad";
  if (severity === "WARN" || severity === "WARNING") return "warn";
  if (severity === "INFO") return "info";
  return "gray";
}

export function severityHex(value: string, dark: boolean) {
  const status = statusHex(dark);
  const severity = String(value).toUpperCase();
  if (severity === "ERROR" || severity === "FATAL") return status.bad;
  if (severity === "WARN" || severity === "WARNING") return status.warn;
  if (severity === "INFO") return status.info;
  return chartTheme(dark).muted;
}
