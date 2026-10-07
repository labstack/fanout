import type { Cell, ColumnFormat, Panel, Status } from "./types";
import { statusFor } from "./thresholds";
import { formatValue } from "./units";
export type { ColumnFormat } from "./types";

export type ColumnDisplay = {
  unsupported?: string;
  kind: ColumnFormat["format"];
  text: string;
  fraction?: number;
  points?: (number | null)[];
  variable?: string;
  status?: Status | null;
};

export function columnDisplay(format: ColumnFormat, value: Cell, max: number, panel?: Panel, better?: "lower" | "higher", trend?: (number | null)[]): ColumnDisplay {
  // SQL-backed status measures can arrive as numeric strings, including zero.
  if (format.format === "status" && format.unit && typeof value === "string" && value.trim() !== "" && Number.isFinite(Number(value))) value = Number(value);
  const text = value === null ? "—" : String(value);
  const result: ColumnDisplay = { kind: format.format, text };
  if (["unit", "bar", "status", "sparkline"].includes(format.format)) result.text = typeof value === "number" ? formatValue(format.unit, value) : text;
  if (format.format === "bar") result.fraction = typeof value === "number" && max > 0 ? Math.max(0, Math.min(1, value / max)) : 0;
  if (format.format === "status" && typeof value === "number") result.status = statusFor(Number.isFinite(value) ? value : null, panel?.thresholds, panel?.better ?? better);
  if (format.format === "service_link") result.variable = format.variable;
  if (format.format === "sparkline" && trend) {
    result.points = trend.slice(0, 240);
    return result;
  }
  if (format.format === "sparkline") {
    try {
      const parsed: unknown = JSON.parse(text);
      if (Array.isArray(parsed)) result.points = parsed.slice(0, 240).map(v => typeof v === "number" && Number.isFinite(v) ? v : null);
      else result.unsupported = "Sparkline requires an array column";
    } catch {
      result.unsupported = "Sparkline requires an array column";
    }
  }
  return result;
}
