import { duration, percent } from "../format";

const compact = new Intl.NumberFormat("en", { notation: "compact", maximumFractionDigits: 1 });
const plain = new Intl.NumberFormat("en", { maximumFractionDigits: 2 });

function small(value: number): string {
  return Math.abs(value) < 1e-4 ? value.toExponential(2).replace(/\.?(0+)(?=e)/, "") : String(Number(value.toPrecision(3)));
}

function number(value: number, formatter = plain): string {
  return value !== 0 && Math.abs(value) < 1 ? small(value) : formatter.format(value);
}

function panelDuration(value: number, unit: string): string {
  const ms = unit === "s" ? value * 1000 : unit === "ns" ? value / 1e6 : value;
  // Keep the original unit if conversion underflows, rather than printing zero.
  if (value !== 0 && ms === 0) return `${small(value)}${unit}`;
  return ms !== 0 && Math.abs(ms) < 1 ? `${small(ms)}ms` : duration(ms);
}

function bytes(value: number): string {
  const units = ["B", "KiB", "MiB", "GiB", "TiB"];
  let v = value;
  let i = 0;
  while (Math.abs(v) >= 1024 && i < units.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${i === 0 ? (v !== 0 && Math.abs(v) < 1 ? small(v) : Math.round(v)) : v.toFixed(1)} ${units[i]}`;
}

/** One value in its unit. Durations always read in the largest unit that is
 *  still precise, so a column never prints 30.00s beside 25.0ms (#232 item 14). */
export function formatValue(unit: string | undefined, value: number | null | undefined): string {
  if (value === null || value === undefined || !Number.isFinite(value)) return "—";
  switch (unit) {
    case "ms":
    case "s":
    case "ns": return panelDuration(value, unit);
    case "percent": return value !== 0 && Math.abs(value) < 1 ? `${small(value)}%` : percent(value / 100);
    case "ratio": return `${number(value)}×`;
    case "per_second": return `${number(value, compact)}/s`;
    case "per_minute": return `${number(value, compact)}/min`;
    case "bytes": return bytes(value);
    case "count": return number(value, Math.abs(value) >= 10_000 ? compact : plain);
    default: return number(value, Math.abs(value) >= 10_000 ? compact : plain);
  }
}

/** Axis ticks: the same units, shorter. */
export function formatAxis(unit?: string): (value: number) => string {
  return (value: number) => {
    if (unit === "ms" || unit === "s" || unit === "ns") return formatValue(unit, value).replace(/(\.\d*?)0+([a-z]*)$/, "$1$2").replace(/\.([a-z]*)$/, "$1");
    return formatValue(unit, value);
  };
}
