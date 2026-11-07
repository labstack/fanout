import { LiveDataMeta } from "../live-data-meta";
import { PageHeader, Notice } from "@labstack/ui";
import { Anchor, Box, Button, Group, NumberInput, SegmentedControl, Select, Stack, Switch, Text, Tooltip } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useCallback, useEffect, useId, useMemo, useRef, useState } from "react";
import { rowModel } from "../../../panels/rows";
import type { DashboardSpec, Panel, PanelResult, Selection } from "../../../panels/types";
import { ApiError, type Problem } from "../api-error";
import { getTelemetrySchema, queryPanels } from "../dashboards/api";
import { DrillDrawer } from "../dashboards/drill";
import { exploreDrillClient } from "./drill";
import { parseDrill } from "../dashboards/drill-state";
import { drillSelection } from "../dashboards/panel-handlers";
import { PanelCard } from "../dashboards/panel-card";
import { classifyPanelResult, type PanelDisplayResult } from "../dashboards/panel-result";
import { RangeMenu } from "../dashboards/toolbar";
import type { RowControls } from "../dashboards/viz/table";
import { LogDetails } from "./log-details";
import { FanqlEditor } from "../fanql-editor/fanql-editor";
import { completeFanql, formatFanql, parseFanql, type Notice as QueryNotice } from "./api";
import { SaveDialog, defaultTitle } from "./save-dialog";
import { exploreTime, maxSlower, type ExploreSearch, type Picks } from "./search";
import type { TextSpan } from "../api-error";

/** Logs and Traces are Explore with the source and viz fixed by the page (proposal 0004, section 2). */
export type Preset = {
  name: string; source: "logs" | "spans"; viz: "logs" | "traces";
  /** The filter fields this page offers beside the typed filters. */
  fields: readonly ("service" | "severity" | "errors" | "slower")[];
  sorts: readonly { value: string; label: string }[];
  example: string;
  /** How bare words and field values match, for the help line. */
  matching: string;
};

export const logsPreset: Preset = { name: "Logs", source: "logs", viz: "logs", fields: ["service", "severity"], sorts: [{ value: "time", label: "Newest" }, { value: "+time", label: "Oldest" }], example: "service:checkout severity>=warn",
  matching: "Bare words match anywhere in the body; body:x matches the whole body and body~x any part of it." };
export const tracesPreset: Preset = { name: "Traces", source: "spans", viz: "traces", fields: ["service", "errors", "slower"], sorts: [{ value: "duration_ms", label: "Slowest" }, { value: "+start", label: "Oldest" }], example: "status:error duration>500ms",
  matching: "Bare words match anywhere in the operation; operation:x matches the whole operation and operation~x any part of it." };

const panelID = "search";
const severityOptions = [{ value: "debug", label: "Debug and above" }, { value: "info", label: "Info and above" }, { value: "warn", label: "Warn and above" }, { value: "error", label: "Error and above" }, { value: "fatal", label: "Fatal" }];

/** FanQL terms for the filter fields; the server parses them like typed filters. */
function pickTerms(preset: Preset, picks: Picks): string {
  const terms: string[] = [];
  // FanQL doubles a quote inside a quoted value.
  if (preset.fields.includes("service") && picks.service) terms.push(`service:"${picks.service.replaceAll('"', '""')}"`);
  if (preset.fields.includes("severity") && picks.severity) terms.push(`severity>=${picks.severity}`);
  if (preset.fields.includes("errors") && picks.errors) terms.push("status:error");
  if (preset.fields.includes("slower") && picks.slower) terms.push(`duration>${picks.slower}ms`);
  return terms.join(" ");
}
const resultHeight = 560;
/** Rows rendered before "Show more": the search returns up to 1,000. */
const rowPage = 100;
/** The server's busy message tells a dashboard to refresh; this page runs one search. */
const searchCopy = (result: PanelResult): PanelResult => result.reason === "busy"
  ? { ...result, error: "Not run: other queries kept the server busy. Run the search again." } : result;
/** The typed filters are grouped so a top-level `or` stays theirs; the fields' terms follow. */
const combine = (filters: string, picked: string) => filters.trim() && picked ? `(${filters}) ${picked}` : filters.trim() ? filters : picked;
/** FanQL quotes a value with doubled inner quotes. */
const quoted = (value: string) => `"${value.replaceAll('"', '""')}"`;
/** A stored filter that reads the span status column, ignoring quoted text such as attribute keys. */
const readsStatus = (sql: string) => /\bstatus\b/i.test(sql.replace(/'(?:[^']|'')*'|"(?:[^"]|"")*"/g, "''"));

/**
 * The searched spans grouped by service, as a FanQL query. Status terms would
 * make every rate 0 or 100%, and an excluded term drops whole traces here but
 * only single spans there, so both are left out.
 */
async function servicesQuery(query: Panel["query"]): Promise<string> {
  const where = query?.where ?? [];
  let filters = "";
  if (where.length > 0) {
    const formatted = await formatFanql({ query: { from: "spans", where }, viz: "traces" });
    filters = formatted.where.filter((entry, i) => !entry.startsWith("-") && !readsStatus(where[i] ?? ""))
      .map(entry => /\bor\b/i.test(entry) ? `(${entry})` : entry).join(" ");
  }
  return `spans${filters ? ` where ${filters}` : ""} | count(), error_rate(), p95(duration) by service sort error_rate`;
}

type Ran = { text: string; terms: string; sort: string; window: string; panel: Panel; result: PanelDisplayResult };

export function SearchPage({ preset, search, onSearch, onSaved, onExplore }: {
  preset: Preset; search: ExploreSearch; onSearch(next: ExploreSearch, replace?: boolean): void; onSaved(dashboardId: string): void;
  /** Opens a whole FanQL query in Explore. */
  onExplore?(q: string): void;
}) {
  const [text, setText] = useState(search.q);
  const [problems, setProblems] = useState<Problem[]>([]);
  const [shifted, setShifted] = useState({ shift: 0, length: 0 });
  const [notices, setNotices] = useState<QueryNotice[]>([]);
  const [error, setError] = useState<string>();
  const [ran, setRan] = useState<Ran>();
  const [running, setRunning] = useState(false);
  const [saving, setSaving] = useState<Panel>();
  const controller = useRef<AbortController | undefined>(undefined);
  const resultBox = useRef<HTMLDivElement>(null);
  const help = useId();
  const [ranAt, setRanAt] = useState<number>();
  const time = exploreTime(search);
  const windowKey = `${search.range}|${search.from ?? ""}|${search.to ?? ""}`;
  const sort = search.sort && preset.sorts.some(s => s.value === search.sort) ? search.sort : preset.sorts[0].value;
  const current = useRef(text); current.current = text;
  const terms = pickTerms(preset, search.picks ?? {});
  const termsNow = useRef(terms); termsNow.current = terms;
  const services = useQuery({ queryKey: ["telemetry-schema"], queryFn: ({ signal }) => getTelemetrySchema(signal), staleTime: 60_000, retry: false, enabled: preset.fields.includes("service") });
  const pick = (patch: Partial<Picks>) => onSearch({ ...search, picks: { ...search.picks, ...patch } });
  // The filters this page last wrote to the URL; any other value came from history.
  const written = useRef(search.q);
  const searchNow = useRef(search); searchNow.current = search;
  const onSearchNow = useRef(onSearch); onSearchNow.current = onSearch;
  const sortNow = useRef(sort); sortNow.current = sort;
  useEffect(() => () => controller.current?.abort(), []);

  // Filters may be empty: an empty search lists the newest logs or slowest traces.
  const execute = async (filters: string, order: string) => {
    controller.current?.abort();
    const abort = new AbortController(); controller.current = abort;
    setRunning(true); setError(undefined); setProblems([]); setNotices([]);
    try {
      // Problem spans in the combined text are mapped back to the editor.
      const picked = termsNow.current;
      const combined = combine(filters, picked);
      const shift = filters.trim() && picked ? 1 : 0;
      setShifted({ shift, length: Array.from(filters).length });
      const parsed = await parseFanql({ text: combined, source: preset.source, viz: preset.viz, window: time.from && time.to ? undefined : time.range }, abort.signal);
      if (abort.signal.aborted) return;
      // Notice spans index the text that was sent, as problem spans do.
      if (current.current === filters) setNotices(parsed.notices);
      const panel: Panel = { id: panelID, title: defaultTitle(`${preset.name}: ${[filters.trim(), picked].filter(Boolean).join(" ") || "all"}`), viz: preset.viz, query: { ...parsed.query, sort: order } };
      // A drawer opened from earlier results closes when the filters change.
      const changed = filters !== written.current;
      written.current = filters;
      // A changed search is a new history entry; reruns of the same one replace it.
      onSearch({ ...searchNow.current, q: filters, sort: order, ...(changed ? { drill: undefined } : {}) }, !changed);
      const dashboard: DashboardSpec = { version: 1, name: preset.name, time, panels: [panel] };
      const width = resultBox.current?.clientWidth || 960;
      const results = await queryPanels({ dashboard, panels: [panelID], time, widths: { [panelID]: width } }, abort.signal);
      if (abort.signal.aborted) return;
      const result = results.find(r => r.id === panelID);
      if (!result) throw new Error("The search returned no result.");
      if (result.status === "invalid") {
        if (current.current === filters) setProblems(result.problems ?? []);
        return;
      }
      setRanAt(Date.now());
      setRan({ text: filters, terms: picked, sort: order, window: windowKey, panel, result: classifyPanelResult(searchCopy(result), [panelID]) });
    } catch (failure) {
      if (abort.signal.aborted) return;
      if (failure instanceof ApiError && failure.problems.length > 0) { if (current.current === filters) setProblems(failure.problems); }
      else setError(failure instanceof Error ? failure.message : "Request failed");
    } finally {
      if (controller.current === abort) setRunning(false);
    }
  };

  const executeNow = useRef(execute); executeNow.current = execute;
  // Opens with results, and reruns when the window or the order changes.
  const rerun = useRef<() => void>(() => undefined);
  rerun.current = () => void execute(current.current, sort);
  useEffect(() => { rerun.current(); }, [windowKey, sort, terms]);
  // Back and Forward restore earlier filters; show and run them.
  useEffect(() => {
    if (search.q === written.current) return;
    written.current = search.q; setText(search.q); void execute(search.q, sort);
  }, [search.q]);

  // Problems and notices from the combined text, in the editor's coordinates; generated terms are not the author's to fix.
  const local = useMemo(() => {
    const inText = (span?: TextSpan) => span && span.start >= shifted.shift && span.end - shifted.shift <= shifted.length ? { start: span.start - shifted.shift, end: span.end - shifted.shift } : undefined;
    const map = <T extends Problem | QueryNotice>(item: T): T => ({ ...item, span: inText(item.span), fix_its: item.fix_its?.flatMap(fix => { const span = inText(fix.span); return span ? [{ ...fix, span }] : []; }) });
    return { problems: problems.map(map), notices: notices.map(map) };
  }, [problems, notices, shifted]);
  // Stable row callbacks keep the table's cells mounted while the URL changes, so
  // the drawer can return focus to the trace button that opened it.
  const ranNow = useRef(ran); ranNow.current = ran;
  const typed = useRef(text); typed.current = text;
  const openRow = useCallback((selection: Selection) => {
    const last = ranNow.current; if (!last) return;
    const selected = drillSelection(last.panel, last.result, selection, {});
    if (selected) onSearchNow.current({ ...searchNow.current, drill: JSON.stringify(selected.target) });
  }, []);
  const controls = useMemo<RowControls>(() => ({
    page: rowPage,
    details: preset.viz === "logs" ? row => <LogDetails row={row} onTrace={() => { const last = ranNow.current; if (last) openRow(rowModel(last.panel, last.result).selection(row)); }} /> : undefined,
    exclude: preset.viz === "traces" ? (field, value) => {
      // A top-level `or` keeps its meaning only inside parentheses.
      const typedText = typed.current.trim();
      const next = [/\bor\b/i.test(typedText) ? `(${typedText})` : typedText, `-${field}:${quoted(value)}`].filter(Boolean).join(" ");
      setText(next); void executeNow.current(next, sortNow.current);
    } : undefined,
  }), [preset.viz, openRow]);
  const stale = ran !== undefined && (ran.text !== text || ran.terms !== terms || ran.sort !== sort || ran.window !== windowKey);
  const canSave = ran !== undefined && !stale && !running && ran.result.status === "ok" && (ran.result.frame?.rows ?? 0) > 0;

  return <Stack gap="md" px={{base:"md",sm:"xl"}} pt="lg" pb="xl" maw={1600} mx="auto" data-search-page={preset.viz}>
    <Group justify="space-between" align="flex-end">
      <PageHeader title={preset.name} description={preset.name === "Logs" ? "Search and inspect log events." : "Find slow requests and inspect their traces."} meta={<LiveDataMeta time={time} updatedAt={ran ? ranAt : null} />} />
      <RangeMenu time={time} onRange={range => onSearch({ ...search, range, from: undefined, to: undefined })} onAbsolute={(from, to) => onSearch({ ...search, from, to })} />
    </Group>
    <Stack gap="xs">
      <Text id={help} size="xs" c="dimmed">Filter with FanQL, for example <code>{preset.example}</code>. {preset.matching} Press Enter to search.</Text>
      <FanqlEditor value={text} label={`Search ${preset.name.toLowerCase()}`} describedBy={help} problems={local.problems} notices={local.notices}
        onChange={value => { setText(value); setProblems([]); setNotices([]); }} onRun={() => void execute(text, sort)}
        complete={(value, cursor, signal) => completeFanql({ text: value, cursor, source: preset.source, viz: preset.viz, ...(time.from && time.to ? { from: time.from, to: time.to } : { window: time.range }) }, signal)} />
    </Stack>
    <Group gap="sm" align="flex-end" data-search-fields>
      {preset.fields.includes("service") && <Select label="Service" placeholder={services.isPending ? "Loading services…" : "All services"} w={220} clearable searchable value={search.picks?.service ?? null}
        error={services.isError && <>Services did not load. <Anchor component="button" type="button" fz="xs" onClick={() => void services.refetch()}>Retry</Anchor></>}
        data={[...new Set([...(search.picks?.service ? [search.picks.service] : []), ...(services.data?.services ?? []).map(s => s.value)])]} onChange={service => pick({ service: service ?? undefined })} />}
      {preset.fields.includes("severity") && <Select label="Severity" placeholder="Any severity" w={180} clearable value={search.picks?.severity ?? null} data={severityOptions} onChange={severity => pick({ severity: severity ?? undefined })} />}
      {preset.fields.includes("slower") && <SlowerThan value={search.picks?.slower} onCommit={slower => pick({ slower })} />}
      {preset.fields.includes("errors") && <Switch label="Errors only" mb={8} checked={search.picks?.errors === "1"} onChange={event => pick({ errors: event.currentTarget.checked ? "1" : undefined })} />}
    </Group>
    <Group justify="space-between">
      <Group gap="xs">
        <Button loading={running} onClick={() => void execute(text, sort)}>Search</Button>
        <SegmentedControl size="xs" aria-label="Order" value={sort} data={[...preset.sorts]} onChange={value => onSearch({ ...search, sort: value })} />
        {preset.viz === "traces" && onExplore && <Tooltip label="Error rate of the matching spans in each service, in Explore. Status and excluded terms are left out.">
          <Button variant="subtle" size="xs" disabled={!ran} onClick={() => {
            // The last search's spans, one row per service, in Explore where the query stays visible.
            if (ran) servicesQuery(ran.panel.query).then(onExplore, (failure: unknown) => setError(failure instanceof Error ? failure.message : "Request failed"));
          }}>Span error rates by service</Button>
        </Tooltip>}
      </Group>
      <Tooltip label="Search for something that returns rows first" disabled={canSave}><span><Button variant="default" disabled={!canSave} onClick={() => ran && setSaving(ran.panel)}>Save to dashboard</Button></span></Tooltip>
    </Group>
    {error && <Notice tone="danger" title="The search could not run">{error}</Notice>}
    <Box ref={resultBox}>
      {ran && <Stack gap="xs">
        {stale && <Text size="sm" fw={600} role="status">Result of the last search · filters have changed</Text>}
        <Box h={resultHeight}>
          <PanelCard panel={ran.panel} title={ran.panel.title} result={ran.result} loading={running} height={resultHeight} group={`search-${preset.viz}`} editing={false} agentAvailable={false}
            range={time.range} onRetry={() => void execute(ran.text, ran.sort)} traceLinks="button" controls={controls} onPoint={openRow} />
        </Box>
      </Stack>}
    </Box>
    {ran && <DrillDrawer client={exploreDrillClient} spec={{ version: 1, name: preset.name, time, panels: [{ ...ran.panel, title: "this search" }] }} time={time} vars={{}} target={parseDrill(search.drill)}
      onChange={target => onSearch({ ...search, drill: target ? JSON.stringify(target) : undefined })} />}
    {saving && <SaveDialog panel={saving} time={time} onClose={() => setSaving(undefined)} onSaved={onSaved} />}
  </Stack>;
}

/** Commits on Enter or blur, so typing 500 searches once, not for 5, 50 and 500. */
function SlowerThan({ value, onCommit }: { value?: string; onCommit(value?: string): void }) {
  const [draft, setDraft] = useState<string | number>(value ? Number(value) : "");
  useEffect(() => { setDraft(value ? Number(value) : ""); }, [value]);
  const commit = () => {
    const next = typeof draft === "number" && draft >= 1 ? String(Math.min(maxSlower, Math.floor(draft))) : undefined;
    if (next !== value) onCommit(next);
  };
  return <NumberInput label="Slower than (ms)" w={160} min={1} max={maxSlower} clampBehavior="strict" value={draft} onChange={setDraft} onBlur={commit} onKeyDown={event => { if (event.key === "Enter") commit(); }} />;
}
