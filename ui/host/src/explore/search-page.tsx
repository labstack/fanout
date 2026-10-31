import { Alert, Box, Button, Group, NumberInput, SegmentedControl, Select, Stack, Switch, Text, Title, Tooltip } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState } from "react";
import type { DashboardSpec, Panel } from "../../../panels/types";
import { ApiError, type Problem } from "../api-error";
import { getTelemetrySchema, queryPanels } from "../dashboards/api";
import { DrillDrawer } from "../dashboards/drill";
import { exploreDrillClient } from "./drill";
import { parseDrill } from "../dashboards/drill-state";
import { drillSelection } from "../dashboards/panel-handlers";
import { PanelCard } from "../dashboards/panel-card";
import { classifyPanelResult, type PanelDisplayResult } from "../dashboards/panel-result";
import { RangeMenu } from "../dashboards/toolbar";
import { FanqlEditor } from "../fanql-editor/fanql-editor";
import { completeFanql, parseFanql, type Notice } from "./api";
import { SaveDialog, defaultTitle } from "./save-dialog";
import { exploreTime, type ExploreSearch, type Picks } from "./search";

/** Logs and Traces are Explore with the source and viz fixed by the page (proposal 0004, section 2). */
export type Preset = {
  name: string; source: "logs" | "spans"; viz: "logs" | "traces";
  /** The filter fields this page offers beside the typed filters. */
  fields: readonly ("service" | "severity" | "errors" | "slower")[];
  sorts: readonly { value: string; label: string }[];
  example: string;
};

export const logsPreset: Preset = { name: "Logs", source: "logs", viz: "logs", fields: ["service", "severity"], sorts: [{ value: "time", label: "Newest" }, { value: "+time", label: "Oldest" }], example: "service:checkout severity>=warn" };
export const tracesPreset: Preset = { name: "Traces", source: "spans", viz: "traces", fields: ["service", "errors", "slower"], sorts: [{ value: "duration_ms", label: "Slowest" }, { value: "errors", label: "Errors first" }], example: "status:error duration>500ms" };

const panelID = "search";
const severityOptions = [{ value: "debug", label: "Debug and above" }, { value: "info", label: "Info and above" }, { value: "warn", label: "Warn and above" }, { value: "error", label: "Error and above" }, { value: "fatal", label: "Fatal" }];

/** FanQL terms for the filter fields; the server parses them like typed filters. */
function pickTerms(preset: Preset, picks: Picks): string {
  const terms: string[] = [];
  if (preset.fields.includes("service") && picks.service) terms.push(`service:"${picks.service}"`);
  if (preset.fields.includes("severity") && picks.severity) terms.push(`severity>=${picks.severity}`);
  if (preset.fields.includes("errors") && picks.errors) terms.push("status:error");
  if (preset.fields.includes("slower") && picks.slower) terms.push(`duration>${picks.slower}ms`);
  return terms.join(" ");
}
const resultHeight = 560;

type Ran = { text: string; terms: string; sort: string; window: string; panel: Panel; result: PanelDisplayResult };

export function SearchPage({ preset, search, onSearch, onSaved }: {
  preset: Preset; search: ExploreSearch; onSearch(next: ExploreSearch, replace?: boolean): void; onSaved(dashboardId: string): void;
}) {
  const [text, setText] = useState(search.q);
  const [problems, setProblems] = useState<Problem[]>([]);
  const [notices, setNotices] = useState<Notice[]>([]);
  const [error, setError] = useState<string>();
  const [ran, setRan] = useState<Ran>();
  const [running, setRunning] = useState(false);
  const [saving, setSaving] = useState<Panel>();
  const controller = useRef<AbortController | undefined>(undefined);
  const resultBox = useRef<HTMLDivElement>(null);
  const help = useId();
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
  useEffect(() => () => controller.current?.abort(), []);

  // Filters may be empty: an empty search lists the newest logs or slowest traces.
  const execute = async (filters: string, order: string) => {
    controller.current?.abort();
    const abort = new AbortController(); controller.current = abort;
    setRunning(true); setError(undefined); setProblems([]);
    try {
      // Typed filters come first so problem spans keep indexing the editor's text.
      const picked = termsNow.current;
      const parsed = await parseFanql({ text: [filters.trim(), picked].filter(Boolean).join(" "), source: preset.source, viz: preset.viz }, abort.signal);
      if (abort.signal.aborted) return;
      setNotices(parsed.notices);
      const panel: Panel = { id: panelID, title: defaultTitle(`${preset.name}: ${[filters.trim(), picked].filter(Boolean).join(" ") || "all"}`), viz: preset.viz, query: { ...parsed.query, sort: order } };
      // A drawer opened from earlier results closes when the filters change.
      const changed = filters !== written.current;
      written.current = filters;
      onSearch({ ...searchNow.current, q: filters, sort: order, ...(changed ? { drill: undefined } : {}) }, true);
      const dashboard: DashboardSpec = { version: 1, name: preset.name, time, panels: [panel] };
      const width = resultBox.current?.clientWidth || 960;
      const results = await queryPanels({ dashboard, panels: [panelID], time, widths: { [panelID]: width } }, abort.signal);
      if (abort.signal.aborted) return;
      const result = results.find(r => r.id === panelID);
      if (!result) throw new Error("The search returned no result.");
      setRan({ text: filters, terms: picked, sort: order, window: windowKey, panel, result: classifyPanelResult(result, [panelID]) });
    } catch (failure) {
      if (abort.signal.aborted) return;
      if (failure instanceof ApiError && failure.problems.length > 0) { if (current.current === filters) setProblems(failure.problems); }
      else setError(failure instanceof Error ? failure.message : "Request failed");
    } finally {
      if (controller.current === abort) setRunning(false);
    }
  };

  // Opens with results, and reruns when the window or the order changes.
  const rerun = useRef<() => void>(() => undefined);
  rerun.current = () => void execute(current.current, sort);
  useEffect(() => { rerun.current(); }, [windowKey, sort, terms]);
  // Back and Forward restore earlier filters; show and run them.
  useEffect(() => {
    if (search.q === written.current) return;
    written.current = search.q; setText(search.q); void execute(search.q, sort);
  }, [search.q]);

  const stale = ran !== undefined && (ran.text !== text || ran.terms !== terms || ran.sort !== sort || ran.window !== windowKey);
  const canSave = ran !== undefined && !stale && !running && ran.result.status === "ok" && (ran.result.frame?.rows ?? 0) > 0;

  return <Stack gap="md" p="lg" maw={1400} data-search-page={preset.viz}>
    <Group justify="space-between" align="flex-end">
      <Title order={2}>{preset.name}</Title>
      <RangeMenu time={time} onRange={range => onSearch({ ...search, range, from: undefined, to: undefined })} onAbsolute={(from, to) => onSearch({ ...search, from, to })} />
    </Group>
    <Stack gap="xs">
      <Text id={help} size="xs" c="dimmed">Filter with FanQL, for example <code>{preset.example}</code>. Press Enter to search.</Text>
      <FanqlEditor value={text} label={`Search ${preset.name.toLowerCase()}`} describedBy={help} problems={problems}
        onChange={value => { setText(value); setProblems([]); }} onRun={() => void execute(text, sort)}
        complete={(value, cursor, signal) => completeFanql({ text: value, cursor, source: preset.source, viz: preset.viz, ...(time.from && time.to ? { from: time.from, to: time.to } : { window: time.range }) }, signal)} />
      {problems.length > 0 && <Stack gap={2} role="alert">{problems.map((problem, index) => <Text key={index} size="sm" c="bad">{problem.message}{problem.hint ? ` (${problem.hint})` : ""}</Text>)}</Stack>}
      {notices.length > 0 && <Stack gap={2}>{notices.map((notice, index) => <Text key={index} size="xs" c="dimmed">{notice.message}</Text>)}</Stack>}
    </Stack>
    <Group gap="sm" align="flex-end" data-search-fields>
      {preset.fields.includes("service") && <Select label="Service" placeholder="All services" w={220} clearable searchable value={search.picks?.service ?? null}
        data={[...new Set([...(search.picks?.service ? [search.picks.service] : []), ...(services.data?.services ?? []).map(s => s.value).filter(name => !name.includes('"'))])]} onChange={service => pick({ service: service ?? undefined })} />}
      {preset.fields.includes("severity") && <Select label="Severity" placeholder="Any severity" w={180} clearable value={search.picks?.severity ?? null} data={severityOptions} onChange={severity => pick({ severity: severity ?? undefined })} />}
      {preset.fields.includes("slower") && <NumberInput label="Slower than (ms)" w={160} min={1} value={search.picks?.slower ? Number(search.picks.slower) : ""}
        onChange={value => pick({ slower: typeof value === "number" && value >= 1 ? String(Math.floor(value)) : undefined })} />}
      {preset.fields.includes("errors") && <Switch label="Errors only" mb={8} checked={search.picks?.errors === "1"} onChange={event => pick({ errors: event.currentTarget.checked ? "1" : undefined })} />}
    </Group>
    <Group justify="space-between">
      <Group gap="xs">
        <Button loading={running} onClick={() => void execute(text, sort)}>Search</Button>
        <SegmentedControl size="xs" aria-label="Order" value={sort} data={[...preset.sorts]} onChange={value => onSearch({ ...search, sort: value })} />
      </Group>
      <Tooltip label="Search for something that returns rows first" disabled={canSave}><span><Button variant="default" disabled={!canSave} onClick={() => ran && setSaving(ran.panel)}>Save to dashboard</Button></span></Tooltip>
    </Group>
    {error && <Alert color="bad" title="The search could not run">{error}</Alert>}
    <Box ref={resultBox}>
      {ran && <Stack gap="xs">
        {stale && <Text size="sm" fw={600} role="status">Result of the last search · filters have changed</Text>}
        <Box h={resultHeight}>
          <PanelCard panel={ran.panel} title={ran.panel.title} result={ran.result} loading={running} height={resultHeight} group={`search-${preset.viz}`} editing={false} agentAvailable={false}
            range={time.range} onRetry={() => void execute(ran.text, ran.sort)} traceLinks="button"
            onPoint={selection => { const selected = drillSelection(ran.panel, ran.result, selection, {}); if (selected) onSearch({ ...search, drill: JSON.stringify(selected.target) }); }} />
        </Box>
      </Stack>}
    </Box>
    {ran && <DrillDrawer client={exploreDrillClient} spec={{ version: 1, name: preset.name, time, panels: [ran.panel] }} time={time} vars={{}} target={parseDrill(search.drill)}
      onChange={target => onSearch({ ...search, drill: target ? JSON.stringify(target) : undefined })} />}
    {saving && <SaveDialog panel={saving} time={time} onClose={() => setSaving(undefined)} onSaved={onSaved} />}
  </Stack>;
}
