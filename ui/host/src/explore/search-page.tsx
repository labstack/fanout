import { Alert, Box, Button, Group, SegmentedControl, Stack, Text, Title, Tooltip } from "@mantine/core";
import { useEffect, useId, useRef, useState } from "react";
import type { DashboardSpec, Panel } from "../../../panels/types";
import { ApiError, type Problem } from "../api-error";
import { queryPanels } from "../dashboards/api";
import { PanelCard } from "../dashboards/panel-card";
import { classifyPanelResult, type PanelDisplayResult } from "../dashboards/panel-result";
import { RangeMenu } from "../dashboards/toolbar";
import { FanqlEditor } from "../fanql-editor/fanql-editor";
import { completeFanql, parseFanql, type Notice } from "./api";
import { SaveDialog, defaultTitle } from "./save-dialog";
import { exploreTime, type ExploreSearch } from "./search";

/** Logs and Traces are Explore with the source and viz fixed by the page (proposal 0004, section 2). */
export type Preset = {
  name: string; source: "logs" | "spans"; viz: "logs" | "traces";
  sorts: readonly { value: string; label: string }[];
  example: string;
};

export const logsPreset: Preset = { name: "Logs", source: "logs", viz: "logs", sorts: [{ value: "time", label: "Newest" }, { value: "+time", label: "Oldest" }], example: "service:checkout severity>=warn" };
export const tracesPreset: Preset = { name: "Traces", source: "spans", viz: "traces", sorts: [{ value: "duration_ms", label: "Slowest" }, { value: "errors", label: "Errors first" }], example: "status:error duration>500ms" };

const panelID = "search";
const resultHeight = 560;

type Ran = { text: string; sort: string; window: string; panel: Panel; result: PanelDisplayResult };

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
  const searchNow = useRef(search); searchNow.current = search;
  useEffect(() => () => controller.current?.abort(), []);

  // Filters may be empty: an empty search lists the newest logs or slowest traces.
  const execute = async (filters: string, order: string) => {
    controller.current?.abort();
    const abort = new AbortController(); controller.current = abort;
    setRunning(true); setError(undefined); setProblems([]);
    try {
      const parsed = await parseFanql({ text: filters, source: preset.source, viz: preset.viz }, abort.signal);
      if (abort.signal.aborted) return;
      setNotices(parsed.notices);
      const panel: Panel = { id: panelID, title: defaultTitle(`${preset.name}: ${filters.trim() || "all"}`), viz: preset.viz, query: { ...parsed.query, sort: order } };
      onSearch({ ...searchNow.current, q: filters, sort: order }, true);
      const dashboard: DashboardSpec = { version: 1, name: preset.name, time, panels: [panel] };
      const width = resultBox.current?.clientWidth || 960;
      const results = await queryPanels({ dashboard, panels: [panelID], time, widths: { [panelID]: width } }, abort.signal);
      if (abort.signal.aborted) return;
      const result = results.find(r => r.id === panelID);
      if (!result) throw new Error("The search returned no result.");
      setRan({ text: filters, sort: order, window: windowKey, panel, result: classifyPanelResult(result, [panelID]) });
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
  useEffect(() => { rerun.current(); }, [windowKey, sort]);

  const stale = ran !== undefined && (ran.text !== text || ran.sort !== sort || ran.window !== windowKey);
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
            range={time.range} onRetry={() => void execute(ran.text, ran.sort)} />
        </Box>
      </Stack>}
    </Box>
    {saving && <SaveDialog panel={saving} time={time} onClose={() => setSaving(undefined)} onSaved={onSaved} />}
  </Stack>;
}
