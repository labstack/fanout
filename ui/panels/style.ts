import { relativeLuminance } from "../theme";
import { bad, ok, warn } from "../tokens";
import type { ChartTheme } from "./compile";

function contrastRatio(a: string, b: string): number {
  const x = relativeLuminance(a), y = relativeLuminance(b);
  return (Math.max(x,y)+.05)/(Math.min(x,y)+.05);
}

export function tint(hex: string, alpha: number): string {
  return `rgba(${[1,3,5].map(i=>parseInt(hex.slice(i,i+2),16)).join(", ")}, ${alpha})`;
}

/** Retain identity hues; a contrast outline supplies the visible boundary. */
export function markStyle(color: string, theme: ChartTheme) {
  return { color, ...(contrastRatio(color,theme.surface) < 3 ? {borderColor:theme.text,borderWidth:1} : {}) };
}

export function lineStyle(color: string, theme: ChartTheme) {
  return { color, ...(contrastRatio(color,theme.surface) < 3 ? {shadowColor:theme.text,shadowBlur:2} : {}) };
}

/** Semantic ink is darker than a mark in light mode; tokens remain intact. */
export function statusInk(status: "ok" | "warn" | "bad", dark: boolean): string {
  return ({ok,warn,bad})[status][dark ? 5 : 8];
}
