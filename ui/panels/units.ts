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

/** Readable chart/table labels use a space before duration units. */
export function formatLabel(unit: string | undefined, value: number | null): string {
  return formatAxis(unit)(value ?? NaN).replace(/(?<=\d)(ms|ns|s|m|h)\b/g, " $1");
}

/** Choose 1/2/5 duration steps in milliseconds, seconds, minutes or hours. */
export function niceDurationInterval(min: number, max: number, unit?: string): number | undefined {
  const factor = unit === "ms" ? 1 : unit === "s" ? 1000 : unit === "ns" ? 1e-6 : undefined;
  if (factor === undefined || !Number.isFinite(min) || !Number.isFinite(max)) return undefined;
  const target = Math.max((max - min) * factor / 6, Number.EPSILON);
  const steps = [1, 10, 100, 1000, 10000, 60000, 600000, 3600000, 36000000].flatMap(base => [1, 2, 5].map(n => base * n)).sort((a, b) => a - b);
  const step = steps.find(step => step >= target) ?? [1, 2, 5, 10].map(n => n * 10 ** Math.floor(Math.log10(target / 3600000)) * 3600000).find(step => step >= target)!;
  return step / factor;
}

/** Use one duration scale for both ends of a bucket. */
export function formatBucket(lower: number | null, upper: number | null, unit?: string): string {
  let bound = (value: number) => formatValue(unit, value);
  let suffix = "";
  if (unit === "ms" || unit === "s" || unit === "ns") {
    const ms = unit === "s" ? 1000 : unit === "ns" ? 1e-6 : 1;
    const magnitude = Math.abs((lower ?? upper ?? 0) * ms);
    const scale = magnitude >= 60000 ? 60000 : magnitude >= 1000 ? 1000 : 1;
    suffix = scale === 60000 ? " min" : scale === 1000 ? " s" : " ms";
    const digits = scale === 60000 ? 1 : scale === 1000 ? (magnitude < 10000 ? 1 : 0) : 2;
    const formatter = new Intl.NumberFormat(undefined, { maximumFractionDigits: digits });
    bound = value => formatter.format(value * ms / scale);
  }
  if (upper === null && lower !== null) return `≥${bound(lower)}${suffix}`;
  if ((lower === null || lower === 0) && upper !== null) return `<${bound(upper)}${suffix}`;
  return `${lower === null ? "−∞" : bound(lower)}–${upper === null ? "∞" : bound(upper)}${suffix}`;
}

/** ECharts otherwise abbreviates midnight to a bare day number. */
export function formatTimeAxis(value: number): string {
  const date = new Date(value);
  return date.getHours() === 0 && date.getMinutes() === 0 && date.getSeconds() === 0
    ? date.toLocaleDateString(undefined, { month: "short", day: "numeric" })
    : date.toLocaleTimeString(undefined, { hour: "2-digit", minute: "2-digit", hourCycle: "h23" });
}

/** Compact table timestamps in the viewer's locale and local time zone. */
export function formatTimestamp(value: number, now = new Date(), locale?: string): string {
  const date = new Date(value);
  if (!Number.isFinite(date.getTime())) return "—";
  const parts = new Intl.DateTimeFormat(locale, { month: "short", day: "numeric", ...(date.getFullYear() !== now.getFullYear() ? { year: "numeric" as const } : {}), hour: "2-digit", minute: "2-digit", second: "2-digit", hourCycle: "h23" }).formatToParts(date);
  // Retain locale field order, but remove verbose punctuation between date/time.
  const dateParts = parts.filter(p => ["month", "day", "year"].includes(p.type)).map(p => p.value);
  const timeParts = parts.filter(p => ["hour", "minute", "second"].includes(p.type)).map(p => p.value);
  return `${dateParts.join(" ")} ${timeParts.join(":")}`;
}
