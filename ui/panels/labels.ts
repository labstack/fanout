import type { Column, Panel } from "./types";

const abbreviations = new Set(["id", "http", "https", "rpc", "grpc", "cpu", "sql", "url", "uri", "ip", "io", "db", "gc", "tls", "api"]);
const units: Record<string, string> = { ms: "ms", ns: "ns", s: "s", us: "µs", bytes: "bytes", percent: "%", ratio: "ratio", per_second: "/s", per_minute: "/min" };

const productNames: Record<string, string> = { body_template: "message template", parquet_index: "traces" };

/** Display only: frame column names, row keys and selections stay unchanged. */
export function displayLabel(column: Column | string, panel?: Panel): string {
  const name = typeof column === "string" ? column : column.name;
  const explicitUnit = panel?.options?.columns?.find(c => c.field === name)?.unit
    ?? (typeof column === "string" ? undefined : column.unit)
    ?? (typeof column !== "string" && column.role === "measure" ? panel?.unit : undefined);
  let label = productNames[name] ?? name;
  if (/\b(?:duckdb|parquet|sqlite|variant|timestamptz_ns|timestamp_ns)\b|\btelemetry\./i.test(label)) label = "value";
  // Unaliased structured measures and common column suffixes share the same
  // wording; no SQL expressions are needed to understand a chart's labels.
  label = label.replace(/^(\w+)\(([^()]*)\)$/, (_match, fn: string, field: string) => [fn, field].filter(Boolean).join("_"));
  const suffix = label.match(/_(ms|ns|us|s|bytes|percent)$/i);
  if (suffix) label = label.slice(0, -suffix[0].length);
  const unit = explicitUnit ?? suffix?.[1]?.toLowerCase();
  label = label.split("_").map((word, i) => (/^p\d+$/i.test(word) || abbreviations.has(word.toLowerCase()))
    ? word.toUpperCase() : i === 0 ? word.charAt(0).toUpperCase() + word.slice(1) : word).join(" ");
  return label + (unit && unit !== "none" && unit !== "count" ? ` (${units[unit] ?? unit})` : "");
}
