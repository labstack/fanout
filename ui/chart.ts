/* Chart and status helpers shared by the browser host and the embedded views.
 * Pure functions over ./tokens only: this directory has no node_modules, so
 * nothing here may import a package. */
import { bad, chart, info, ok, series, warn } from "./tokens";

/* "unknown" is neutral, not alarming: an empty window has nothing to grade, and
   painting it red claims a failure as confidently as green claimed health. */
export function healthColor(health: string) {
  if (health === "healthy") return "ok";
  if (health === "degraded") return "warn";
  if (health === "unknown") return "gray";
  return "bad";
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

/** A node's shape says what its colour says.
 *
 *  Health was drawn in green, amber and red alone, which is the one channel a
 *  reader with a colour vision deficiency does not have: a service map became
 *  twenty identical circles. The shapes are ordered by severity so the map also
 *  reads at a glance — a diamond stands out from a ring the way an alarm should.
 */
export function healthSymbol(health: string) {
  if (health === "unhealthy") return "diamond";
  if (health === "degraded") return "roundRect";
  return "circle";
}

/** Shape is the severity channel, so an ungraded node keeps the circle and
 *  says so with its outline instead: a dashed ring reads as "nothing to grade"
 *  without claiming a place in the severity order. */
export function healthBorderType(health: string) {
  return health === "unknown" ? "dashed" : "solid";
}

/** ECharts sizes a symbol by its bounding box, and the shapes do not fill one
 *  equally: a diamond covers half of it against a circle's ~0.79 and a rounded
 *  square's ~0.95. Sized naively the unhealthy node drew a third smaller than a
 *  healthy one — and shrank its click target with it — which is the opposite of
 *  what the shape is for. The scale evens the drawn area out. */
export function healthSymbolScale(health: string) {
  if (health === "unhealthy") return 1.25;
  if (health === "degraded") return 0.91;
  return 1;
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
