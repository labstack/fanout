import { Alert, Anchor, Group, Loader, Stack, Text } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import type { Cell } from "../../../panels/types";
import { queryPanels } from "../dashboards/api";
import { Attributes } from "../dashboards/trace/span-details";

type LogExtras = { attributes: Record<string, unknown>; resource: Record<string, unknown>; span_id: string; scope: string };

const sqlText = (value: string) => `'${value.replaceAll("'", "''")}'`;
const object = (value: Cell): Record<string, unknown> => {
  const parsed: unknown = typeof value === "string" && value ? JSON.parse(value) : null;
  return parsed && typeof parsed === "object" && !Array.isArray(parsed) ? parsed as Record<string, unknown> : {};
};

/** A log row carries no ID, so its attributes are read back by its millisecond, service, namespace, trace and redacted body. */
async function logExtras(row: Record<string, Cell>, signal?: AbortSignal): Promise<LogExtras> {
  const at = Number(row.time);
  // The table's millisecond is rounded, so the stored instant is within half of one either side.
  const time = { from: new Date(at - 1).toISOString(), to: new Date(at + 1).toISOString(), refresh: "off" as const };
  const body = Array.from(String(row.body ?? ""));
  // The logs table cuts bodies over 2,000 characters to 1,999 and an ellipsis.
  const bodyMatch = body.length === 2000 && body[1999] === "…" ? `starts_with(body, ${sqlText(body.slice(0, 1999).join(""))})` : `coalesce(body, '') = ${sqlText(body.join(""))}`;
  const sql = `SELECT attributes, resource, coalesce(span_id, ''), coalesce(scope_name, '') FROM logs WHERE $__window(time) AND coalesce(service, '') = ${sqlText(String(row.service ?? ""))}`
    + ` AND coalesce(namespace, '') = ${sqlText(String(row.namespace ?? ""))} AND coalesce(trace_id, '') = ${sqlText(String(row.trace_id ?? ""))} AND ${bodyMatch} LIMIT 1`;
  const [result] = await queryPanels({ dashboard: { version: 1, name: "Log", time, panels: [{ id: "log", title: "Log", viz: "table", sql }] }, panels: ["log"], time }, signal);
  if (result?.status === "error" || result?.status === "invalid") throw new Error(result.error ?? "The log details could not load.");
  const values = result?.frame?.values;
  if (!values || !result.frame?.rows) throw new Error("This log is no longer stored.");
  return { attributes: object(values[0][0]), resource: object(values[1][0]), span_id: String(values[2][0] ?? ""), scope: String(values[3][0] ?? "") };
}

export function LogDetails({ row, onTrace }: { row: Record<string, Cell>; onTrace?: () => void }) {
  const extras = useQuery({ queryKey: ["log-extras", row], queryFn: ({ signal }) => logExtras(row, signal), retry: false, staleTime: Infinity });
  const traceId = typeof row.trace_id === "string" ? row.trace_id : "";
  return <Stack gap="xs" py={4} data-log-details onClick={event => event.stopPropagation()} style={{ cursor: "auto" }}>
    {traceId && <Group gap="xs" wrap="wrap">
      <Text size="xs" c="dimmed">Trace</Text>
      {onTrace ? <Anchor component="button" type="button" size="xs" ff="monospace" onClick={onTrace} style={{ overflowWrap: "anywhere" }}>{traceId}</Anchor> : <Text size="xs" ff="monospace">{traceId}</Text>}
      {extras.data?.span_id && <><Text size="xs" c="dimmed">Span</Text><Text size="xs" ff="monospace">{extras.data.span_id}</Text></>}
    </Group>}
    {extras.isFetching && <Loader size="xs" role="status" aria-label="Loading log details" />}
    {extras.error && <Alert color="bad" p="xs">{extras.error.message}</Alert>}
    {extras.data && <>
      <Attributes label="Attributes" values={extras.data.attributes} />
      <Attributes label="Resource" values={extras.data.resource} />
      {extras.data.scope && <Text size="xs" c="dimmed">Instrumentation scope: {extras.data.scope}</Text>}
    </>}
  </Stack>;
}
