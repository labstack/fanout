/** Sequential ramps have strictly ordered luminance, with a readable high end. */
import { info } from "../tokens";
import type { ChartTheme } from "./compile";
export function heatRamp(theme: ChartTheme): string[] {
  return theme.dark ? ["#0f2a43", "#164267", "#1e5a8a", "#2879ae", "#399aca", "#57b8e3", "#7dd3fc"]
    : [info[0], info[1], info[2], info[3], info[5], info[7], info[9]];
}
/** Continuous capped log position; square-root spacing opens the low end. */
export function heatStep(count: number, cap: number): number {
  // A singleton has no logarithmic magnitude; also avoids log(1) / log(1).
  if (count <= 1) return 0;
  if (count >= cap) return 6;
  return 6 * Math.sqrt(Math.log(count) / Math.log(cap));
}


/** Continuous log magnitude expands the low end without changing a count's color between frames. */
export function heatColor(count: number, cap: number, ramp: readonly string[]): string {
  const position = heatStep(count, cap);
  const low = Math.floor(position), high = Math.min(6, low + 1), fraction = position - low;
  const mix = (channel: number) => Math.round(parseInt(ramp[low].slice(channel, channel+2),16) * (1-fraction) + parseInt(ramp[high].slice(channel, channel+2),16) * fraction).toString(16).padStart(2,"0");
  return `#${mix(1)}${mix(3)}${mix(5)}`;
}
