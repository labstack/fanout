/* Fanout design tokens.
 *
 * The palette is the documentation site's, restated for the browser app so the
 * product and the pages that document it are recognizably the same thing. The
 * source of the values is site/src/styles/fanout.css; anything changed here has
 * to change there too.
 *
 * Surfaces are Ayu Dark. The slight blue cast is the point — a "corrected"
 * neutral grey would undo what makes these read as Ayu rather than as any dark
 * theme.
 *
 * The accent is violet, and that is the decision this file exists to hold.
 * Green, amber and red are load-bearing in an observability product: they mean
 * healthy, degraded and unhealthy, and they appear next to the thing being
 * described. An interactive color that borrows one of those hues puts "green
 * means healthy" in conflict with "green means clickable" on the same screen,
 * which is exactly what teal-as-primary did here. Violet carries no status, so
 * it is the hue free to mean "you can click this".
 *
 * Ramps run light to dark, which is Mantine's convention: index 0 is the
 * lightest tint and index 9 the darkest shade. The site's own tokens are marked
 * on the stop that carries them.
 */

/** Ayu Dark surfaces. Mantine reads `dark` for every dark-scheme surface:
 *  0 text, 2 dimmed, 4 border, 5 hover, 6 elevated surface, 7 body. */
export const ayu = [
  "#fafafa", // --sl-color-white
  "#e6e4de", // --sl-color-gray-1
  "#bfbdb6", // --sl-color-gray-2
  "#8b8e99", // --sl-color-gray-3
  "#565b69", // --sl-color-gray-4
  "#1d2433", // --sl-color-gray-5
  "#131721", // --sl-color-gray-6
  "#0b0e14", // --sl-color-black
  "#080a10",
  "#05070b",
] as const;

/** The interactive accent. Shade 7 is the site's light-scheme link color and
 *  shade 5 its dark-scheme one, which is why primaryShade names those two. */
export const brand = [
  "#f3ecfd",
  "#ece3fb", // --sl-color-accent-low (light)
  "#dcc9f7",
  "#d2a6ff", // --sl-color-accent-high (dark)
  "#bf94ec",
  "#a97ce0", // --sl-color-accent (dark)
  "#9163d6",
  "#7c4dcc", // --sl-color-accent (light)
  "#5b32a3", // --sl-color-accent-high (light)
  "#40236f",
] as const;

/** Healthy. Ayu's green, the hue the landing page's service table already uses. */
export const ok = [
  "#eefbe6", "#dcf7cc", "#c2f0a6", "#a5e880", "#8fe06c",
  "#7fd962", // site healthy
  "#66c04b", "#4f9c3a", "#3b7a2c", "#2a5a1f",
] as const;

/** Degraded. */
export const warn = [
  "#fff5e6", "#ffe9c9", "#ffd79b", "#ffc571", "#ffbc62",
  "#ffb454", // site degraded
  "#ef9c33", "#c87d21", "#9c5f16", "#74460f",
] as const;

/** Unhealthy, and every error surface. */
export const bad = [
  "#fdecee", "#fbd9dc", "#f8b6bc", "#f59099", "#f37d87",
  "#f26d78", // site unhealthy
  "#e04d5a", "#c03642", "#96262f", "#6f1a21",
] as const;

/** Informational — the fourth severity, deliberately not one of the three
 *  status hues. Ayu's entity blue. */
export const info = [
  "#e8f6ff", "#ccebff", "#a3daff", "#7dcbff", "#66c5ff",
  "#59c2ff", // Ayu entity
  "#33a7e6", "#1e86bd", "#146694", "#0d4a6d",
] as const;

/** Chart chrome. Charts are drawn by ECharts into a canvas, so they cannot read
 *  CSS custom properties and need the resolved values. These are the same Ayu
 *  and light-scheme stops the rest of the app gets from Mantine. */
export const chart = {
  dark: { text: "#bfbdb6", muted: "#8b8e99", grid: "#1d2433", surface: "#131721", border: "#565b69" },
  light: { text: "#4a5058", muted: "#6b7280", grid: "#eceef0", surface: "#fcfcfc", border: "#a4abb4" },
} as const;

/** Categorical series — one color per service or metric, where the color
 *  identifies rather than grades. Drawn from the brand mark's own ribbons, and
 *  kept clear of green, amber and red so a series is never mistaken for a
 *  health reading. */
export const series = {
  dark: ["#66d0ee", "#a97ce0", "#5fe8ce", "#cb55e8", "#41b6f8", "#d2a6ff"],
  light: ["#2b93b5", "#7c4dcc", "#1a9c86", "#a12fbf", "#2f7fd4", "#9163d6"],
} as const;

/** Light-scheme ground and dark-scheme ground, for the browser UI outside the
 *  document — the address bar and the tab strip. */
export const ground = { light: "#fcfcfc", dark: "#0b0e14" } as const;

export const fonts = {
  /** Mono owns the brand, headings and technical artifacts. */
  display: '"IBM Plex Mono", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
  /** Sans is for sustained reading: prose, table cells, chat. */
  body: '"IBM Plex Sans", ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
} as const;
