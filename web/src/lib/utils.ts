import { clsx, type ClassValue } from "clsx";
import { twMerge } from "tailwind-merge";

export function cn(...inputs: ClassValue[]): string {
  return twMerge(clsx(inputs));
}

/** Format a number for display: compact large values, round small ones. */
export function fmt(v: number): string {
  if (!Number.isFinite(v)) return "—";
  const abs = Math.abs(v);
  if (abs >= 1_000_000) return (v / 1_000_000).toFixed(1) + "M";
  if (abs >= 10_000) return (v / 1_000).toFixed(1) + "k";
  if (abs >= 1_000) return (v / 1_000).toFixed(2) + "k";
  if (Number.isInteger(v)) return v.toLocaleString();
  if (abs >= 100) return v.toFixed(1);
  if (abs >= 1) return v.toFixed(2);
  return v.toPrecision(3);
}
