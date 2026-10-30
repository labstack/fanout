import { Alert, Box, Button, Group, SegmentedControl, Stack, Tabs, Text, Title, Tooltip } from "@mantine/core";
import { useQuery } from "@tanstack/react-query";
import { useEffect, useId, useRef, useState } from "react";
import type { DashboardSpec, Panel, Viz } from "../../../panels/types";
import { ApiError, type Problem } from "../api-error";
import { getTelemetrySchema, queryPanels } from "../dashboards/api";
import { PanelCard } from "../dashboards/panel-card";
import { classifyPanelResult, type PanelDisplayResult } from "../dashboards/panel-result";
import { RangeMenu } from "../dashboards/toolbar";
import { FanqlEditor } from "../fanql-editor/fanql-editor";
import { SqlEditor } from "../sql-editor/sql-editor";
import { completeFanql, parseFanql, type Notice, type Parsed } from "./api";
import { exploreTime, type ExploreSearch } from "./search";

export const explorePanelID = "explore";
const resultHeight = 420;

const vizLabel: Record<Viz, string> = {
  stat: "Stat", gauge: "Gauge", timeseries: "Time series", bar: "Bar", table: "Table", text: "Text", heatmap: "Heatmap", histogram: "Histogram",
  scatter: "Scatter", state_timeline: "State timeline", logs: "Logs", log_patterns: "Log patterns", traces: "Traces", service_map: "Service map", health: "Health",
};
const requirementLabel = { min_max: "a minimum and maximum", thresholds: "thresholds" } as const;

/** What the last run sent, so later edits can be shown as stale and not saved. */
export type ExploreRun = { tab: ExploreSearch["tab"]; source: string; panel: Panel; result: PanelDisplayResult; canonical?: string };

export function hasRows(run?: ExploreRun): boolean {
  return run?.result.status === "ok" && (run.result.frame?.rows ?? 0) > 0;
}

export function ExplorePage({ search, onSearch, onSave }: {
  search: ExploreSearch; onSearch(next: ExploreSearch, replace?: boolean): void;
  /** Called with the panel of the last successful run; omitted hides Save. */
  onSave?(run: ExploreRun): void;
}) {
  const [text, setText] = useState(search.q);
  const [sql, setSql] = useState(search.sql ?? "");
  const [parsed, setParsed] = useState<Parsed>();
  const [problems, setProblems] = useState<Problem[]>([]);
  const [notices, setNotices] = useState<Notice[]>([]);
  const [error, setError] = useState<string>();
  const [run, setRun] = useState<ExploreRun>();
  const [running, setRunning] = useState(false);
  const controller = useRef<AbortController | undefined>(undefined);
  const resultBox = useRef<HTMLDivElement>(null);
  const help = useId();
  const time = exploreTime(search);
  const schema = useQuery({ queryKey: ["telemetry-schema"], queryFn: ({ signal }) => getTelemetrySchema(signal), staleTime: 60_000, retry: false, enabled: search.tab === "sql" });
  useEffect(() => () => controller.current?.abort(), []);

  const execute = async (tab: ExploreSearch["tab"], source: string, viz?: Viz) => {
    if (!source.trim()) return;
    controller.current?.abort();
    const abort = new AbortController(); controller.current = abort;
    setRunning(true); setError(undefined); setProblems([]);
    try {
      let panel: Panel;
      let canonical: string | undefined;
      if (tab === "query") {
        const next = await parseFanql({ text: source, viz }, abort.signal);
        if (abort.signal.aborted) return;
        setParsed(next); setNotices(next.notices);
        const chosen = viz && next.viz.compatible.some(c => c.viz === viz && c.requires.length === 0) ? viz : next.viz.compatible.find(c => c.requires.length === 0)?.viz ?? "table";
        panel = { id: explorePanelID, title: next.canonical, viz: chosen, query: next.query };
        canonical = next.canonical;
        onSearch({ ...search, tab, q: source, viz: chosen }, true);
      } else {
        panel = { id: explorePanelID, title: "SQL", viz: "table", sql: source };
        setNotices([]);
        onSearch({ ...search, tab, sql: source }, true);
      }
      const dashboard: DashboardSpec = { version: 1, name: "Explore", time, panels: [panel] };
      const width = resultBox.current?.clientWidth || 960;
      const results = await queryPanels({ dashboard, panels: [explorePanelID], time, widths: { [explorePanelID]: width } }, abort.signal);
      if (abort.signal.aborted) return;
      const result = results.find(r => r.id === explorePanelID);
      if (!result) throw new Error("The query returned no result.");
      setRun({ tab, source, panel, result: classifyPanelResult(result, [explorePanelID]), canonical });
    } catch (failure) {
      if (abort.signal.aborted) return;
      if (failure instanceof ApiError && failure.problems.length > 0) setProblems(failure.problems);
      else setError(failure instanceof Error ? failure.message : "Request failed");
    } finally {
      if (controller.current === abort) setRunning(false);
    }
  };

  // A shared link runs on open; a new time window reruns what is on screen.
  const timeKey = `${search.range}|${search.from ?? ""}|${search.to ?? ""}`;
  const lastRun = useRef(run); lastRun.current = run;
  useEffect(() => {
    const previous = lastRun.current;
    if (previous) void execute(previous.tab, previous.source, previous.panel.viz);
    else if (search.tab === "query" ? search.q.trim() : search.sql?.trim()) void execute(search.tab, search.tab === "query" ? search.q : search.sql ?? "", search.viz);
  }, [timeKey]);

  const source = search.tab === "query" ? text : sql;
  const stale = run !== undefined && (run.tab !== search.tab || run.source !== source);
  const compatible = parsed?.viz.compatible ?? [];
  const canSave = onSave !== undefined && !stale && !running && hasRows(run);

  return <Stack gap="md" p="lg" maw={1400} data-explore>
    <Group justify="space-between" align="flex-end">
      <Stack gap={2}>
        <Title order={2}>Explore</Title>
        <Text size="sm" c="dimmed">Ask a question of your telemetry. Runs the same query path as dashboards.</Text>
      </Stack>
      <RangeMenu time={time} onRange={range => onSearch({ ...search, range, from: undefined, to: undefined })} onAbsolute={(from, to) => onSearch({ ...search, from, to })} />
    </Group>
    <Tabs value={search.tab} onChange={value => onSearch({ ...search, tab: value === "sql" ? "sql" : "query" })} keepMounted={false}>
      <Tabs.List>
        <Tabs.Tab value="query">Query</Tabs.Tab>
        <Tabs.Tab value="sql">SQL</Tabs.Tab>
      </Tabs.List>
      <Tabs.Panel value="query" pt="sm">
        <Stack gap="xs">
          <Text id={help} size="xs" c="dimmed">Press Enter to run. For example <code>spans | p95(duration) by service every 5m</code>.</Text>
          <FanqlEditor value={text} onChange={value => { setText(value); setProblems([]); }} onRun={() => void execute("query", text, search.viz)} problems={problems} describedBy={help}
            complete={(value, cursor, signal) => completeFanql({ text: value, cursor, ...(time.from && time.to ? { from: time.from, to: time.to } : { window: time.range }) }, signal)} />
          {problems.length > 0 && <Stack gap={2} role="alert" data-fanql-problems>{problems.map((problem, index) => <Text key={index} size="sm" c="bad">{problem.message}{problem.hint ? ` (${problem.hint})` : ""}</Text>)}</Stack>}
          {notices.length > 0 && <Stack gap={2} data-fanql-notices>{notices.map((notice, index) => <Text key={index} size="xs" c="dimmed">{notice.message}</Text>)}</Stack>}
        </Stack>
      </Tabs.Panel>
      <Tabs.Panel value="sql" pt="sm">
        <SqlEditor value={sql} onChange={value => { setSql(value); setProblems([]); }} onRun={() => void execute("sql", sql)} onEscape={() => undefined} schema={schema.data} problems={search.tab === "sql" ? problems : []} label="Explore SQL" />
      </Tabs.Panel>
    </Tabs>
    <Group justify="space-between">
      <Group gap="xs">
        <Button loading={running} disabled={!source.trim()} onClick={() => void execute(search.tab, source, search.viz)}>Run</Button>
        {search.tab === "query" && compatible.length > 0 && <SegmentedControl size="xs" aria-label="Visualization" value={run?.panel.viz ?? search.viz ?? ""}
          onChange={value => void execute("query", text, value as Viz)}
          data={compatible.map(c => ({ value: c.viz, disabled: c.requires.length > 0, label: c.requires.length > 0
            ? <Tooltip label={`Needs ${c.requires.map(r => requirementLabel[r]).join(" and ")}; set them after saving`}><span>{vizLabel[c.viz]}</span></Tooltip>
            : vizLabel[c.viz] }))} />}
        {search.tab === "query" && parsed?.viz.edits.map(edit => <Button key={edit.viz} size="compact-sm" variant="subtle" onClick={() => { setText(edit.text); void execute("query", edit.text, edit.viz); }}>
          {vizLabel[edit.viz]} (runs <code>{edit.text}</code>)
        </Button>)}
      </Group>
      {onSave && <Tooltip label="Run a query that returns rows first" disabled={canSave}><span><Button variant="default" disabled={!canSave} onClick={() => run && onSave(run)}>Save to dashboard</Button></span></Tooltip>}
    </Group>
    {error && <Alert color="bad" title="The query could not run">{error}</Alert>}
    <Box ref={resultBox}>
      {run ? <Stack gap="xs">
        <Text size="sm" fw={600} role="status">{stale ? "Result of the last run · query has changed" : run.canonical ? <>Ran as <code>{run.canonical}</code></> : "Result"}</Text>
        <Box h={resultHeight}>
          <PanelCard panel={run.panel} title={run.panel.title} result={run.result} loading={running} height={resultHeight} group="explore" editing={false} agentAvailable={false}
            range={time.range} onRetry={() => void execute(run.tab, run.source, run.panel.viz)} />
        </Box>
      </Stack> : <Text c="dimmed" size="sm" data-explore-empty>Type a query, for example <code>spans | count() by service</code>, and press Enter.</Text>}
    </Box>
  </Stack>;
}
