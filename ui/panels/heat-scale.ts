/** Sequential ramps have strictly ordered luminance, with a readable high end. */
import type { ChartTheme } from "./compile";
export function heatRamp(theme: ChartTheme): string[] {
  return theme.dark ? ["#0f2a43", "#164267", "#1e5a8a", "#2879ae", "#399aca", "#57b8e3", "#7dd3fc"]
    : ["#dbeafe", "#bfdbfe", "#93c5fd", "#60a5fa", "#3b82f6", "#2563eb", "#1d4ed8"];
}
/** Count magnitude on a capped log scale; the cap occupies the seventh step. */
export function heatStep(count: number, cap: number): number {
  // A singleton has no logarithmic magnitude; also avoids log(1) / log(1).
  if (count <= 1) return 0;
  if (count >= cap) return 6;
  return Math.max(0, Math.min(6, Math.floor(Math.log10(Math.max(1, count)) / Math.log10(cap) * 6)));
}

