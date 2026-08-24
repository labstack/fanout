import { HeatmapChart, LineChart } from "echarts/charts";
import { Badge, Paper, SimpleGrid, Stack, Table, Text } from "@mantine/core";
import { ArrowUpRight, ArrowsLeftRight, GridFour, Pulse } from "@phosphor-icons/react";
import { StrictMode, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import { EmptyState, MetaFooter, Metric, PageControls, Tabs, ViewHeader, ViewShell, ViewStatus, chartTheme, healthColor, seriesColor, statusHex, usePagedItems } from "./components";
import type { Endpoint, Performance, Result } from "./contracts";
import { EChart, useECharts } from "./echart";
import { duration, integer, percent, windowLabel } from "./format";
import { askAbout, useFanoutApp } from "./use-fanout-app";
import "./app.css";

type View = "activity" | "latency" | "endpoints" | "compare";
useECharts([LineChart, HeatmapChart]);

function PerformanceApp() {
  const { app, callTool, error, host, result, toolError } = useFanoutApp<Result<Performance>>("Fanout service performance");
  const [view, setView] = useState<View>("activity");
  const dark = host?.theme === "dark";
  return <ViewShell dark={dark}>
    <ViewHeader eyebrow="Trends and latency" title={result?.data.service || "System performance"} summary={result ? `Traffic, latency, and errors ${result.data.service ? `for ${result.data.service}` : "across all services"}` : undefined} onRefresh={() => callTool("service_performance")} disabled={!app} />
    <ViewStatus error={toolError ?? (error ? "This view could not be loaded. Please try again." : null)} loading={!result && !error && !toolError ? "Loading performance signals…" : undefined} />
    {result && <>
      <Tabs active={view} onChange={setView} items={[{ id: "activity", label: "Activity" }, { id: "latency", label: "Latency map" }, { id: "endpoints", label: "Endpoints", count: result.data.endpoints.length }, { id: "compare", label: "Compare" }]} />
      {view === "activity" && <ActivityView data={result.data} dark={dark} />}
      {view === "latency" && <HeatmapView data={result.data} dark={dark} />}
      {view === "endpoints" && <EndpointsView endpoints={result.data.endpoints} onEndpoint={(endpoint) => askAbout(app, `Investigate ${endpoint.method} ${endpoint.path}. Explain its latency and errors.`)} />}
      {view === "compare" && <ComparisonView data={result.data} />}
      <MetaFooter left={windowLabel(result.provenance.window)} right={`Updated ${new Date(result.provenance.generated_at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}`} />
    </>}
  </ViewShell>;
}

function ActivityView({ data, dark }: { data: Performance; dark: boolean }) {
  const last = data.points.at(-1);
  if (!last) return <EmptyState tall icon={<Pulse size={20} weight="duotone" />} title="No activity in this window">Trends will appear as activity is recorded.</EmptyState>;
  const labels = data.points.map((point) => point.time);
  return <Stack px={{ base: "md", sm: "lg" }} pb="md">
    <SimpleGrid cols={{ base: 1, xs: 3 }} spacing="sm"><Metric label="Operations" value={integer.format(last.spans)} /><Metric label="P95 latency" value={duration(last.p95_ms)} color={last.p95_ms >= 750 ? "warn" : "ok"} /><Metric label="Error rate" value={percent(last.error_rate)} color={last.error_rate >= .01 ? "bad" : "ok"} /></SimpleGrid>
    <PerformanceChart dark={dark} labels={labels} title="Traffic and logs" series={[{ name: "Operations", data: data.points.map((point) => point.spans), color: seriesColor("operations", dark) }, { name: "Logs", data: data.points.map((point) => point.log_count), color: seriesColor("logs", dark) }]} />
    <PerformanceChart dark={dark} labels={labels} title="Latency and error correlation" series={[{ name: "P95 latency", data: data.points.map((point) => point.p95_ms), color: statusHex(dark).warn }, { name: "Error rate × 1000", data: data.points.map((point) => point.error_rate * 1000), color: statusHex(dark).bad }]} />
  </Stack>;
}

function PerformanceChart({ labels, title, series, dark }: { labels: string[]; title: string; series: Array<{ name: string; data: number[]; color: string }>; dark: boolean }) {
  const option = useMemo(() => {
    const colors = chartTheme(dark);
    return { color: series.map((item) => item.color), grid: { left: 42, right: 18, top: 42, bottom: 30 }, legend: { top: 5, left: 0, textStyle: { color: colors.muted, fontSize: 10 }, icon: "circle", itemWidth: 7, itemHeight: 7 }, tooltip: { trigger: "axis", backgroundColor: colors.surface, borderColor: colors.border, textStyle: { color: colors.text, fontSize: 10 } }, xAxis: { type: "category", data: labels.map((value) => new Date(value).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })), boundaryGap: false, axisLine: { lineStyle: { color: colors.border } }, axisTick: { show: false }, axisLabel: { color: colors.muted, fontSize: 9, hideOverlap: true } }, yAxis: { type: "value", splitLine: { lineStyle: { color: colors.grid } }, axisLabel: { color: colors.muted, fontSize: 9 } }, series: series.map((item) => ({ name: item.name, type: "line", data: item.data, smooth: .22, showSymbol: false, lineStyle: { width: 2 }, areaStyle: { opacity: .045 } })) };
  }, [dark, labels, series]);
  return <Paper withBorder radius="md" p="sm"><Text fw={650} size="sm" mb="xs">{title}</Text><EChart option={option} height={210} label={title} /></Paper>;
}

function HeatmapView({ data, dark }: { data: Performance; dark: boolean }) {
  const model = useMemo(() => {
    const services = [...new Set(data.heatmap.map((point) => point.service))];
    const times = [...new Set(data.heatmap.map((point) => point.time))];
    const values = new Map(data.heatmap.map((point) => [`${point.service}\u0000${point.time}`, point.p95_ms]));
    return { services, times, values, max: Math.max(...data.heatmap.map((point) => point.p95_ms), 1) };
  }, [data.heatmap]);
  if (model.services.length === 0) return <EmptyState tall icon={<GridFour size={20} weight="duotone" />} title="No latency samples yet">The heatmap will compare service latency across time buckets.</EmptyState>;
  const colors = chartTheme(dark);
  const option = { grid: { left: 105, right: 20, top: 20, bottom: 45 }, tooltip: { position: "top", backgroundColor: colors.surface, borderColor: colors.border, textStyle: { color: colors.text, fontSize: 10 }, formatter: (params: { data: [number, number, number] }) => `${model.services[params.data[1]]}<br/>${duration(params.data[2])}` }, xAxis: { type: "category", data: model.times.map((time) => new Date(time).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })), splitArea: { show: true }, axisLabel: { color: colors.muted, fontSize: 9, hideOverlap: true }, axisLine: { lineStyle: { color: colors.border } } }, yAxis: { type: "category", data: model.services, splitArea: { show: true }, axisLabel: { color: colors.text, fontSize: 9 }, axisLine: { lineStyle: { color: colors.border } } }, visualMap: { min: 0, max: model.max, calculable: true, orient: "horizontal", left: "center", bottom: 0, textStyle: { color: colors.muted, fontSize: 8 }, inRange: { color: [colors.grid, statusHex(dark).warn, statusHex(dark).bad] } }, series: [{ type: "heatmap", data: model.services.flatMap((service, y) => model.times.map((time, x) => [x, y, model.values.get(`${service}\u0000${time}`) ?? 0])) }] };
  return <Paper withBorder radius="md" mx={{ base: "md", sm: "lg" }} mb="md" p="xs"><EChart option={option} height={Math.max(280, model.services.length * 32 + 110)} label="Service P95 latency heatmap" /></Paper>;
}

function EndpointsView({ endpoints, onEndpoint }: { endpoints: Endpoint[]; onEndpoint: (endpoint: Endpoint) => void }) {
  const routes = usePagedItems(endpoints, 8);
  if (endpoints.length === 0) return <EmptyState tall icon={<ArrowUpRight size={20} weight="duotone" />} title="No endpoints detected">HTTP routes and span operations will appear here as traffic arrives.</EmptyState>;
  return <><Table.ScrollContainer minWidth={700}><Table striped highlightOnHover verticalSpacing="sm">
    <Table.Thead><Table.Tr><Table.Th>Endpoint</Table.Th><Table.Th>Calls</Table.Th><Table.Th>P50</Table.Th><Table.Th>P95</Table.Th><Table.Th>P99</Table.Th><Table.Th>Errors</Table.Th></Table.Tr></Table.Thead>
    <Table.Tbody>{routes.pageItems.map((endpoint) => <Table.Tr key={`${endpoint.method}-${endpoint.path}`} tabIndex={0} onClick={() => onEndpoint(endpoint)} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") onEndpoint(endpoint); }} style={{ cursor: "pointer" }}><Table.Td><Badge variant="light" mr="xs">{endpoint.method}</Badge><Text component="code" size="sm">{endpoint.path}</Text></Table.Td><Table.Td>{integer.format(endpoint.calls)}</Table.Td><Table.Td>{duration(endpoint.p50_ms)}</Table.Td><Table.Td>{duration(endpoint.p95_ms)}</Table.Td><Table.Td>{duration(endpoint.p99_ms)}</Table.Td><Table.Td><Text c={healthColor(endpoint.health)}>{percent(endpoint.error_rate)}</Text></Table.Td></Table.Tr>)}</Table.Tbody>
  </Table></Table.ScrollContainer><PageControls {...routes} onChange={routes.setPage} /></>;
}

function ComparisonView({ data }: { data: Performance }) {
  if (data.comparison.length === 0) return <EmptyState tall icon={<ArrowsLeftRight size={20} weight="duotone" />} title="Nothing to compare yet">Fanout compares the first and second half of the selected window.</EmptyState>;
  return <Table.ScrollContainer minWidth={620}><Table striped verticalSpacing="sm"><Table.Thead><Table.Tr><Table.Th>Signal</Table.Th><Table.Th>Earlier</Table.Th><Table.Th>Change</Table.Th><Table.Th>Recent</Table.Th></Table.Tr></Table.Thead><Table.Tbody>{data.comparison.map((metric) => <Table.Tr key={metric.label}><Table.Td><Text fw={650}>{metric.label}</Text><Text c="dimmed" size="xs">{metric.unit}</Text></Table.Td><Table.Td>{formatMetric(metric.before, metric.unit)}</Table.Td><Table.Td><Badge color={metric.direction === "improvement" ? "ok" : metric.direction === "regression" ? "bad" : "gray"} variant="light">{metric.change_pct > 0 ? "↑" : metric.change_pct < 0 ? "↓" : "→"} {Math.abs(metric.change_pct).toFixed(1)}%</Badge>{metric.significant && <Text c="dimmed" size="xs" mt={3}>notable</Text>}</Table.Td><Table.Td>{formatMetric(metric.after, metric.unit)}</Table.Td></Table.Tr>)}</Table.Tbody></Table></Table.ScrollContainer>;
}

function formatMetric(value: number, unit: string) { if (unit === "ms") return duration(value); if (unit === "%") return `${value.toFixed(2)}%`; return integer.format(value); }
createRoot(document.getElementById("root")!).render(<StrictMode><PerformanceApp /></StrictMode>);
