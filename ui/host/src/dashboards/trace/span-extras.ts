import type { TraceSpan } from "../../../../contracts";
import { queryPanels } from "../api";
import type { SpanEvent, SpanExtras } from "./span-details";

// Host only: MCP apps render span details without the host API, so the loader
// lives apart from the component they share.
const sqlText = (value: string) => `'${value.replaceAll("'", "''")}'`;
const parse = (value: unknown): unknown => typeof value === "string" && value ? JSON.parse(value) : value;
const object = (value: unknown): Record<string, unknown> => {
  const parsed = parse(value);
  return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed as Record<string, unknown> : {};
};
const list = (value: unknown): unknown[] => {
  const parsed = parse(value);
  return Array.isArray(parsed) ? parsed : [];
};

/** Reads one span's attributes, resource and events with a single-row SQL panel around its start. */
export async function spanExtras(traceId: string, span: TraceSpan, signal?: AbortSignal): Promise<SpanExtras> {
  const at = new Date(span.start).valueOf();
  const time = { from: new Date(at - 1).toISOString(), to: new Date(at + 2).toISOString(), refresh: "off" as const };
  // Event offsets are computed in SQL: nanosecond instants exceed a double's exact range.
  const sql = `SELECT attributes, resource, [epoch_ns(t) - start_unix_nano for t in event_times] AS event_offsets, event_names, event_attributes, scope_name FROM spans WHERE $__window(start_time) AND trace_id = ${sqlText(traceId)} AND span_id = ${sqlText(span.span_id)} LIMIT 1`;
  const [result] = await queryPanels({ dashboard: { version: 1, name: "Span", time, panels: [{ id: "span", title: "Span", viz: "table", sql }] }, panels: ["span"], time }, signal);
  if (result?.status === "error") throw new Error(result.error ?? "The span details could not load.");
  const values = result?.frame?.values;
  if (!values || !result.frame?.rows) throw new Error("This span is no longer stored.");
  const offsets = list(values[2][0]), names = list(values[3][0]), attributes = list(values[4][0]);
  const events: SpanEvent[] = names.map((name, index) => ({ offset_ns: Number(offsets[index] ?? 0), name: String(name ?? ""), attributes: object(attributes[index]) }));
  return { attributes: object(values[0][0]), resource: object(values[1][0]), events, scope: String(values[5][0] ?? "") };
}
