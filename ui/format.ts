export const integer = new Intl.NumberFormat(undefined, { maximumFractionDigits: 0 });

/** A rate as a percentage. A rate that is small but real is said to be small
 *  rather than rounded away: "0.00%" beside a red health badge reads as a
 *  contradiction, and the reader cannot tell it from a service with no errors
 *  at all. */
export function percent(value: number) {
  if (!Number.isFinite(value)) return "—";
  if (value > 0 && value < 0.0001) return "<0.01%";
  return `${(value * 100).toFixed(value >= 0.1 ? 1 : 2)}%`;
}

/** A duration in the largest unit that still says something precise.
 *
 *  Ten minutes was printed as "600.00s": two decimals of false precision on a
 *  number nobody reads in seconds, and it sat in the same column as "25.0ms",
 *  so a glance could not compare them. Above a minute the value reads in
 *  minutes and hours, and the decimals shrink as the magnitude grows. */
export function duration(value: number): string {
  if (!Number.isFinite(value)) return "—";
  // A negative duration is bad data, not missing data, and saying so is more
  // use than an em dash that reads as "nothing recorded".
  if (value < 0) return `-${duration(-value)}`;
  if (value === 0) return "0ms";
  // Small but real, for the same reason percent says "<0.01%": a four-microsecond
  // span is not a zero-length one, and the waterfall is full of them.
  if (value < 0.01) return "<0.01ms";
  if (value < 1) return `${value.toFixed(2)}ms`;
  // Each unit decides its precision on the value it will actually print, so a
  // number never rounds up into the next unit's territory — 999.5ms was
  // rendered "1000ms", four digits of milliseconds in a column of seconds.
  if (Math.round(value * 10) / 10 < 100) return `${value.toFixed(1)}ms`;
  if (Math.round(value) < 1000) return `${Math.round(value)}ms`;

  const totalSeconds = Math.round(value / 100) / 10;
  if (totalSeconds < 10) return `${(value / 1000).toFixed(2)}s`;
  if (totalSeconds < 60) return `${totalSeconds.toFixed(1)}s`;

  const whole = Math.round(value / 1000);
  const minutes = Math.floor(whole / 60);
  const seconds = whole % 60;
  if (minutes < 60) return seconds === 0 ? `${minutes}m` : `${minutes}m ${seconds}s`;
  const hours = Math.floor(minutes / 60);
  const restMinutes = minutes % 60;
  return restMinutes === 0 ? `${hours}h` : `${hours}h ${restMinutes}m`;
}

export function windowLabel(window: string) {
  const [startValue, endValue] = window.split("/");
  const start = new Date(startValue);
  const end = new Date(endValue);
  if (Number.isNaN(start.valueOf()) || Number.isNaN(end.valueOf())) return window;
  const minutes = Math.round((end.valueOf() - start.valueOf()) / 60_000);
  if (minutes >= 60 && minutes % 60 === 0) return `Last ${minutes / 60}h`;
  return `Last ${Math.max(minutes, 1)}m`;
}

export function timelineTimestamp(value: string, window: string, seconds = false) {
  const date = new Date(value);
  if (Number.isNaN(date.valueOf())) return value;
  const [startValue, endValue] = window.split("/");
  const start = new Date(startValue);
  const end = new Date(endValue);
  const multiDay = !Number.isNaN(start.valueOf()) && !Number.isNaN(end.valueOf()) && end.valueOf() - start.valueOf() > 24 * 60 * 60 * 1000;
  return date.toLocaleString([], {
    ...(multiDay ? { month: "short", day: "numeric" } as const : {}),
    hour: "numeric",
    minute: "2-digit",
    ...(seconds ? { second: "2-digit" } as const : {}),
  });
}

/** The viewer's time zone, abbreviated the way their locale writes it.
 *
 *  Every timestamp in the product is rendered in the browser's zone and none
 *  of them said so, which is a real ambiguity when the reader is looking at an
 *  incident with someone in another office, or at a server that logs in UTC.
 *
 *  `when` matters: an abbreviation is a property of an instant, not of a zone.
 *  A table of rows from before a daylight-saving change headed "PST" when its
 *  rows read PDT is a worse answer than no heading at all, so a caller labels
 *  the rows it is actually showing. */
export function timeZoneLabel(when: Date | string | number = new Date()) {
  const at = when instanceof Date ? when : new Date(when);
  if (Number.isNaN(at.valueOf())) return timeZoneLabel(new Date());
  const parts = new Intl.DateTimeFormat([], { timeZoneName: "short" }).formatToParts(at);
  return parts.find((part) => part.type === "timeZoneName")?.value ?? "";
}

/** The whole instant — date, seconds and zone — for the title of a timestamp
 *  that is displayed shortened. Takes whatever the caller holds, so nobody has
 *  to round-trip an epoch through toISOString, which throws on a bad value
 *  where this returns it unchanged. */
export function exactTimestamp(value: string | number | Date) {
  const date = value instanceof Date ? value : new Date(value);
  if (Number.isNaN(date.valueOf())) return String(value);
  return date.toLocaleString([], { year: "numeric", month: "short", day: "numeric", hour: "numeric", minute: "2-digit", second: "2-digit", timeZoneName: "short" });
}
