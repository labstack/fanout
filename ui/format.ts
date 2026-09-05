export const integer = new Intl.NumberFormat(undefined, { maximumFractionDigits: 0 });

export function percent(value: number) {
  return `${(value * 100).toFixed(value >= 0.1 ? 1 : 2)}%`;
}

export function duration(value: number) {
  if (value >= 1000) return `${(value / 1000).toFixed(2)}s`;
  return `${value.toFixed(value >= 100 ? 0 : 1)}ms`;
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
