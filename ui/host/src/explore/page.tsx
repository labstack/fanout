import { Alert, Box, Button, Group, SegmentedControl, Stack, Tabs, Text, Title, Tooltip } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState } from "react";
import type { DashboardSpec, Panel, Query, VarValue, Viz } from "../../../panels/types";
import { ApiError, type Problem } from "../api-error";
import { getTelemetrySchema, queryPanels } from "../dashboards/api";
import { DrillDrawer } from "../dashboards/drill";
import { parseDrill } from "../dashboards/drill-state";
import { drillSelection } from "../dashboards/panel-handlers";
import { PanelCard } from "../dashboards/panel-card";
import { exploreDrillClient } from "./drill";
import { classifyPanelResult, type PanelDisplayResult } from "../dashboards/panel-result";
import { RangeMenu } from "../dashboards/toolbar";
import { FanqlEditor } from "../fanql-editor/fanql-editor";
import { SqlEditor } from "../sql-editor/sql-editor";
import { completeFanql, formatFanql, parseFanql, type Formatted, type Notice, type Parsed, type VizCompat } from "./api";
import { Builder } from "./builder";
import { SaveDialog, defaultTitle } from "./save-dialog";
import { exploreTime, type ExploreSearch } from "./search";

const explorePanelID = "explore";
const resultHeight = 420;

const vizLabel: Record<Viz, string> = {
  stat: "Stat", gauge: "Gauge", timeseries: "Time series", bar: "Bar", table: "Table", text: "Text", heatmap: "Heatmap", histogram: "Histogram",
  scatter: "Scatter", state_timeline: "State timeline", logs: "Logs", log_patterns: "Log patterns", traces: "Traces", service_map: "Service map", health: "Health",
};
const requirementLabel = { min_max: "a minimum and maximum", thresholds: "thresholds" } as const;

/** Query and Builder are two views of one FanQL query; SQL is separate. */
type Kind = "query" | "sql";
const kindOf = (tab: ExploreSearch["tab"]): Kind => tab === "sql" ? "sql" : "query";
const starter: Query = { from: "spans", measures: ["count()"], by: ["service"] };

/** What the last run sent, so later edits can be shown as stale and not saved. */
type ExploreRun = { kind: Kind; source: string; window: string; panel: Panel; result: PanelDisplayResult; canonical?: string };

/** New panels must return rows; an edited panel may be legitimately empty now, so a clean run is enough. */
function canKeep(run: ExploreRun | undefined, editing: boolean): boolean {
  // A run with no rows reports "empty", not "ok".
  if (editing) return run?.result.status === "ok" || run?.result.status === "empty";
  return run?.result.status === "ok" && (run.result.frame?.rows ?? 0) > 0;
}

/** Explore inside a dashboard: its variables resolve, and saving writes the panel there. */
export type DashboardContext = {
  spec: DashboardSpec; vars: Record<string, VarValue>;
  /** Saves the last run's panel; a rejection's message is shown. */
  save: { label: string; run(panel: Panel): Promise<void> };
  /** The panel being edited: its min, max and thresholds satisfy viz requirements. */
  panel?: Panel;
  /** Turns a run's draft into the panel to preview and save, carrying the edited panel's settings. */
  compose?(draft: Panel): Panel;
  /** Reports whether the query or SQL differs from what the editor opened with. */
  onDirtyChange?(dirty: boolean): void;
  /** Closes the editor without saving; the host asks before discarding a draft. */
  cancel?(): void;
  /** Seeds the Builder when the panel's query cannot be edited as text. */
  initial?: { query: Query; formatted: Formatted };
};

export function ExplorePage({ search, onSearch, onSaved, context }: {
  search: ExploreSearch; onSearch(next: ExploreSearch, replace?: boolean): void;
  /** Opens the dashboard the result was saved to. */
  onSaved?(dashboardId: string): void;
  context?: DashboardContext;
}) {
  const variables = context?.spec.variables;
  const base = context?.panel;
  const satisfied = (c: VizCompat) => c.requires.every(r => r === "min_max" ? base?.min !== undefined && base?.max !== undefined : (base?.thresholds?.length ?? 0) > 0);
  const compose = (draft: Panel) => context?.compose ? context.compose(draft) : draft;
  const vars = context?.vars ?? {};
  const [savingError, setSavingError] = useState<string>();
  const [savingPanel, setSavingPanel] = useState(false);
  const [saving, setSaving] = useState<Panel>();
  const [text, setText] = useState(search.q);
  const [sql, setSql] = useState(search.sql ?? "");
  const opened = useRef({ text: search.q, sql: search.sql ?? "" });
  const dirty = text !== opened.current.text || sql !== opened.current.sql;
  const onDirtyChange = context?.onDirtyChange;
  useEffect(() => { onDirtyChange?.(dirty); }, [dirty, onDirtyChange]);
  const runButton = useRef<HTMLButtonElement>(null);
  const [parsed, setParsed] = useState<Parsed & { text: string }>();
  const [built, setBuilt] = useState<Query | undefined>(context?.initial?.query);
  const [formatted, setFormatted] = useState<Formatted | undefined>(context?.initial?.formatted);
  const [formatProblems, setFormatProblems] = useState<Problem[]>([]);
  const formatter = useRef<{ abort?: AbortController; timer?: ReturnType<typeof setTimeout> }>({});
  const [problems, setProblems] = useState<Problem[]>([]);
  const [notices, setNotices] = useState<Notice[]>([]);
  const [error, setError] = useState<string>();
  const [run, setRun] = useState<ExploreRun>();
  const [running, setRunning] = useState(false);
  const controller = useRef<AbortController | undefined>(undefined);
  const resultBox = useRef<HTMLDivElement>(null);
  const help = useId();
  const time = exploreTime(search);
  // Async continuations read the navigation as it is when they finish.
  const searchNow = useRef(search); searchNow.current = search;
  const windowKey = `${search.range}|${search.from ?? ""}|${search.to ?? ""}`;
  // Responses compare against what is in the editors now, not when they were sent.
  const current = useRef({ text, sql }); current.current = { text, sql };
  const schema = useQuery({ queryKey: ["telemetry-schema"], queryFn: ({ signal }) => getTelemetrySchema(signal), staleTime: 60_000, retry: false, enabled: search.tab !== "query" });
  useEffect(() => () => { controller.current?.abort(); formatter.current.abort?.abort(); clearTimeout(formatter.current.timer); }, []);

  /** `direct` runs a builder query the text cannot carry (format said editable: false). */
  const execute = async (kind: Kind, source: string, viz?: Viz, direct?: Query) => {
    if (!source.trim()) return;
    controller.current?.abort();
    const abort = new AbortController(); controller.current = abort;
    setRunning(true); setError(undefined); setProblems([]);
    try {
      let panel: Panel;
      let canonical: string | undefined;
      if (kind === "query" && direct) {
        setParsed(undefined);
        panel = compose({ id: explorePanelID, title: defaultTitle(source), viz: viz ?? "table", query: direct });
        canonical = source;
        onSearch({ ...searchNow.current, q: source, viz: panel.viz }, true);
      } else if (kind === "query") {
        // Parse without the previous viz: a new query shape may not fit it.
        const next = await parseFanql({ text: source, variables }, abort.signal);
        if (abort.signal.aborted) return;
        setParsed({ ...next, text: source }); setNotices(next.notices);
        // An accepted viz edit changes the query; the Builder follows it.
        if (searchNow.current.tab === "builder") setBuilt(next.query);
        const chosen = viz && next.viz.compatible.some(c => c.viz === viz && satisfied(c)) ? viz : next.viz.compatible.find(satisfied)?.viz ?? "table";
        panel = compose({ id: explorePanelID, title: defaultTitle(next.canonical), viz: chosen, query: next.query });
        canonical = next.canonical;
        onSearch({ ...searchNow.current, q: source, viz: chosen, ...(source !== searchNow.current.q ? { drill: undefined } : {}) }, true);
      } else {
        // SQL has no viz analysis; an edited panel keeps its own viz.
        panel = compose({ id: explorePanelID, title: "SQL", viz: base?.viz ?? "table", sql: source });
        setNotices([]);
        onSearch({ ...searchNow.current, sql: source }, true);
      }
      const dashboard: DashboardSpec = { version: 1, name: context?.spec.name ?? "Explore", time, variables, panels: [panel] };
      const width = resultBox.current?.clientWidth || 960;
      const results = await queryPanels({ dashboard, panels: [explorePanelID], time, vars, widths: { [explorePanelID]: width } }, abort.signal);
      if (abort.signal.aborted) return;
      const result = results.find(r => r.id === explorePanelID);
      if (!result) throw new Error("The query returned no result.");
      setRun({ kind, source, window: windowKey, panel, result: classifyPanelResult(result, [explorePanelID]), canonical });
      // A SQL error that a retry cannot fix is a problem with the statement, shown at the editor.
      if (kind === "sql" && result.status === "error" && !result.retryable && current.current.sql === source) setProblems([{ path: "sql", message: result.error ?? "Query failed" }]);
    } catch (failure) {
      if (abort.signal.aborted) return;
      if (failure instanceof ApiError && failure.problems.length > 0) {
        // Spans index the text that was sent; drop them once it has been edited.
        if (current.current[kind === "query" ? "text" : "sql"] === source) setProblems(failure.problems);
      }
      else setError(failure instanceof Error ? failure.message : "Request failed");
    } finally {
      if (controller.current === abort) setRunning(false);
    }
  };

  // A shared link runs on open; a new time window reruns what is in the editor.
  const latest = useRef({ kind: kindOf(search.tab), viz: run?.panel.viz ?? search.viz, direct: undefined as Query | undefined });
  latest.current = { kind: kindOf(search.tab), viz: run?.panel.viz ?? search.viz, direct: formatted?.editable === false ? built : undefined };
  const rerun = useRef<() => void>(() => undefined);
  useEffect(() => { rerun.current(); }, [windowKey]);

  // Builder edits print through the server so the text stays the one query.
  const cancelFormat = () => { clearTimeout(formatter.current.timer); formatter.current.timer = undefined; formatter.current.abort?.abort(); };
  const format = async (query: Query): Promise<Formatted | undefined> => {
    cancelFormat();
    const abort = new AbortController(); formatter.current.abort = abort;
    try {
      const next = await formatFanql({ query }, abort.signal);
      if (abort.signal.aborted) return undefined;
      setFormatted(next); setFormatProblems([]); setNotices(next.notices); setProblems([]);
      setText(next.text);
      return next;
    } catch (failure) {
      if (abort.signal.aborted) return undefined;
      setFormatProblems(failure instanceof ApiError && failure.problems.length > 0 ? failure.problems : [{ path: "query", message: failure instanceof Error ? failure.message : "The query could not be printed" }]);
      return undefined;
    }
  };
  const edit = (query: Query) => {
    setBuilt(query);
    clearTimeout(formatter.current.timer);
    formatter.current.timer = setTimeout(() => { formatter.current.timer = undefined; void format(query); }, 250);
  };
  /** Every Builder run prints the current builder query first, so no path runs text a pending edit has replaced. */
  const runBuilder = async (viz?: Viz) => {
    if (!built) return;
    const next = await format(built);
    // The user may have left the Builder while the query was printing.
    if (next && searchNow.current.tab === "builder") void execute("query", next.text, viz ?? run?.panel.viz ?? search.viz, next.editable ? undefined : built);
  };
  const openTab = async (tab: ExploreSearch["tab"]) => {
    if (tab !== "builder") {
      // Leaving the Builder prints any pending edit before the text is shown.
      if (search.tab === "builder" && built && formatter.current.timer) void format(built);
      onSearch({ ...search, tab }); return;
    }
    // Text the server would not print as editable is never reparsed (section 0.11).
    if (formatted?.editable === false && built) { onSearch({ ...search, tab }); return; }
    if (parsed && parsed.text === text) { setBuilt(parsed.query); onSearch({ ...search, tab }); return; }
    if (!text.trim()) { setBuilt(starter); onSearch({ ...search, tab }); void format(starter); return; }
    try {
      const next = await parseFanql({ text, variables });
      setParsed({ ...next, text }); setBuilt(next.query); onSearch({ ...search, tab });
    } catch (failure) {
      if (failure instanceof ApiError && failure.problems.length > 0) setProblems(failure.problems);
      else setError(failure instanceof Error ? failure.message : "Request failed");
    }
  };

  rerun.current = () => {
    const { kind, viz, direct } = latest.current;
    if (searchNow.current.tab === "builder" && built) void runBuilder(viz);
    else void execute(kind, current.current[kind === "query" ? "text" : "sql"], viz, direct);
  };

  // A link that opens on the Builder seeds it like a tab switch would.
  useEffect(() => { if (search.tab === "builder") void openTab("builder"); }, []);

  const kind = kindOf(search.tab);
  const source = kind === "query" ? text : sql;
  const readOnlyText = formatted?.editable === false;
  const stale = run !== undefined && (run.kind !== kind || run.source !== source || run.window !== windowKey);
  const compatible = parsed?.viz.compatible ?? [];
  const editing = context?.panel !== undefined;
  const canSave = !stale && !running && canKeep(run, editing);

  return <Stack gap="md" p="lg" maw={1400} data-explore>
    <Group justify={context ? "flex-end" : "space-between"} align="flex-end">
      {!context && <Stack gap={2}>
        <Title order={2}>Explore</Title>
        <Text size="sm" c="dimmed">Ask a question of your telemetry. Runs the same query path as dashboards.</Text>
      </Stack>}
      <RangeMenu time={time} onRange={range => onSearch({ ...search, range, from: undefined, to: undefined })} onAbsolute={(from, to) => onSearch({ ...search, from, to })} />
    </Group>
    <Tabs value={search.tab} onChange={value => void openTab(value === "sql" || value === "builder" ? value : "query")} keepMounted={false}>
      <Tabs.List>
        <Tabs.Tab value="query">Query</Tabs.Tab>
        <Tabs.Tab value="builder">Builder</Tabs.Tab>
        <Tabs.Tab value="sql">SQL</Tabs.Tab>
      </Tabs.List>
      <Tabs.Panel value="query" pt="sm">
        <Stack gap="xs">
          <Text id={help} size="xs" c="dimmed">Press Enter to run. For example <code>spans | p95(duration) by service every 5m</code>.</Text>
          {readOnlyText && <Text size="xs" c="dimmed" data-fanql-readonly>This query is edited in the Builder: {formatted?.reason ?? "it is too large for the text editor"}.</Text>}
          <FanqlEditor value={text} readOnly={readOnlyText} onChange={value => { cancelFormat(); setText(value); setProblems([]); setFormatted(undefined); }} onRun={() => void execute("query", text, search.viz, readOnlyText ? built : undefined)} problems={problems} describedBy={help}
            complete={(value, cursor, signal) => completeFanql({ text: value, cursor, ...(time.from && time.to ? { from: time.from, to: time.to } : { window: time.range }) }, signal)} />
          {problems.length > 0 && <Stack gap={2} role="alert" data-fanql-problems>{problems.map((problem, index) => <Text key={index} size="sm" c="bad">{problem.message}{problem.hint ? ` (${problem.hint})` : ""}</Text>)}</Stack>}
          {notices.length > 0 && <Stack gap={2} data-fanql-notices>{notices.map((notice, index) => <Text key={index} size="xs" c="dimmed">{notice.message}</Text>)}</Stack>}
        </Stack>
      </Tabs.Panel>
      <Tabs.Panel value="builder" pt="sm">
        <Stack gap="xs">
          {schema.error ? <Alert color="warn" title="Fields could not be loaded">{schema.error.message} <Button size="compact-xs" variant="default" ml="xs" onClick={() => void schema.refetch()}>Retry</Button></Alert>
            : built && schema.data ? <Builder query={built} schema={schema.data} variables={(variables ?? []).map(v => v.name)} onChange={edit} /> : <Text size="sm" c="dimmed">Loading fields…</Text>}
          {formatProblems.length > 0 && <Stack gap={2} role="alert" data-builder-problems>{formatProblems.map((problem, index) => <Text key={index} size="sm" c="bad">{problem.message}{problem.hint ? ` (${problem.hint})` : ""}</Text>)}</Stack>}
          {formatted && formatProblems.length === 0 && <Text size="xs" c="dimmed" data-builder-text>As FanQL: <code>{formatted.text}</code></Text>}
        </Stack>
      </Tabs.Panel>
      <Tabs.Panel value="sql" pt="sm">
        <SqlEditor value={sql} onChange={value => { setSql(value); setProblems([]); }} onRun={() => void execute("sql", sql)} onEscape={() => runButton.current?.focus()} schema={schema.data} problems={search.tab === "sql" ? problems : []} label={context ? "Panel SQL" : "Explore SQL"} />
        {search.tab === "sql" && problems.length > 0 && <Alert color="bad" mt="xs" title="The SQL has problems"><ul>{problems.map((problem, index) => <li className="sql-problem" key={index}>{problem.message}{problem.hint ? ` (${problem.hint})` : ""}</li>)}</ul></Alert>}
      </Tabs.Panel>
    </Tabs>
    <Group justify="space-between">
      <Group gap="xs">
        <Button ref={runButton} loading={running} disabled={search.tab === "builder" ? !built || formatProblems.length > 0 : !source.trim()}
          onClick={() => void (search.tab === "builder" ? runBuilder(search.viz) : execute(kind, source, search.viz, readOnlyText ? built : undefined))}>Run</Button>
        {kind === "query" && compatible.length > 0 && <SegmentedControl size="xs" aria-label="Visualization" value={run?.panel.viz ?? search.viz ?? ""}
          onChange={value => void (search.tab === "builder" ? runBuilder(value as Viz) : execute("query", text, value as Viz, readOnlyText ? built : undefined))}
          data={compatible.map(c => ({ value: c.viz, disabled: !satisfied(c), label: !satisfied(c)
            ? <Tooltip label={`Needs ${c.requires.map(r => requirementLabel[r]).join(" and ")}; set them after saving`}><span>{vizLabel[c.viz]}</span></Tooltip>
            : vizLabel[c.viz] }))} />}
        {kind === "query" && !stale && !readOnlyText && parsed?.viz.edits.map(edit => <Tooltip key={edit.viz} label={<>Rewrites the query as <code>{edit.text}</code></>} multiline maw={420}>
          <Button size="compact-sm" variant="subtle" onClick={() => { setText(edit.text); void execute("query", edit.text, edit.viz); }}>{vizLabel[edit.viz]}…</Button>
        </Tooltip>)}
      </Group>
      <Group gap="xs">
      {context?.cancel && <Button variant="default" disabled={savingPanel} onClick={context.cancel}>Cancel</Button>}
      <Tooltip label={editing ? "Run the query first" : "Run a query that returns rows first"} disabled={canSave}><span>
        {context
          ? <Button disabled={!canSave} loading={savingPanel} onClick={() => {
            if (!run) return;
            setSavingPanel(true); setSavingError(undefined);
            context.save.run(run.panel).catch((failure: unknown) => setSavingError(failure instanceof Error ? failure.message : "Save failed")).finally(() => setSavingPanel(false));
          }}>{context.save.label}</Button>
          : <Button variant="default" disabled={!canSave} onClick={() => run && setSaving(run.panel)}>Save to dashboard</Button>}
      </span></Tooltip>
      </Group>
    </Group>
    {error && <Alert color="bad" title="The query could not run">{error}</Alert>}
    {savingError && <Alert color="bad" title="Not saved">{savingError}</Alert>}
    <Box ref={resultBox}>
      {run ? <Stack gap="xs">
        <Text size="sm" fw={600} role="status">{stale ? "Result of the last run · query has changed" : run.canonical ? <>Ran as <code>{run.canonical}</code></> : "Result"}</Text>
        <Box h={resultHeight}>
          <PanelCard panel={run.panel} title={run.panel.title} result={run.result} loading={running} height={resultHeight} group="explore" editing={false} agentAvailable={false}
            range={time.range} onRetry={() => void execute(run.kind, run.source, run.panel.viz, readOnlyText ? built : undefined)} traceLinks="button"
            onPoint={selection => { const selected = drillSelection(run.panel, run.result, selection, vars); if (selected) onSearch({ ...search, drill: JSON.stringify(selected.target) }); }} />
        </Box>
      </Stack> : <Text c="dimmed" size="sm" data-explore-empty>Type a query, for example <code>spans | count() by service</code>, and press Enter.</Text>}
    </Box>
    {run && <DrillDrawer client={exploreDrillClient} spec={{ version: 1, name: context?.spec.name ?? "Explore", time, variables, panels: [run.panel] }} time={time} vars={vars} target={parseDrill(search.drill)}
      onChange={target => onSearch({ ...search, drill: target ? JSON.stringify(target) : undefined })} />}
    {saving && onSaved && <SaveDialog panel={saving} time={time} onClose={() => setSaving(undefined)} onSaved={onSaved} />}
  </Stack>;
}
