import {
  Alert,
  Button,
  Drawer,
  Loader,
  Stack,
  Text,
  useComputedColorScheme,
} from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { TraceDetailView } from "./trace/detail";
import type {
  DashboardSpec,
  DashboardTime,
  VarValue,
} from "../../../panels/types";
import type { DrillClient } from "./drill-client";
import type { DrillTarget } from "./drill-state";
import { LogsViz } from "./viz/logs";
export type DrillProps = {
  client: DrillClient;
  spec: DashboardSpec;
  time: DashboardTime;
  vars: Record<string, VarValue>;
  target?: DrillTarget;
  onChange(target?: DrillTarget): void;
};
export function DrillDrawer({
  client,
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
      client.exemplars(
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
    queryFn: ({ signal }) => client.trace(target!, signal),
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
      {target?.trace_id && <Button variant="subtle" mb="sm" onClick={() => {
        const { trace_id: _traceId, namespace: _namespace, ...next } = target;
        onChange(next);
      }}>Back to traces</Button>}
      {!panel && (
        <Alert color="bad">This panel is no longer in the dashboard.</Alert>
      )}
      {(exemplars.isFetching || trace.isFetching) && (
        <Loader role="status" aria-label="Loading drill data" size="sm" />
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
      {trace.data && <TraceDetailView result={trace.data} dark={dark} />}
    </Drawer>
  );
}
