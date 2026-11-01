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
import { Builder, querySchema } from "./builder";
import { SaveDialog, defaultTitle } from "./save-dialog";
import { exploreTime, parseExploreSearch, toExploreParams, type ExploreSearch } from "./search";

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

/** The drill dashboards give a source: exemplar traces for spans, matching logs for logs. Metrics have none. */
function drillFor(query: Query, viz: Viz): Panel["drill"] {
  if (query.from === "logs") return "logs";
  if (query.from === "spans" && viz !== "health" && viz !== "service_map") return "traces";
  return undefined;
}

/** Stored filters and measures keyed to their FanQL text, for entries the Builder shows as chips. */
function labelsOf(query: Query, formatted: Formatted): Record<string, string> {
  const pairs = (stored: string[] | undefined, printed: string[]) => (stored ?? []).flatMap((entry, i) => i < printed.length ? [[entry, printed[i]] as const] : []);
  return Object.fromEntries([...pairs(query.where, formatted.where), ...pairs(query.measures, formatted.measures)]);
}

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
  // Stored filters and measures as FanQL, from every print so far, for the Builder's chips.
  const [labels, setLabels] = useState<Record<string, string>>(() => context?.initial ? labelsOf(context.initial.query, context.initial.formatted) : {});
  // A Builder filter value that cannot be used holds the run.
  const [builderInvalid, setBuilderInvalid] = useState(false);
  const formatter = useRef<{ abort?: AbortController; timer?: ReturnType<typeof setTimeout> }>({});
  const [problems, setProblems] = useState<Problem[]>([]);
  const [notices, setNotices] = useState<Notice[]>([]);
  const [error, setError] = useState<string>();
  // Enter on an empty editor runs nothing; it says why, once per Enter, while the editor stays empty.
  const [emptyRun, setEmptyRun] = useState(0);
  const [run, setRun] = useState<ExploreRun>();
  // A run the server refused leaves the last result shown; this marks what was refused.
  const [refused, setRefused] = useState<{ kind: Kind; source: string; window: string }>();
  const [running, setRunning] = useState(false);
  const controller = useRef<AbortController | undefined>(undefined);
  const resultBox = useRef<HTMLDivElement>(null);
  const help = useId();
  const time = exploreTime(search);
  // Async continuations read the navigation as it is when they finish.
  const searchNow = useRef(search); searchNow.current = search;
  // The entry the last run wrote, so Back and Forward can tell another entry apart.
  const shown = useRef({ q: search.q, sql: search.sql ?? "", viz: search.viz });
  /** A run that changes the query adds a history entry; running it again replaces the entry. */
  const record = (next: ExploreSearch) => {
    const now = searchNow.current;
    shown.current = { q: next.q, sql: next.sql ?? "", viz: next.viz };
    // A viz chosen for an entry that named none refines that entry.
    onSearch(next, next.q === now.q && next.sql === now.sql && (now.viz === undefined || next.viz === now.viz));
  };
  const windowOf = (s: ExploreSearch) => `${s.range}|${s.from ?? ""}|${s.to ?? ""}`;
  const windowKey = windowOf(search);
  // The window the range menu asked for: reaching it reruns the editor; reaching any other is navigation.
  const asked = useRef<string | undefined>(undefined);
  const pickWindow = (next: ExploreSearch) => {
    // Compared as the URL will hold it: an absolute window drops the range.
    const key = windowOf(parseExploreSearch(toExploreParams(next)));
    if (key !== windowKey) asked.current = key;
    onSearch(next);
  };
  // The window last shown; unset until the page has opened.
  const seen = useRef<string | undefined>(undefined);
  // Responses compare against what is in the editors now, not when they were sent.
  const current = useRef({ text, sql }); current.current = { text, sql };
  const schema = useQuery({ queryKey: ["telemetry-schema"], queryFn: ({ signal }) => getTelemetrySchema(signal), staleTime: 60_000, retry: false, enabled: search.tab !== "query" });
  // A remount, as StrictMode does, opens the page again.
  useEffect(() => () => { controller.current?.abort(); formatter.current.abort?.abort(); clearTimeout(formatter.current.timer); seen.current = undefined; }, []);

  /** `direct` runs a builder query the text cannot carry (format said editable: false). */
  const execute = async (kind: Kind, source: string, viz?: Viz, direct?: Query) => {
    if (!source.trim()) return;
    controller.current?.abort();
    const abort = new AbortController(); controller.current = abort;
    setRunning(true); setError(undefined); setProblems([]); setNotices([]); setRefused(undefined);
    try {
      let panel: Panel;
      let canonical: string | undefined;
      if (kind === "query" && direct) {
        setParsed(undefined);
        panel = compose({ id: explorePanelID, title: defaultTitle(source), viz: viz ?? "table", query: direct, drill: drillFor(direct, viz ?? "table") });
        canonical = source;
        record({ ...searchNow.current, q: source, viz: panel.viz });
      } else if (kind === "query") {
        // Parse without the previous viz: a new query shape may not fit it.
        const next = await parseFanql({ text: source, variables }, abort.signal);
        if (abort.signal.aborted) return;
        setParsed({ ...next, text: source });
        // Notice spans index the text that was sent, as problem spans do.
        if (current.current.text === source) setNotices(next.notices);
        // An accepted viz edit changes the query; the Builder follows it.
        if (searchNow.current.tab === "builder") setBuilt(next.query);
        const chosen = viz && next.viz.compatible.some(c => c.viz === viz && satisfied(c)) ? viz : next.viz.compatible.find(satisfied)?.viz ?? "table";
        panel = compose({ id: explorePanelID, title: defaultTitle(next.canonical), viz: chosen, query: next.query, drill: drillFor(next.query, chosen) });
        canonical = next.canonical;
        record({ ...searchNow.current, q: source, viz: chosen, ...(source !== searchNow.current.q ? { drill: undefined } : {}) });
      } else {
        // SQL has no viz analysis; an edited panel keeps its own viz.
        panel = compose({ id: explorePanelID, title: "SQL", viz: base?.viz ?? "table", sql: source });
        record({ ...searchNow.current, sql: source });
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
      setRefused({ kind, source, window: windowKey });
      if (failure instanceof ApiError && failure.problems.length > 0) {
        // Spans index the text that was sent; drop them once it has been edited.
        if (current.current[kind === "query" ? "text" : "sql"] === source) setProblems(failure.problems);
      }
      else setError(failure instanceof Error ? failure.message : "Request failed");
    } finally {
      if (controller.current === abort) setRunning(false);
    }
  };

  // A shared link runs on open; a window chosen in the range menu reruns what is in the editor.
  const latest = useRef({ kind: kindOf(search.tab), viz: run?.panel.viz ?? search.viz, direct: undefined as Query | undefined });
  latest.current = { kind: kindOf(search.tab), viz: run?.panel.viz ?? search.viz, direct: formatted?.editable === false ? built : undefined };
  const rerun = useRef<() => void>(() => undefined);

  // Builder edits print through the server so the text stays the one query.
  const cancelFormat = () => { clearTimeout(formatter.current.timer); formatter.current.timer = undefined; formatter.current.abort?.abort(); };
  /** `adopt` makes the print the query text; opening the Builder only shows it, so the user's text is not replaced. */
  const format = async (query: Query, adopt = true): Promise<Formatted | undefined> => {
    cancelFormat();
    const abort = new AbortController(); formatter.current.abort = abort;
    try {
      const next = await formatFanql({ query }, abort.signal);
      if (abort.signal.aborted) return undefined;
      setFormatted(next); setFormatProblems([]); setLabels(known => ({ ...known, ...labelsOf(query, next) }));
      if (adopt) { setNotices(next.notices); setProblems([]); setText(next.text); }
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
    // A link that opens on the Builder is already there; showing it again would add a history entry.
    const show = () => { if (searchNow.current.tab !== tab) onSearch({ ...searchNow.current, tab }); };
    // Text the server would not print as editable is never reparsed (section 0.11).
    if (formatted?.editable === false && built) { show(); return; }
    if (parsed && parsed.text === text) { setBuilt(parsed.query); show(); void format(parsed.query, false); return; }
    // An empty editor starts the Builder from an example; the text stays empty until the user edits or runs it.
    if (!text.trim()) { setBuilt(starter); show(); void format(starter, false); return; }
    try {
      const next = await parseFanql({ text, variables });
      setParsed({ ...next, text }); setBuilt(next.query); show(); void format(next.query, false);
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

  // Back and Forward bring another entry, which may differ only in its window: show and run what it holds, never the draft.
  useEffect(() => {
    const entry = { q: search.q, sql: search.sql ?? "", viz: search.viz };
    const same = entry.q === shown.current.q && entry.sql === shown.current.sql && entry.viz === shown.current.viz;
    const opened = seen.current === undefined, moved = seen.current !== windowKey, chosen = asked.current === windowKey;
    seen.current = windowKey; asked.current = undefined;
    if (same && (opened || (moved && chosen))) { rerun.current(); return; }
    if (same && !moved) return;
    shown.current = entry;
    cancelFormat(); setFormatted(undefined); setProblems([]);
    setText(entry.q); setSql(entry.sql);
    const kind = kindOf(search.tab);
    void execute(kind, kind === "query" ? entry.q : entry.sql, entry.viz);
  }, [search.q, search.sql, search.viz, windowKey]);

  // A link that opens on the Builder seeds it like a tab switch would.
  useEffect(() => { if (search.tab === "builder") void openTab("builder"); }, []);

  const kind = kindOf(search.tab);
  const source = kind === "query" ? text : sql;
  const emptyShown = emptyRun > 0 && !text.trim();
  const readOnlyText = formatted?.editable === false;
  const stale = run !== undefined && (run.kind !== kind || run.source !== source || run.window !== windowKey);
  const compatible = parsed?.viz.compatible ?? [];
  const editing = context?.panel !== undefined;
  // An edited panel whose query and viz are as it opened saves its other settings, such as the title, without a run.
  const unchanged = base !== undefined && !dirty && (search.viz ?? base.viz) === base.viz;
  const ran = !stale && !running && canKeep(run, editing);
  const canSave = ran || unchanged;
  const failed = refused !== undefined ? refused.kind === kind && refused.source === source && refused.window === windowKey : !stale && run?.result.status === "error";
  const saveReason = running ? "Wait for the run to finish" : failed ? "The last run failed; saving needs a run that works" : stale ? "Run the query as it is now first" : editing ? "Run the query first" : "Run a query that returns rows first";
  const saveHelp = useId();
  // Points open what makes makeDrill return a target; otherwise the chart offers no drill.
  const drills = run !== undefined && (run.panel.drill !== undefined || run.panel.viz === "service_map" || (run.result.frame?.columns.some(c => c.name === "trace_id") ?? false));

  return <Stack gap="md" p="lg" maw={1400} data-explore>
    <Group justify={context ? "flex-end" : "space-between"} align="flex-end">
      {!context && <Stack gap={2}>
        <Title order={2}>Explore</Title>
        <Text size="sm" c="dimmed">Ask a question of your telemetry. Runs the same query path as dashboards.</Text>
      </Stack>}
      <RangeMenu time={time} onRange={range => pickWindow({ ...search, range, from: undefined, to: undefined })} onAbsolute={(from, to) => pickWindow({ ...search, from, to })} />
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
          <div>
            <FanqlEditor value={text} readOnly={readOnlyText} placeholder="spans | count() by service" onChange={(value, kept) => { cancelFormat(); setText(value); setProblems(kept?.problems ?? []); setNotices(kept?.notices ?? []); setFormatted(undefined); }} onRun={() => { if (!text.trim()) setEmptyRun(count => count + 1); void execute("query", text, search.viz, readOnlyText ? built : undefined); }} problems={problems} notices={notices} describedBy={help}
              complete={(value, cursor, signal) => completeFanql({ text: value, cursor, ...(time.from && time.to ? { from: time.from, to: time.to } : { window: time.range }) }, signal)} />
            {/* The live region stays mounted so additions to it are announced; each Enter adds a new node. It sits with the editor so it takes no gap while empty. */}
            <Text size="xs" c="dimmed" mt={emptyShown ? 4 : 0} aria-live="polite" data-fanql-empty>{emptyShown && <span key={emptyRun}>Type a query to run, for example <code>spans | count() by service</code>.</span>}</Text>
          </div>
        </Stack>
      </Tabs.Panel>
      <Tabs.Panel value="builder" pt="sm">
        <Stack gap="xs">
          {!built && (problems.length > 0 || error) ? <Alert color="bad" title="This query cannot open in the Builder" data-builder-blocked>
            {problems.length > 0 ? <Stack gap={2} mb="xs">{problems.map((problem, index) => <Text key={index} size="sm">{problem.message}{problem.hint ? ` (${problem.hint})` : ""}</Text>)}</Stack> : <Text size="sm" mb="xs">{error}</Text>}
            <Button size="compact-sm" variant="default" onClick={() => void openTab("query")}>Fix it in the Query tab</Button>
          </Alert> : <>
            {schema.error && <Alert color="warn" title="Fields could not be loaded">
              {built ? "Only the fields this query uses are offered until the field list loads. " : ""}{schema.error.message}
              <Button size="compact-xs" variant="default" ml="xs" loading={schema.isFetching} onClick={() => void schema.refetch()}>Retry</Button>
            </Alert>}
            {built && (schema.data ?? schema.error) ? <Builder query={built} schema={schema.data ?? querySchema(built)} variables={(variables ?? []).map(v => v.name)} labels={labels} onChange={edit} onInvalid={setBuilderInvalid} />
              : !schema.error && <Text size="sm" c="dimmed">Loading fields…</Text>}
          </>}
          {formatProblems.length > 0 && <Stack gap={2} role="alert" data-builder-problems>{formatProblems.map((problem, index) => <Text key={index} size="sm" c="bad">{problem.message}{problem.hint ? ` (${problem.hint})` : ""}</Text>)}</Stack>}
          {formatted && formatProblems.length === 0 && <Text size="xs" c="dimmed" data-builder-text>As FanQL: <code>{formatted.text}</code></Text>}
        </Stack>
      </Tabs.Panel>
      <Tabs.Panel value="sql" pt="sm">
        {!sql.trim() && <Text size="xs" c="dimmed" mb="xs" data-sql-hint>{text.trim() ? "SQL runs on its own; the FanQL query is not converted. " : ""}Write one SELECT over spans, logs or metrics that filters time with <code>$__window(time_column)</code>, for example <code>SELECT service, count(*) AS spans FROM spans WHERE $__window(start_time) GROUP BY service</code>.</Text>}
        <SqlEditor value={sql} onChange={value => { setSql(value); setProblems([]); }} onRun={() => void execute("sql", sql)} onEscape={() => runButton.current?.focus()} schema={schema.data} problems={search.tab === "sql" ? problems : []} label={context ? "Panel SQL" : "Explore SQL"} />
        {search.tab === "sql" && problems.length > 0 && <Alert color="bad" mt="xs" title="The SQL has problems"><ul>{problems.map((problem, index) => <li className="sql-problem" key={index}>{problem.message}{problem.hint ? ` (${problem.hint})` : ""}</li>)}</ul></Alert>}
      </Tabs.Panel>
    </Tabs>
    <Group justify="space-between">
      <Group gap="xs">
        <Button ref={runButton} loading={running} disabled={search.tab === "builder" ? !built || formatProblems.length > 0 || builderInvalid : !source.trim()}
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
      {/* aria-disabled keeps the button focusable, so its reason is reachable by keyboard and read with it. */}
      <Tooltip label={saveReason} disabled={canSave} events={{ hover: true, focus: true, touch: true }}>
        {context
          ? <Button data-disabled={!canSave || undefined} aria-disabled={!canSave} aria-describedby={canSave ? undefined : saveHelp} loading={savingPanel} onClick={() => {
            const panel = ran ? run?.panel : base;
            if (!panel || !canSave) return;
            setSavingPanel(true); setSavingError(undefined);
            context.save.run(panel).catch((failure: unknown) => setSavingError(failure instanceof Error ? failure.message : "Save failed")).finally(() => setSavingPanel(false));
          }}>{context.save.label}</Button>
          : <Button variant="default" data-disabled={!canSave || undefined} aria-disabled={!canSave} aria-describedby={canSave ? undefined : saveHelp} onClick={() => run && canSave && setSaving(run.panel)}>Save to dashboard</Button>}
      </Tooltip>
      {!canSave && <span id={saveHelp} hidden>{saveReason}</span>}
      </Group>
    </Group>
    {error && <Alert color="bad" title="The query could not run">{error}</Alert>}
    {savingError && <Alert color="bad" title="Not saved">{savingError}</Alert>}
    <Box ref={resultBox}>
      {run ? <Stack gap="xs">
        <Text size="sm" fw={600} role="status">{running ? "Running the query…" : run.kind !== kind ? `Result of the last ${run.kind === "sql" ? "SQL" : "FanQL"} run` : run.source !== source ? "Result of the last run · query has changed" : run.window !== windowKey ? "Result of the last run · time range has changed" : run.canonical ? <>Ran as <code>{run.canonical}</code></> : "Result"}</Text>
        <Box h={resultHeight}>
          <PanelCard panel={run.panel} title={run.panel.title} result={run.result} loading={running} height={resultHeight} group="explore" editing={false} agentAvailable={false}
            range={time.range} onRetry={() => void execute(run.kind, run.source, run.panel.viz, readOnlyText ? built : undefined)} traceLinks="button"
            onPoint={drills ? selection => { const selected = drillSelection(run.panel, run.result, selection, vars); if (selected) onSearch({ ...search, drill: JSON.stringify(selected.target) }); } : undefined} />
        </Box>
      </Stack> : running ? <Text c="dimmed" size="sm" role="status" data-explore-running>Running the query…</Text> : <Text c="dimmed" size="sm" data-explore-empty>{search.tab === "builder" ? "Build a query above and press Run." : search.tab === "sql" ? "Write a query above and press Run." : "Type a query above and press Enter."}</Text>}
    </Box>
    {run && <DrillDrawer client={exploreDrillClient} spec={{ version: 1, name: context?.spec.name ?? "Explore", time, variables, panels: [run.panel] }} time={time} vars={vars} target={parseDrill(search.drill)}
      onChange={target => onSearch({ ...search, drill: target ? JSON.stringify(target) : undefined })} />}
    {saving && onSaved && <SaveDialog panel={saving} time={time} onClose={() => setSaving(undefined)} onSaved={onSaved} />}
  </Stack>;
}
