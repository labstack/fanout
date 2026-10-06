import {
  Alert,
  Button,
  Drawer,
  Group,
  Loader,
  Stack,
  Text,
  useComputedColorScheme,
} from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { TraceLogs, Waterfall } from "./trace/trace-components";
import type {
  DashboardSpec,
  DashboardTime,
  VarValue,
} from "../../../panels/types";
import { getTrace, queryExemplars } from "./api";
import type { DrillTarget } from "./drill-state";
import { LogsViz } from "./viz/logs";
export type DrillProps = {
  spec: DashboardSpec;
  time: DashboardTime;
  vars: Record<string, VarValue>;
  target?: DrillTarget;
  onChange(target?: DrillTarget): void;
};
export function DrillDrawer({
  spec,
  time,
  vars,
  target,
  onChange,
}: DrillProps) {
  const dark = useComputedColorScheme("light") === "dark";
  const panel = spec.panels.find((p) => p.id === target?.panel_id);
  const directLogs = target?.kind === "logs" && panel?.query?.from === "logs";
  const exemplars = useQuery({
    queryKey: ["exemplars", spec, target, vars],
    queryFn: ({ signal }) =>
      queryExemplars(
        {
          dashboard: spec,
          panel_id: target!.panel_id,
          kind: directLogs ? "logs" : "traces",
          time: {
            from: target!.window_from,
            to: target!.window_to,
            refresh: "off",
          },
          from: target!.from,
          to: target!.to,
          dimensions: target!.dimensions,
          bucket: target!.bucket,
          vars,
        },
        signal,
      ),
    enabled: Boolean(target && panel && !target.trace_id),
    retry: false,
  });
  const trace = useQuery({
    queryKey: ["drill-trace", target],
    queryFn: ({ signal }) => getTrace(target!, signal),
    enabled: Boolean(target?.trace_id),
    retry: false,
  });
  return (
    <Drawer
      opened={Boolean(target)}
      onClose={() => onChange(undefined)}
      title={
        panel
          ? `${panel.title} · ${target?.kind}`
          : "Panel selection unavailable"
      }
      position="right"
      size="xl"
    >
      {!panel && (
        <Alert color="bad">This panel is no longer in the dashboard.</Alert>
      )}
      {(exemplars.isFetching || trace.isFetching) && (
        <Loader aria-label="Loading drill data" size="sm" />
      )}
      {(exemplars.error || trace.error) && (
        <Alert color="bad">{(exemplars.error ?? trace.error)?.message}</Alert>
      )}
      {directLogs && exemplars.data?.logs && panel && (
        <LogsViz
          panel={{ ...panel, viz: "logs" }}
          result={{
            id: panel.id,
            status: exemplars.data.logs.rows ? "ok" : "empty",
            frame: exemplars.data.logs,
            elapsed_ms: 0,
            from_ms: Date.parse(target!.from),
            to_ms: Date.parse(target!.to),
          }}
          dark={dark}
          height={500}
        />
      )}
      {directLogs && exemplars.data?.logs?.rows === 0 && (
        <Text c="dimmed">No logs match this selection.</Text>
      )}
      {!directLogs && !target?.trace_id && exemplars.data && (
        <Stack gap="xs">
          {exemplars.data.traces.length === 0 && (
            <Text c="dimmed">No exemplar traces match this selection.</Text>
          )}
          {exemplars.data.traces.map((t) => (
            <Button
              key={`${t.namespace}/${t.trace_id}`}
              variant="default"
              justify="space-between"
              onClick={() =>
                onChange({
                  ...target!,
                  trace_id: t.trace_id,
                  namespace: t.namespace,
                })
              }
            >
              {t.service} · {t.operation} · {t.duration_ms.toFixed(1)} ms
            </Button>
          ))}
          {exemplars.data.truncated && (
            <Text size="xs" c="dimmed">
              Showing at most 20 exemplar traces.
            </Text>
          )}
        </Stack>
      )}
      {trace.data && (
        <Stack gap="sm">
          <Group>
            <Text fw={600}>{trace.data.data.trace_id}</Text>
            <Text c={trace.data.data.has_error ? "bad" : "dimmed"}>
              {trace.data.data.has_error ? "■ Error" : "● OK"}
            </Text>
          </Group>
          {trace.data.data.spans.length ? (
            <Waterfall
              spans={trace.data.data.spans}
              dark={dark}
              onSpan={() => undefined}
            />
          ) : (
            <Text c="dimmed">No spans were found for this trace.</Text>
          )}
          <Text fw={600}>Correlated logs</Text>
          <TraceLogs entries={trace.data.data.logs} />
          {trace.data.data.truncated && (
            <Alert color="warn">
              This trace is truncated: {trace.data.data.spans.length} of{" "}
              {trace.data.data.span_count} spans,{" "}
              {trace.data.data.services.length} of{" "}
              {trace.data.data.service_count} services.
            </Alert>
          )}
        </Stack>
      )}
    </Drawer>
  );
}
