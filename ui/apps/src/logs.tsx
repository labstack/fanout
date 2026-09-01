import { BarChart } from "echarts/charts";
import { ActionIcon, Badge, Group, Paper, SegmentedControl, Table, Text, TextInput, Tooltip } from "@mantine/core";
import { ArrowSquareOut, ListMagnifyingGlass, MagnifyingGlass } from "@phosphor-icons/react";
import { StrictMode, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import { EmptyState, MetaFooter, PageControls, ViewHeader, ViewShell, ViewStatus, chartTheme, statusHex, usePagedItems } from "./components";
import type { LogEntry, Logs, Result } from "./contracts";
import { EChart, useECharts } from "./echart";
import { timelineTimestamp, windowLabel } from "./format";
import { askAbout, useFanoutApp } from "./use-fanout-app";
import "./app.css";

useECharts([BarChart]);

function LogsApp() {
  const { app, callTool, error, host, result, toolError } = useFanoutApp<Result<Logs>>("Fanout log explorer");
  const [severity, setSeverity] = useState("ALL");
  const [search, setSearch] = useState("");
  const dark = host?.theme === "dark";
  const entries = useMemo(() => (result?.data.entries ?? []).filter((entry) => (severity === "ALL" || entry.severity.toUpperCase() === severity) && (!search || entry.body.toLowerCase().includes(search.toLowerCase()) || entry.service.toLowerCase().includes(search.toLowerCase()))), [result, search, severity]);
  return <ViewShell dark={dark}>
    <ViewHeader eyebrow="Application activity" title="Logs" summary={result ? `${result.data.entries.length} entries in this time range` : undefined} onRefresh={() => callTool("search_logs")} disabled={!app} />
    <ViewStatus error={toolError ?? (error ? "This view could not be loaded. Please try again." : null)} loading={!result && !error && !toolError ? "Searching logs…" : undefined} />
    {result && result.data.entries.length === 0 && <><EmptyState tall icon={<ListMagnifyingGlass size={20} weight="duotone" />} title="No logs matched">Try a wider time window, a different service, or a less restrictive search.</EmptyState><MetaFooter left={windowLabel(result.provenance.window)} right="No entries found" /></>}
    {result && result.data.entries.length > 0 && <>
      <LogHistogram data={result.data} dark={dark} window={result.provenance.window} />
      <Group px={{ base: "md", sm: "lg" }} py="sm" justify="space-between" align="center">
        <SegmentedControl size="xs" value={severity} onChange={setSeverity} data={["ALL", "ERROR", "WARN", "INFO"]} />
        <TextInput aria-label="Filter visible logs" type="search" value={search} onChange={(event) => setSearch(event.currentTarget.value)} placeholder="Filter visible logs…" leftSection={<MagnifyingGlass size={15} />} w={{ base: "100%", xs: 250 }} />
      </Group>
      <LogList entries={entries} window={result.provenance.window} onTrace={(entry) => askAbout(app, `Investigate trace ${entry.trace_id} related to this ${entry.severity} log from ${entry.service}.`)} />
      <MetaFooter left={windowLabel(result.provenance.window)} right={`${entries.length} matching`} />
    </>}
  </ViewShell>;
}

function LogHistogram({ data, dark, window }: { data: Logs; dark: boolean; window: string }) {
  const option = useMemo(() => {
    const colors = chartTheme(dark);
    const times = [...new Set(data.buckets.map((bucket) => bucket.time))];
    const severities = [...new Set(data.buckets.map((bucket) => bucket.severity))];
    const values = new Map(data.buckets.map((bucket) => [`${bucket.time}\u0000${bucket.severity}`, bucket.count]));
    return { color: severities.map((severity) => severityHex(severity, dark)), grid: { left: 42, right: 18, top: 30, bottom: 28 }, tooltip: { trigger: "axis", axisPointer: { type: "shadow" }, backgroundColor: colors.surface, borderColor: colors.border, textStyle: { color: colors.text, fontSize: 10 } }, legend: { top: 0, right: 0, textStyle: { color: colors.muted, fontSize: 9 }, itemWidth: 7, itemHeight: 7, icon: "circle" }, xAxis: { type: "category", data: times.map((time) => timelineTimestamp(time, window)), axisLabel: { color: colors.muted, fontSize: 8, hideOverlap: true }, axisLine: { lineStyle: { color: colors.border } } }, yAxis: { type: "value", minInterval: 1, splitLine: { lineStyle: { color: colors.grid } }, axisLabel: { color: colors.muted, fontSize: 8 } }, series: severities.map((severity) => ({ name: severity, type: "bar", stack: "logs", barMaxWidth: 22, data: times.map((time) => values.get(`${time}\u0000${severity}`) ?? 0), itemStyle: { borderRadius: [2, 2, 0, 0] } })) };
  }, [dark, data.buckets, window]);
  return <Paper withBorder radius="md" mx={{ base: "md", sm: "lg" }} p="xs"><EChart option={option} height={190} label="Log volume by severity over time" /></Paper>;
}

function LogList({ entries, onTrace, window }: { entries: LogEntry[]; onTrace: (entry: LogEntry) => void; window: string }) {
  const logs = usePagedItems(entries, 6);
  if (entries.length === 0) return <EmptyState icon={<MagnifyingGlass size={20} weight="duotone" />} title="No visible matches">Adjust the local severity or text filter.</EmptyState>;
  return <><Table.ScrollContainer minWidth={680}><Table striped highlightOnHover verticalSpacing="xs">
    <Table.Thead><Table.Tr><Table.Th>Time</Table.Th><Table.Th>Level</Table.Th><Table.Th>Service</Table.Th><Table.Th>Message</Table.Th><Table.Th /></Table.Tr></Table.Thead>
    <Table.Tbody>{logs.pageItems.map((entry, index) => <Table.Tr key={`${entry.time}-${logs.from + index}`}><Table.Td><Text size="xs" ff="monospace">{timelineTimestamp(entry.time, window, true)}</Text></Table.Td><Table.Td><Badge size="sm" color={severityColor(entry.severity)} variant="light">{entry.severity || "LOG"}</Badge></Table.Td><Table.Td><Text size="sm" fw={600}>{entry.service}</Text></Table.Td><Table.Td><Text size="sm" lineClamp={2} title={entry.body}>{entry.body}</Text></Table.Td><Table.Td>{entry.trace_id && <Tooltip label="Investigate trace"><ActionIcon variant="subtle" aria-label={`Investigate trace ${entry.trace_id}`} onClick={() => onTrace(entry)}><ArrowSquareOut size={15} weight="bold" /></ActionIcon></Tooltip>}</Table.Td></Table.Tr>)}</Table.Tbody>
  </Table></Table.ScrollContainer><PageControls {...logs} onChange={logs.setPage} /></>;
}

function severityColor(value: string) { const severity = value.toUpperCase(); if (severity === "ERROR" || severity === "FATAL") return "bad"; if (severity === "WARN" || severity === "WARNING") return "warn"; if (severity === "INFO") return "info"; return "gray"; }
function severityHex(value: string, dark: boolean) { const status = statusHex(dark); const severity = value.toUpperCase(); if (severity === "ERROR" || severity === "FATAL") return status.bad; if (severity === "WARN" || severity === "WARNING") return status.warn; if (severity === "INFO") return status.info; return chartTheme(dark).muted; }

createRoot(document.getElementById("root")!).render(<StrictMode><LogsApp /></StrictMode>);
