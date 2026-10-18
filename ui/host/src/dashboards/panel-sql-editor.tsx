import { Alert, Box, Button, Group, Modal, Stack, Text } from "@mantine/core";
import { useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState } from "react";
import type { DashboardSpec, DashboardTime, Panel, VarValue } from "../../../panels/types";
import { interpolate } from "../../../panels/variables";
import { SqlEditor } from "../sql-editor/sql-editor";
import type { SqlProblem } from "../sql-editor/diagnostics";
import { ApiError, dashboardsKey, getTelemetrySchema, patchDashboard, queryPanels } from "./api";
import { PanelCard } from "./panel-card";
import { classifyPanelResult, type PanelDisplayResult } from "./panel-result";

type PanelSqlEditorProps = {
  dashboardId: string; version: number; spec: DashboardSpec; panel: Panel;
  time: DashboardTime; vars: Record<string, VarValue>; onClose(): void;
};

export default function PanelSqlEditor({ dashboardId, version, spec, panel, time, vars, onClose }: PanelSqlEditorProps) {
  const client = useQueryClient();
  const [sql, setSql] = useState(panel.sql ?? "");
  const [problems, setProblems] = useState<SqlProblem[]>([]);
  const [error, setError] = useState<Error>();
  const [conflict, setConflict] = useState(false);
  const [result, setResult] = useState<{ panel: Panel; result: PanelDisplayResult }>();
  const [running, setRunning] = useState(false);
  const [saving, setSaving] = useState(false);
  const [closeIntent, setCloseIntent] = useState<"close" | "reload">();
  const busy = useRef(false);
  const controller = useRef<AbortController | undefined>(undefined);
  const mounted = useRef(false);
  const runButton = useRef<HTMLButtonElement>(null);
  const keepEditingButton = useRef<HTMLButtonElement>(null);
  const help = useId();
  const schema = useQuery({ queryKey: ["telemetry-schema"], queryFn: ({ signal }) => getTelemetrySchema(signal), staleTime: 60_000, retry: false });
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; controller.current?.abort(); }; }, []);
  useEffect(() => { if (closeIntent) keepEditingButton.current?.focus(); }, [closeIntent]);
  const title = interpolate(panel.title, vars);
  const dirty = sql !== (panel.sql ?? "");
  const close = (intent: "close" | "reload") => {
    if (intent === "reload") void client.invalidateQueries({ queryKey: ["dashboard", dashboardId] });
    onClose();
  };
  const requestClose = (intent: "close" | "reload" = "close") => {
    if (saving) return;
    if (dirty) setCloseIntent(intent);
    else close(intent);
  };
  const keepEditing = () => { setCloseIntent(undefined); runButton.current?.focus(); };
  const report = (failure: unknown) => {
    const error = failure instanceof Error ? failure : new Error("Request failed");
    setError(error);
    if (error instanceof ApiError) {
      if (error.code === "dashboard_version_conflict") setConflict(true);
      const index = spec.panels.findIndex(p => p.id === panel.id);
      // A dashboard-level or non-SQL validation error stays in the alert.
      setProblems(error.problems.filter(problem => problem.path === `panels[${index}].sql` || problem.path === `panels[${index}]`));
    }
  };
  const run = async () => {
    if (busy.current) return;
    busy.current = true;
    setRunning(true); setError(undefined); setProblems([]); setResult(undefined);
    const edited = { ...panel, sql };
    const abort = new AbortController(); controller.current = abort;
    try {
      const width = runButton.current?.closest('[role="dialog"]')?.clientWidth || 800;
      const results = await queryPanels({ dashboard: { ...spec, panels: spec.panels.map(p => p.id === panel.id ? edited : p) }, panels: [panel.id], time, vars, compare: time.compare === "previous_period", widths: { [panel.id]: width } }, abort.signal);
      if (!mounted.current || abort.signal.aborted) return;
      const next = results.find(r => r.id === panel.id);
      if (!next) throw new Error("The preview returned no result for this panel.");
      setResult({ panel: edited, result: classifyPanelResult(next, [panel.id]) });
      if (next.status === "error" && !next.retryable) setProblems([{ path: "sql", message: next.error ?? "Query failed" }]);
    } catch (failure) { if (mounted.current && !abort.signal.aborted) report(failure); }
    finally { busy.current = false; if (mounted.current) setRunning(false); }
  };
  const save = async () => {
    if (busy.current || sql === panel.sql || conflict) return;
    busy.current = true; setSaving(true); setError(undefined); setProblems([]);
    try {
      const record = await patchDashboard(dashboardId, [{ op: "update_panel", id: panel.id, set: { sql } }], version, `Edited SQL for ${panel.title}`);
      await client.cancelQueries({ queryKey: ["dashboard", dashboardId], exact: true });
      client.setQueryData(["dashboard", dashboardId], record);
      void client.invalidateQueries({ queryKey: dashboardsKey });
      void client.invalidateQueries({ queryKey: ["dashboard-versions", dashboardId] });
      if (mounted.current) onClose();
    } catch (failure) { if (mounted.current) report(failure); }
    finally { busy.current = false; if (mounted.current) setSaving(false); }
  };
  // Let CodeMirror handle Escape before the dialog's bubbling handler; Mantine's
  // built-in handler runs on window capture and would discard the draft first.
  return <Modal opened onClose={() => requestClose()} title={`Edit SQL · ${title}`} size="min(1000px, 100vw - 32px)" closeOnEscape={false} closeButtonProps={{ disabled: saving, "aria-label": "Close SQL editor" }} onKeyDown={event => {
    if (event.key !== "Escape" || event.nativeEvent.isComposing) return;
    event.preventDefault(); event.stopPropagation();
    if (closeIntent) keepEditing();
    else requestClose();
  }}>
    <Stack gap="md" data-panel-sql-editor>
      <Text id={help} size="sm" c="dimmed">Run with ⌘/Ctrl+Enter. Escape leaves the SQL field. Tab moves to the next control. Save creates a new dashboard version.</Text>
      {closeIntent && <Alert color="warn" title="Discard SQL changes?">
        <Text size="sm">Your SQL changes have not been saved.</Text>
        <Group gap="xs" mt="sm"><Button ref={keepEditingButton} variant="default" onClick={keepEditing}>Keep editing</Button><Button color="bad" disabled={saving} onClick={() => close(closeIntent)}>Discard changes</Button></Group>
      </Alert>}
      {schema.error && <Alert color="warn" title="SQL completion unavailable">{schema.error.message}<Button size="compact-xs" variant="default" ml="xs" onClick={() => void schema.refetch()}>Retry schema</Button></Alert>}
      <SqlEditor value={sql} onChange={value => { setSql(value); setCloseIntent(undefined); setProblems([]); if (!conflict) setError(undefined); }} onRun={() => void run()} onEscape={() => runButton.current?.focus()} schema={schema.data} problems={problems} describedBy={help} disabled={saving || running} />
      <Group justify="space-between">
        <Button ref={runButton} variant="default" loading={running} disabled={saving} onClick={() => void run()}>Run</Button>
        <Group gap="xs"><Button variant="default" disabled={saving} onClick={() => requestClose()}>Cancel</Button><Button loading={saving} disabled={running || sql === panel.sql || conflict} onClick={() => void save()}>Save SQL</Button></Group>
      </Group>
      {conflict ? <Alert color="warn">Someone saved this dashboard since you opened it. Load the latest version, then redo your change. <Button size="compact-sm" onClick={() => requestClose("reload")}>Load latest</Button></Alert>
        : error && <Alert color="bad" title="SQL could not be applied">{error.message}{error instanceof ApiError && error.problems.length > 0 && <ul>{error.problems.map((problem, index) => <li className="panel-sql-problem" key={index}>{problem.path}: {problem.message}{problem.hint ? ` (${problem.hint})` : ""}</li>)}</ul>}</Alert>}
      {result && <Stack gap="xs"><Text size="sm" fw={600} role="status">{result.panel.sql === sql ? "Preview result" : "Preview of the last run · SQL has changed"}</Text><Box h={360}>
        <PanelCard panel={result.panel} title={title} result={result.result} loading={running} height={360} group={`sql-preview-${dashboardId}-${panel.id}`} editing={false} agentAvailable={false} vars={vars} compare={time.compare === "previous_period"} range={panel.time?.range ?? time.range} onRetry={() => void run()} />
      </Box></Stack>}
    </Stack>
  </Modal>;
}
