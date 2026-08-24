import { GraphChart, HeatmapChart, SankeyChart } from "echarts/charts";
import { Button, Paper, Stack, Table, Text } from "@mantine/core";
import { FlowArrow, MagnifyingGlass, ShareNetwork } from "@phosphor-icons/react";
import { StrictMode, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import { EmptyState, MetaFooter, PageControls, Tabs, ViewHeader, ViewShell, ViewStatus, chartTheme, statusHex, usePagedItems } from "./components";
import type { Edge, Result, Topology } from "./contracts";
import { EChart, useECharts } from "./echart";
import { duration, integer, percent, windowLabel } from "./format";
import { askAbout, useFanoutApp } from "./use-fanout-app";
import "./app.css";

type View = "graph" | "flow" | "matrix";
useECharts([GraphChart, SankeyChart, HeatmapChart]);

function TopologyApp() {
  const { app, callTool, error, host, result, toolError } = useFanoutApp<Result<Topology>>("Fanout service topology");
  const [selected, setSelected] = useState<string | null>(null);
  const [view, setView] = useState<View>("graph");
  const dark = host?.theme === "dark";
  return <ViewShell dark={dark}>
    <ViewHeader eyebrow="Service map" title="Dependencies" summary={result ? `${result.data.nodes.length} services connected by ${result.data.edges.length} routes` : undefined} onRefresh={() => callTool("service_topology")} disabled={!app} />
    <ViewStatus error={toolError ?? (error ? "This view could not be loaded. Please try again." : null)} loading={!result && !error && !toolError ? "Loading service relationships…" : undefined} />
    {result && result.data.nodes.length === 0 && <><EmptyState tall icon={<ShareNetwork size={20} weight="duotone" />} title="No service relationships yet">Connections will appear as services communicate.</EmptyState><MetaFooter left={windowLabel(result.provenance.window)} right="No routes found" /></>}
    {result && result.data.nodes.length > 0 && <>
      <Tabs active={view} onChange={setView} items={[{ id: "graph", label: "Graph" }, { id: "flow", label: "Traffic flow" }, { id: "matrix", label: "Matrix" }]} />
      <TopologyBody data={result.data} view={view} selected={selected} dark={dark} onSelect={setSelected} onInvestigate={(service) => askAbout(app, `Investigate dependencies and failures around ${service}.`)} />
      <MetaFooter left={windowLabel(result.provenance.window)} right={`${result.data.nodes.length} services · ${result.data.edges.length} routes`} />
    </>}
  </ViewShell>;
}

function TopologyBody({ data, view, selected, dark, onSelect, onInvestigate }: { data: Topology; view: View; selected: string | null; dark: boolean; onSelect: (id: string) => void; onInvestigate: (id: string) => void }) {
  const activeEdges = selected ? data.edges.filter((edge) => edge.caller === selected || edge.callee === selected) : data.edges;
  return <Stack px={{ base: "md", sm: "lg" }} pb="md">
    <Paper withBorder radius="md" p="xs" pos="relative">
      {view === "graph" && <GraphView data={data} selected={selected} dark={dark} onSelect={onSelect} />}
      {view === "flow" && <FlowView data={data} dark={dark} onSelect={onSelect} />}
      {view === "matrix" && <MatrixView data={data} dark={dark} />}
      {selected && <Button pos="absolute" top="sm" right="sm" size="xs" variant="default" leftSection={<MagnifyingGlass size={14} weight="bold" />} onClick={() => onInvestigate(selected)}>Investigate {selected}</Button>}
    </Paper>
    <EdgeList edges={activeEdges} onSelect={onSelect} />
  </Stack>;
}

function GraphView({ data, selected, dark, onSelect }: { data: Topology; selected: string | null; dark: boolean; onSelect: (id: string) => void }) {
  const option = useMemo(() => {
    const colors = chartTheme(dark);
    const status = statusHex(dark);
    return { tooltip: { backgroundColor: colors.surface, borderColor: colors.border, textStyle: { color: colors.text, fontSize: 10 } }, series: [{ type: "graph", layout: "force", roam: true, draggable: true, force: { repulsion: 220, edgeLength: [80, 150], gravity: .08 }, label: { show: true, position: "bottom", color: colors.text, fontSize: 10 }, edgeSymbol: ["none", "arrow"], edgeSymbolSize: 6, data: data.nodes.map((node) => ({ id: node.service, name: node.service, value: node.spans, symbolSize: Math.min(46, 24 + Math.log10(Math.max(node.spans, 1)) * 5), itemStyle: { color: colors.surface, borderColor: healthHex(node.health, dark), borderWidth: selected === node.service ? 5 : 3, opacity: selected && selected !== node.service ? .45 : 1 } })), links: data.edges.map((edge) => ({ source: edge.caller, target: edge.callee, value: edge.calls, lineStyle: { width: Math.min(5, 1 + Math.log10(Math.max(edge.calls, 1))), color: edge.error_rate >= .05 ? status.bad : colors.muted, opacity: selected && edge.caller !== selected && edge.callee !== selected ? .1 : .42, curveness: .08 } })), emphasis: { focus: "adjacency", lineStyle: { opacity: .85 } } }] };
  }, [dark, data, selected]);
  return <EChart option={option} height={350} label="Interactive service dependency graph" onClick={(params) => { const item = params as { dataType?: string; data?: { id?: string } }; if (item.dataType === "node" && item.data?.id) onSelect(item.data.id); }} />;
}

function FlowView({ data, dark, onSelect }: { data: Topology; dark: boolean; onSelect: (id: string) => void }) {
  const links = useMemo(() => acyclicEdges(data.edges), [data.edges]);
  const option = useMemo(() => {
    const colors = chartTheme(dark);
    return { tooltip: { trigger: "item", backgroundColor: colors.surface, borderColor: colors.border, textStyle: { color: colors.text, fontSize: 10 } }, series: [{ type: "sankey", left: 20, right: 30, top: 20, bottom: 20, nodeWidth: 14, nodeGap: 12, draggable: true, emphasis: { focus: "adjacency" }, label: { color: colors.text, fontSize: 10 }, lineStyle: { color: "gradient", opacity: .28, curveness: .55 }, data: data.nodes.map((node) => ({ name: node.service, itemStyle: { color: healthHex(node.health, dark), borderColor: colors.surface, borderWidth: 2 } })), links: links.map((edge) => ({ source: edge.caller, target: edge.callee, value: Math.max(edge.calls, 1) })) }] };
  }, [dark, data.nodes, links]);
  if (links.length === 0) return <EmptyState tall icon={<FlowArrow size={20} weight="duotone" />} title="No traffic routes observed">Services are visible, but this window contains no direct service-to-service calls.</EmptyState>;
  return <><EChart option={option} height={350} label="Primary service traffic flow" onClick={(params) => { const item = params as { dataType?: string; name?: string }; if (item.dataType === "node" && item.name) onSelect(item.name); }} /><Text c="dimmed" size="xs" ta="center">Showing {links.length} primary routes from {data.edges.length} observed connections</Text></>;
}

function MatrixView({ data, dark }: { data: Topology; dark: boolean }) {
  const names = data.nodes.map((node) => node.service);
  const max = Math.max(...data.edges.map((edge) => edge.error_rate), .01);
  const option = useMemo(() => {
    const colors = chartTheme(dark);
    const status = statusHex(dark);
    return { grid: { left: 100, right: 25, top: 15, bottom: 80 }, tooltip: { backgroundColor: colors.surface, borderColor: colors.border, textStyle: { color: colors.text, fontSize: 10 }, formatter: (params: { data: [number, number, number, number, number] }) => `${names[params.data[1]]} → ${names[params.data[0]]}<br/>${integer.format(params.data[3])} calls · ${duration(params.data[4])}<br/>${percent(params.data[2])} errors` }, xAxis: { type: "category", data: names, splitArea: { show: true }, axisLabel: { color: colors.muted, rotate: 35, fontSize: 9 }, axisLine: { lineStyle: { color: colors.border } } }, yAxis: { type: "category", data: names, splitArea: { show: true }, axisLabel: { color: colors.text, fontSize: 9 }, axisLine: { lineStyle: { color: colors.border } } }, visualMap: { min: 0, max, calculable: true, orient: "horizontal", left: "center", bottom: 8, textStyle: { color: colors.muted, fontSize: 8 }, inRange: { color: [colors.grid, status.warn, status.bad] } }, series: [{ type: "heatmap", data: data.edges.map((edge) => [names.indexOf(edge.callee), names.indexOf(edge.caller), edge.error_rate, edge.calls, edge.average_ms]) }] };
  }, [dark, data, max, names]);
  return <EChart option={option} height={380} label="Service dependency error matrix" />;
}

function EdgeList({ edges, onSelect }: { edges: Edge[]; onSelect: (id: string) => void }) {
  const routes = usePagedItems(edges, 4);
  if (edges.length === 0) return null;
  return <Paper withBorder radius="md" style={{ overflow: "hidden" }}><Table.ScrollContainer minWidth={520}><Table striped highlightOnHover verticalSpacing="sm"><Table.Thead><Table.Tr><Table.Th>Route</Table.Th><Table.Th>Calls</Table.Th><Table.Th>Latency</Table.Th><Table.Th>Errors</Table.Th></Table.Tr></Table.Thead><Table.Tbody>{routes.pageItems.map((edge) => <Table.Tr key={`${edge.caller}-${edge.callee}-${edge.type}`} tabIndex={0} onClick={() => onSelect(edge.caller)} onKeyDown={(event) => { if (event.key === "Enter" || event.key === " ") onSelect(edge.caller); }} style={{ cursor: "pointer" }}><Table.Td><Text fw={600} size="sm">{edge.caller} → {edge.callee}</Text></Table.Td><Table.Td>{integer.format(edge.calls)}</Table.Td><Table.Td>{duration(edge.average_ms)}</Table.Td><Table.Td>{percent(edge.error_rate)}</Table.Td></Table.Tr>)}</Table.Tbody></Table></Table.ScrollContainer><PageControls {...routes} onChange={routes.setPage} /></Paper>;
}

function healthHex(health: string, dark: boolean) { const status = statusHex(dark); return health === "unhealthy" ? status.bad : health === "degraded" ? status.warn : status.ok; }
function acyclicEdges(edges: Edge[]) { const kept: Edge[] = []; const adjacency = new Map<string, Set<string>>(); const reaches = (from: string, target: string, seen = new Set<string>()): boolean => { if (from === target) return true; if (seen.has(from)) return false; seen.add(from); for (const next of adjacency.get(from) ?? []) if (reaches(next, target, seen)) return true; return false; }; for (const edge of [...edges].sort((left, right) => right.calls - left.calls || left.caller.localeCompare(right.caller) || left.callee.localeCompare(right.callee))) { if (edge.caller === edge.callee || reaches(edge.callee, edge.caller)) continue; const outgoing = adjacency.get(edge.caller) ?? new Set<string>(); outgoing.add(edge.callee); adjacency.set(edge.caller, outgoing); kept.push(edge); } return kept; }

createRoot(document.getElementById("root")!).render(<StrictMode><TopologyApp /></StrictMode>);
