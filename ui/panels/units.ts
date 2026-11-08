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
  if (value !== 0 && ms === 0) return `${small(value)} ${unit}`;
  return ms !== 0 && Math.abs(ms) < 1 ? `${small(ms)} ms` : duration(ms);
}

const byteUnits = ["B", "KiB", "MiB", "GiB", "TiB"];

function bytes(value: number): string {
  let v = value;
  let i = 0;
  while (Math.abs(v) >= 1024 && i < byteUnits.length - 1) {
    v /= 1024;
    i += 1;
  }
  return `${i === 0 ? (v !== 0 && Math.abs(v) < 1 ? small(v) : Math.round(v)) : v.toFixed(1)} ${byteUnits[i]}`;
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

const durationFactor: Record<string, number> = { ms: 1, s: 1000, ns: 1e-6 };
const durationScales = [[3_600_000, "h"], [60_000, "m"], [1000, "s"], [1, "ms"]] as const;

/** Axis ticks: the same units, shorter. Given the axis's bounds, a duration
 *  axis reads in one unit, so 0, 5s and 10s are not printed as 0ms, 5s, 10s. */
export function formatAxis(unit?: string, axis?: { min: number; max: number; interval: number }): (value: number) => string {
  const factor = unit === undefined ? undefined : durationFactor[unit];
  if (unit === "bytes" && axis && axis.interval > 0) {
    const extent = Math.max(Math.abs(axis.min), Math.abs(axis.max));
    // The largest binary unit the axis reaches whose ticks still read in two decimals.
    const power = [4, 3, 2, 1].find(p => extent >= 1024 ** p && Math.abs(axis.interval / 1024 ** p * 100 - Math.round(axis.interval / 1024 ** p * 100)) < 1e-6) ?? 0;
    return (value: number) => Number.isFinite(value) ? `${number(value / 1024 ** power)} ${byteUnits[power]}` : "—";
  }
  if (unit === "percent" && axis && axis.interval > 0) {
    // Every tick uses the decimals the step needs: 0.0%, 0.5%, 1.0%, not 0.00%, 0.5%, 1.00%.
    // A step finer than four decimals keeps the per-value format, which reads 2e-5%.
    const places = [0, 1, 2, 3, 4].find(p => Math.abs(axis.interval * 10 ** p - Math.round(axis.interval * 10 ** p)) < 1e-6);
    if (places !== undefined) {
      const fixed = new Intl.NumberFormat("en", { minimumFractionDigits: places, maximumFractionDigits: places });
      return (value: number) => Number.isFinite(value) ? `${fixed.format(value)}%` : "—";
    }
  }
  if (factor !== undefined && axis && axis.interval > 0) {
    const extent = Math.max(Math.abs(axis.min), Math.abs(axis.max)) * factor, step = axis.interval * factor;
    // The largest unit the axis reaches whose ticks still read in two decimals.
    const [scale, suffix] = durationScales.find(([size]) => extent >= size && Math.abs(step / size * 100 - Math.round(step / size * 100)) < 1e-6) ?? [1, "ms"];
    return (value: number) => Number.isFinite(value) ? `${number(value * factor / scale)} ${suffix}` : "—";
  }
  return (value: number) => {
    if ((!unit || unit === "count") && Math.abs(value) >= 1000) return number(value, compact);
    if (unit === "ms" || unit === "s" || unit === "ns") return formatValue(unit, value).replace(/(\.\d*?)0+([a-z]*)$/, "$1$2").replace(/\.([a-z]*)$/, "$1");
    return formatValue(unit, value);
  };
}

/** Whether axis ticks print the unit themselves, so an axis name need not repeat it. */
export function ticksShowUnit(unit?: string): boolean {
  return unit !== undefined && unit !== "count" && /[^\d\s]$/.test(formatAxis(unit)(1).trim());
}

/** Readable chart/table labels use a space before duration units. */
export function formatLabel(unit: string | undefined, value: number | null, axis?: { min: number; max: number; interval: number }): string {
  return formatAxis(unit, axis)(value ?? NaN).replace(/(?<=\d)(ms|ns|s|m|h)\b/g, " $1");
}

/** Value axes use about one tick per 70 pixels, including both endpoints. */
export function valueAxisTicks(pixels: number): number {
  return Math.max(3, Math.min(10, Math.round(pixels / 70))) - 1;
}
function niceStep(target: number, unit?: string): number {
  const factor = unit === "ms" ? 1 : unit === "s" ? 1000 : unit === "ns" ? 1e-6 : undefined;
  const ceil125 = (n: number) => {
    const base = 10 ** Math.floor(Math.log10(n));
    return [1, 2, 5, 10].map(k => k * base).find(step => step >= n)!;
  };
  if (unit === "bytes") {
    // Steps of 1, 2 or 5 whole bytes, or of the binary unit the ticks print in;
    // 1000 of one unit is 1 of the next, except in the largest, which keeps growing.
    const power = Math.min(4, Math.max(0, Math.floor(Math.log(target) / Math.log(1024))));
    const step = ceil125(Math.max(target / 1024 ** power, power === 0 ? 1 : 0));
    return (step >= 1000 && power < 4 ? 1024 : step) * 1024 ** power;
  }
  if (factor === undefined) return ceil125(target);
  const ms = target * factor;
  const steps = [1, 10, 100, 1000, 10000, 60000, 600000, 3600000, 36000000].flatMap(base => [1, 2, 5].map(n => base * n)).sort((a, b) => a - b);
  return (ms < 1 ? ceil125(ms) : steps.find(step => step >= ms) ?? ceil125(ms / 3600000) * 3600000) / factor;
}
export function valueAxis(min: number, max: number, unit: string | undefined, pixels: number) {
  min = Number.isFinite(min) ? Math.min(0, min) : 0;
  max = Number.isFinite(max) ? Math.max(0, max) : 1;
  if (max === min) max = min + 1;
  const splitNumber = valueAxisTicks(pixels);
  let interval = niceStep(Math.max(max - min, Number.EPSILON) / splitNumber, unit);
  // Rounding the bounds must not introduce more ticks than the pixel budget; the cap ends the search whatever the step.
  for (let round = 0; round < 64 && Math.ceil(max / interval) - Math.floor(min / interval) > splitNumber; round++) interval = niceStep(interval * 1.01, unit);
  return { min: Math.floor(min / interval) * interval, max: Math.ceil(max / interval) * interval, interval, splitNumber, fanout_unit: unit };
}
/** Recompute after the native plot or annotation band changes its height. */
export function adaptValueAxis(axis: Record<string, unknown>, pixels: number): Record<string, unknown> {
  if (axis.type !== "value" && axis.type !== "log") return axis;
  if (axis.type !== "value" || typeof axis.min !== "number" || typeof axis.max !== "number") return { ...axis, splitNumber: valueAxisTicks(pixels) };
  const unit = typeof axis.fanout_unit === "string" ? axis.fanout_unit : undefined;
  const next = valueAxis(axis.min, axis.max, unit, pixels);
  // New ticks may need another duration or binary unit.
  return { ...axis, ...next, ...(unit && (unit in durationFactor || unit === "bytes" || unit === "percent") ? { axisLabel: { ...(axis.axisLabel as object), formatter: formatAxis(unit, next) } } : {}) };
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
