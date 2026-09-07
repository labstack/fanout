import { HeatmapChart, LineChart } from "echarts/charts";
import { Badge, Paper, SimpleGrid, Stack, Table, Text } from "@mantine/core";
import { ArrowUpRight, ArrowsLeftRight, GridFour, Pulse } from "@phosphor-icons/react";
import { StrictMode, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import { EmptyState, MetaFooter, Metric, PageControls, Tabs, ViewHeader, ViewShell, ViewStatus, usePagedItems } from "./components";
import { chartTheme, healthColor, seriesColor, statusHex } from "../../chart";
import { typeScale } from "../../tokens";
import type { Endpoint, Performance, Result } from "../../contracts";
import { EChart, useECharts } from "./echart";
import { duration, integer, percent, timelineTimestamp, windowLabel } from "../../format";
import { askAbout, useFanoutApp } from "./use-fanout-app";
import "./app.css";

type View = "activity" | "latency" | "endpoints" | "compare";
useECharts([LineChart, HeatmapChart]);

function PerformanceApp() {
  const { app, callTool, error, host, result, toolError } = useFanoutApp<Result<Performance>>("Fanout service performance");
  const [view, setView] = useState<View>("activity");
  const dark = host?.theme === "dark";
  return <ViewShell dark={dark}>
    <ViewHeader title={result?.data.service || "System performance"} summary={result ? `Traffic, latency, and errors ${result.data.service ? `for ${result.data.service}` : "across all services"}` : undefined} onRefresh={() => callTool("service_performance")} disabled={!app} />
    <ViewStatus error={toolError ?? (error ? "This view could not be loaded. Please try again." : null)} loading={!result && !error && !toolError ? "Loading performance signals…" : undefined} retry={() => void callTool("service_performance")} />
    {result && <>
      <Tabs active={view} onChange={setView} items={[{ id: "activity", label: "Activity" }, { id: "latency", label: "Latency map" }, { id: "endpoints", label: "Endpoints", count: result.data.endpoints.length }, { id: "compare", label: "Compare" }]} />
      {view === "activity" && <ActivityView data={result.data} dark={dark} window={result.provenance.window} />}
      {view === "latency" && <HeatmapView data={result.data} dark={dark} window={result.provenance.window} />}
      {view === "endpoints" && <EndpointsView endpoints={result.data.endpoints} onEndpoint={(endpoint) => askAbout(app, `Investigate ${endpoint.method} ${endpoint.path}. Explain its latency and errors.`)} />}
      {view === "compare" && <ComparisonView data={result.data} />}
      <MetaFooter left={windowLabel(result.provenance.window)} right={`Updated ${new Date(result.provenance.generated_at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}`} />
    </>}
  </ViewShell>;
}

function ActivityView({ data, dark, window }: { data: Performance; dark: boolean; window: string }) {
  if (data.points.length === 0) return <EmptyState tall icon={<Pulse size={20} weight="duotone" />} title="No activity in this window">Trends will appear as activity is recorded.</EmptyState>;
  // The tiles read totals, not points.at(-1): the footer labels this card with
  // the whole window, and the newest bucket is both a fraction of it and still
  // filling, so a headline taken from it disagrees with the chart underneath it
  // and with the Compare tab beside it.
  const { totals } = data;
  const labels = data.points.map((point) => point.time);
  return <Stack px={{ base: "md", sm: "lg" }} pb="md">
    <SimpleGrid cols={{ base: 1, xs: 3 }} spacing="sm"><Metric label="Operations" value={integer.format(totals.spans)} /><Metric label="P95 latency" value={duration(totals.p95_ms)} color={totals.p95_ms >= 750 ? "warn" : "ok"} /><Metric label="Error rate" value={percent(totals.error_rate)} color={totals.error_rate >= .01 ? "bad" : "ok"} /></SimpleGrid>
    <PerformanceChart dark={dark} labels={labels} title="Traffic and logs" window={window} series={[{ name: "Operations", data: data.points.map((point) => point.spans), color: seriesColor("operations", dark) }, { name: "Logs", data: data.points.map((point) => point.log_count), color: seriesColor("logs", dark) }]} />
    <PerformanceChart dark={dark} labels={labels} title="Latency and error correlation" window={window} series={[{ name: "P95 latency", data: data.points.map((point) => point.p95_ms), color: statusHex(dark).warn, axis: "duration" }, { name: "Error rate", data: data.points.map((point) => point.error_rate), color: statusHex(dark).bad, axis: "percent" }]} />
  </Stack>;
}

type Axis = "count" | "duration" | "percent";

const axisFormat: Record<Axis, (value: number) => string> = {
  count: (value) => integer.format(value),
  duration,
  percent,
};

/* Tick labels are not table cells. `percent` says "<0.01%" for a rate too small
   to write out, which is right in a cell and useless on an axis — every
   gridline would carry the same label. Ticks state the number they sit on. */
const tickFormat: Record<Axis, (value: number) => string> = {
  count: (value) => integer.format(value),
  duration,
  percent: (value) => `${(value * 100).toFixed(value >= 0.1 ? 0 : 2)}%`,
};

/* Each series is drawn against an axis in its own units. A rate used to be
   multiplied by a thousand so it could share the latency axis, which put
   "Error rate × 1000" in the legend and left the reader converting in their
   head. Latency labels read in the units `duration` chooses, so one axis no
   longer carries both "0.0ms" and "100.00s". */
function PerformanceChart({ labels, title, series, dark, window }: { labels: string[]; title: string; series: Array<{ name: string; data: number[]; color: string; axis?: Axis }>; dark: boolean; window: string }) {
  const option = useMemo(() => {
    const colors = chartTheme(dark);
    const axes = [...new Set(series.map((item) => item.axis ?? "count"))] as Axis[];
    const unitOf = (name: string) => series.find((item) => item.name === name)?.axis ?? "count";
    return {
      color: series.map((item) => item.color),
      grid: { left: 46, right: axes.length > 1 ? 54 : 18, top: 42, bottom: 30 },
      legend: { top: 5, left: 0, textStyle: { color: colors.muted, fontSize: typeScale.micro }, icon: "circle", itemWidth: 7, itemHeight: 7 },
      tooltip: {
        trigger: "axis", backgroundColor: colors.surface, borderColor: colors.border, textStyle: { color: colors.text, fontSize: typeScale.micro },
        formatter: (params: Array<{ seriesName: string; value: number; marker: string; axisValueLabel: string }>) =>
          [params[0]?.axisValueLabel, ...params.map((entry) => `${entry.marker}${entry.seriesName}: ${axisFormat[unitOf(entry.seriesName)](entry.value)}`)].join("<br/>"),
      },
      xAxis: { type: "category", data: labels.map((value) => timelineTimestamp(value, window)), boundaryGap: false, axisLine: { lineStyle: { color: colors.border } }, axisTick: { show: false }, axisLabel: { color: colors.muted, fontSize: typeScale.micro, hideOverlap: true } },
      yAxis: axes.map((kind, index) => ({
        type: "value",
        position: index === 0 ? "left" : "right",
        min: 0,
        // A rate series that is all zeroes has no range of its own, and the
        // chart would invent one: a "100.0%" tick above a window with no errors
        // in it.
        ...(kind === "percent" && !series.some((item) => item.axis === "percent" && item.data.some((value) => value > 0)) ? { max: 0.01 } : {}),
        splitLine: index === 0 ? { lineStyle: { color: colors.grid } } : { show: false },
        axisLabel: { color: colors.muted, fontSize: typeScale.micro, formatter: (value: number) => tickFormat[kind](value) },
      })),
      series: series.map((item) => ({ name: item.name, type: "line", yAxisIndex: axes.indexOf(item.axis ?? "count"), data: item.data, smooth: .22, showSymbol: false, lineStyle: { width: 2 }, areaStyle: { opacity: .045 } })),
    };
  }, [dark, labels, series, window]);
  return <Paper withBorder radius="md" p="sm"><Text fw={650} size="sm" mb="xs">{title}</Text><EChart option={option} height={210} label={title} /></Paper>;
}

function HeatmapView({ data, dark, window }: { data: Performance; dark: boolean; window: string }) {
  const model = useMemo(() => {
    const services = [...new Set(data.heatmap.map((point) => point.service))];
    const times = [...new Set(data.heatmap.map((point) => point.time))];
    const values = new Map(data.heatmap.map((point) => [`${point.service}\u0000${point.time}`, point.p95_ms]));
    return { services, times, values, max: Math.max(...data.heatmap.map((point) => point.p95_ms), 1) };
  }, [data.heatmap]);
  if (model.services.length === 0) return <EmptyState tall icon={<GridFour size={20} weight="duotone" />} title="No latency samples yet">The heatmap will compare service latency across time buckets.</EmptyState>;
  const colors = chartTheme(dark);
  const option = { grid: { left: 105, right: 20, top: 20, bottom: 78 }, tooltip: { position: "top", backgroundColor: colors.surface, borderColor: colors.border, textStyle: { color: colors.text, fontSize: typeScale.micro }, formatter: (params: { data: [number, number, number] }) => `${model.services[params.data[1]]}<br/>${duration(params.data[2])}` }, xAxis: { type: "category", data: model.times.map((time) => timelineTimestamp(time, window)), splitArea: { show: true }, axisLabel: { color: colors.muted, fontSize: typeScale.micro, hideOverlap: true }, axisLine: { lineStyle: { color: colors.border } } }, yAxis: { type: "category", data: model.services, splitArea: { show: true }, axisLabel: { color: colors.text, fontSize: typeScale.micro }, axisLine: { lineStyle: { color: colors.border } } }, // The scale sat on top of the time labels and said "600000" with no unit
      // — a number the reader had to guess the meaning of. It has its own band
      // now, and reads in the same units as every other latency in the product.
      visualMap: { min: 0, max: model.max, calculable: true, orient: "horizontal", left: "center", bottom: 0, itemWidth: 12, itemHeight: 90, text: ["slower", "faster"], textGap: 8, formatter: (value: number) => duration(value), textStyle: { color: colors.muted, fontSize: typeScale.micro }, inRange: { color: [colors.grid, statusHex(dark).warn, statusHex(dark).bad] } }, series: [{ type: "heatmap", data: model.services.flatMap((service, y) => model.times.map((time, x) => [x, y, model.values.get(`${service}\u0000${time}`) ?? 0])) }] };
  return <Paper withBorder radius="md" mx={{ base: "md", sm: "lg" }} mb="md" p="xs"><EChart option={option} height={Math.max(280, model.services.length * 32 + 110)} label="Service P95 latency heatmap" /></Paper>;
}

function EndpointsView({ endpoints, onEndpoint }: { endpoints: Endpoint[]; onEndpoint: (endpoint: Endpoint) => void }) {
  const routes = usePagedItems(endpoints, 8);
  if (endpoints.length === 0) return <EmptyState tall icon={<ArrowUpRight size={20} weight="duotone" />} title="No endpoints detected">HTTP routes and span operations will appear here as traffic arrives.</EmptyState>;
  return <><Table.ScrollContainer minWidth={700}><Table striped highlightOnHover verticalSpacing="sm">
    <Table.Thead><Table.Tr><Table.Th>Endpoint</Table.Th><Table.Th ta="right">Calls</Table.Th><Table.Th ta="right">P50</Table.Th><Table.Th ta="right">P95</Table.Th><Table.Th ta="right">P99</Table.Th><Table.Th ta="right">Errors</Table.Th></Table.Tr></Table.Thead>
    <Table.Tbody>{routes.pageItems.map((endpoint) => <Table.Tr key={`${endpoint.method}-${endpoint.path}`} tabIndex={0} onClick={() => onEndpoint(endpoint)} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") onEndpoint(endpoint); }} style={{ cursor: "pointer" }}><Table.Td><Badge variant="light" mr="xs">{endpoint.method}</Badge><Text component="span" ff="monospace" size="sm">{endpoint.path}</Text></Table.Td><Table.Td ta="right">{integer.format(endpoint.calls)}</Table.Td><Table.Td ta="right">{duration(endpoint.p50_ms)}</Table.Td><Table.Td ta="right">{duration(endpoint.p95_ms)}</Table.Td><Table.Td ta="right">{duration(endpoint.p99_ms)}</Table.Td><Table.Td ta="right"><Text c={healthColor(endpoint.health)}>{percent(endpoint.error_rate)}</Text></Table.Td></Table.Tr>)}</Table.Tbody>
  </Table></Table.ScrollContainer><PageControls {...routes} onChange={routes.setPage} /></>;
}

function ComparisonView({ data }: { data: Performance }) {
  if (data.comparison.length === 0) return <EmptyState tall icon={<ArrowsLeftRight size={20} weight="duotone" />} title="Nothing to compare yet">Fanout compares the first and second half of the selected window.</EmptyState>;
  return <Table.ScrollContainer minWidth={620}><Table striped verticalSpacing="sm"><Table.Thead><Table.Tr><Table.Th>Signal</Table.Th><Table.Th>Earlier</Table.Th><Table.Th>Change</Table.Th><Table.Th>Recent</Table.Th></Table.Tr></Table.Thead><Table.Tbody>{data.comparison.map((metric) => <Table.Tr key={metric.label}><Table.Td><Text fw={650}>{metric.label}</Text><Text c="dimmed" size="xs">{metric.unit}</Text></Table.Td><Table.Td>{formatMetric(metric.before, metric.unit)}</Table.Td><Table.Td><Badge color={metric.direction === "improvement" ? "ok" : metric.direction === "regression" ? "bad" : "gray"} variant="light">{metric.change_pct > 0 ? "↑" : metric.change_pct < 0 ? "↓" : "→"} {Math.abs(metric.change_pct).toFixed(1)}%</Badge>{metric.significant && <Text c="dimmed" size="xs" mt={3}>notable</Text>}</Table.Td><Table.Td>{formatMetric(metric.after, metric.unit)}</Table.Td></Table.Tr>)}</Table.Tbody></Table></Table.ScrollContainer>;
}

function formatMetric(value: number, unit: string) { if (unit === "ms") return duration(value); if (unit === "%") return `${value.toFixed(2)}%`; return integer.format(value); }
createRoot(document.getElementById("root")!).render(<StrictMode><PerformanceApp /></StrictMode>);
