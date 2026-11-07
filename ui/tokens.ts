/** Console inputs shared by the host and embedded views. Status and interaction
 * hues stay separate: iris never conveys health. The library owns component
 * styling, contrast resolution and chart chrome in ./theme.ts. */
export const brand = [
  "#F1F0FF", "#E4E2FF", "#CDC9FF", "#B5B0FF", "#A29CFF",
  "#8F8CFF", "#756DEB", "#5A50D8", "#463DAE", "#332B81",
] as const;

export const surfaces = {
  dark: { ground: "#0B0B0C", raised: "#121214", inset: "#08080A", hover: "#1A1A1D", border: "#232327", borderSoft: "#1A1A1E", controlBorder: "#34343A", text: "#EDEDEF", muted: "#8E8E96" },
  light: { ground: "#F5F5F6", raised: "#FFFFFF", inset: "#EFEFF1", hover: "#EDEDEF", border: "#E3E3E6", borderSoft: "#ECECEE", controlBorder: "#C8C8CE", text: "#111113", muted: "#63636B" },
} as const;

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

/** Six fixed identity slots. Slot 2 is magenta, away from error red and warn
 * amber. WCAG relative luminance checks every mark against ground and raised;
 * failing light slots are darkened without changing hue. Palette tests use
 * OKLab distance and Machado full-severity CVD simulation for distinctness;
 * labels, shapes and dashes remain necessary when hues converge under CVD. */
export const chartSeries = {
  dark: ["#3987e5", "#ed83fb", "#199e70", "#9085e9", "#c98500", "#d55181"],
  light: ["#2a78d6", "#9719a7", "#19a170", "#4a3aa7", "#bd8000", "#ce6d92"],
} as const;

export const typeScale = { micro: 11, small: 12, body: 14, title: 16, section: 18, page: 24 } as const;

export const fonts = {
  /** Mono is for data: code, ids, timestamps, numeric cells, and the chart
   *  axes that borrow it. It no longer sets headings. */
  display: '"Geist Mono Variable", ui-monospace, SFMono-Regular, Menlo, Monaco, Consolas, monospace',
  /** One grotesk for everything a person reads: UI, prose, headings, the
   *  brand lockup. Headings differ by weight and size, not by family. */
  body: '"Geist Variable", ui-sans-serif, system-ui, -apple-system, BlinkMacSystemFont, "Segoe UI", sans-serif',
} as const;
