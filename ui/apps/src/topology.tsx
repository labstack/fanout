import { GraphChart, HeatmapChart, SankeyChart } from "echarts/charts";
import { StrictMode, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import { EmptyState, Tabs } from "./components";
import type { Edge, Result, Topology } from "./contracts";
import { EChart, useECharts } from "./echart";
import { duration, integer, percent, windowLabel } from "./format";
import { askAbout, useFanoutApp } from "./use-fanout-app";
import "./app.css";
import "./topology.css";

type View = "graph" | "flow" | "matrix";
useECharts([GraphChart, SankeyChart, HeatmapChart]);

function TopologyApp() {
  const { app, callTool, error, host, result, toolError } = useFanoutApp<Result<Topology>>("Fanout service topology");
  const [selected, setSelected] = useState<string | null>(null);
  const [view, setView] = useState<View>("graph");
  return <main className={`app ${host?.theme === "dark" ? "dark" : ""}`}>
    <header className="header"><div><div className="eyebrow">Service map</div><h1 className="title">Dependencies</h1>{result && <p className="summary">{result.data.nodes.length} services connected by {result.data.edges.length} routes</p>}</div><button className="refresh" onClick={() => callTool("service_topology")} disabled={!app}>Refresh</button></header>
    {(error || toolError) && <div className="error">{toolError ?? "This view could not be loaded. Please try again."}</div>}
    {!result && !error && !toolError && <div className="loading">Loading service relationships…</div>}
    {result && result.data.nodes.length === 0 && <><EmptyState icon="⌘" title="No service relationships yet">Connections will appear as services communicate.</EmptyState><footer className="meta"><span>{windowLabel(result.provenance.window)}</span><span>No routes found</span></footer></>}
    {result && result.data.nodes.length > 0 && <>
      <Tabs active={view} onChange={setView} items={[{ id: "graph", label: "Graph" }, { id: "flow", label: "Traffic flow" }, { id: "matrix", label: "Matrix" }]} />
      <TopologyBody data={result.data} view={view} selected={selected} onSelect={setSelected} onInvestigate={(service) => askAbout(app, `Investigate dependencies and failures around ${service}.`)} />
      <footer className="meta"><span>{windowLabel(result.provenance.window)}</span><span>{result.data.nodes.length} services · {result.data.edges.length} routes</span></footer>
    </>}
  </main>;
}

function TopologyBody({ data, view, selected, onSelect, onInvestigate }: { data: Topology; view: View; selected: string | null; onSelect: (id: string) => void; onInvestigate: (id: string) => void }) {
  const activeEdges = selected ? data.edges.filter((edge) => edge.caller === selected || edge.callee === selected) : data.edges;
  return <>
    <section className="graph-wrap">
      {view === "graph" && <GraphView data={data} selected={selected} onSelect={onSelect} />}
      {view === "flow" && <FlowView data={data} onSelect={onSelect} />}
      {view === "matrix" && <MatrixView data={data} />}
      {selected && <button className="investigate" onClick={() => onInvestigate(selected)}>Investigate {selected}</button>}
    </section>
    <EdgeList edges={activeEdges} onSelect={onSelect} />
  </>;
}

function GraphView({ data, selected, onSelect }: { data: Topology; selected: string | null; onSelect: (id: string) => void }) {
  const option = useMemo(() => ({
    tooltip: { backgroundColor: "#1a221e", borderColor: "#2b3832", textStyle: { color: "#eff5f1", fontSize: 10 } },
    series: [{ type: "graph", layout: "force", roam: true, draggable: true, force: { repulsion: 220, edgeLength: [80, 150], gravity: .08 }, label: { show: true, position: "bottom", color: "#dfe7e2", fontSize: 10 }, edgeSymbol: ["none", "arrow"], edgeSymbolSize: 6,
      data: data.nodes.map((node) => ({ id: node.service, name: node.service, value: node.spans, symbolSize: Math.min(46, 24 + Math.log10(Math.max(node.spans, 1)) * 5), itemStyle: { color: "#121815", borderColor: healthColor(node.health), borderWidth: selected === node.service ? 5 : 3, opacity: selected && selected !== node.service ? .45 : 1 } })),
      links: data.edges.map((edge) => ({ source: edge.caller, target: edge.callee, value: edge.calls, lineStyle: { width: Math.min(5, 1 + Math.log10(Math.max(edge.calls, 1))), color: edge.error_rate >= .05 ? "#f06a6a" : "#819087", opacity: selected && edge.caller !== selected && edge.callee !== selected ? .1 : .42, curveness: .08 } })),
      emphasis: { focus: "adjacency", lineStyle: { opacity: .85 } },
    }],
  }), [data, selected]);
  return <EChart option={option} height={350} label="Interactive service dependency graph" onClick={(params) => { const item = params as { dataType?: string; data?: { id?: string } }; if (item.dataType === "node" && item.data?.id) onSelect(item.data.id); }} />;
}

function FlowView({ data, onSelect }: { data: Topology; onSelect: (id: string) => void }) {
  const links = useMemo(() => acyclicEdges(data.edges), [data.edges]);
  const option = useMemo(() => ({
    tooltip: { trigger: "item", backgroundColor: "#1a221e", borderColor: "#2b3832", textStyle: { color: "#eff5f1", fontSize: 10 } },
    series: [{ type: "sankey", left: 20, right: 30, top: 20, bottom: 20, nodeWidth: 14, nodeGap: 12, draggable: true, emphasis: { focus: "adjacency" }, label: { color: "#dfe7e2", fontSize: 10 }, lineStyle: { color: "gradient", opacity: .28, curveness: .55 }, data: data.nodes.map((node) => ({ name: node.service, itemStyle: { color: healthColor(node.health), borderColor: "#121815", borderWidth: 2 } })), links: links.map((edge) => ({ source: edge.caller, target: edge.callee, value: Math.max(edge.calls, 1) })) }],
  }), [data.nodes, links]);
  if (links.length === 0) return <EmptyState icon="⇢" title="No traffic routes observed">Services are visible, but this window contains no direct service-to-service calls.</EmptyState>;
  return <><EChart option={option} height={350} label="Primary service traffic flow" onClick={(params) => { const item = params as { dataType?: string; name?: string }; if (item.dataType === "node" && item.name) onSelect(item.name); }} /><p className="flow-note">Showing {links.length} primary routes from {data.edges.length} observed connections</p></>;
}

function MatrixView({ data }: { data: Topology }) {
  const names = data.nodes.map((node) => node.service);
  const max = Math.max(...data.edges.map((edge) => edge.error_rate), .01);
  const option = useMemo(() => ({
    grid: { left: 100, right: 25, top: 15, bottom: 80 },
    tooltip: { backgroundColor: "#1a221e", borderColor: "#2b3832", textStyle: { color: "#eff5f1", fontSize: 10 }, formatter: (params: { data: [number, number, number, number, number] }) => `${names[params.data[1]]} → ${names[params.data[0]]}<br/>${integer.format(params.data[3])} calls · ${duration(params.data[4])}<br/>${percent(params.data[2])} errors` },
    xAxis: { type: "category", data: names, splitArea: { show: true }, axisLabel: { color: "#8e9b93", rotate: 35, fontSize: 9 }, axisLine: { lineStyle: { color: "#2b3832" } } },
    yAxis: { type: "category", data: names, splitArea: { show: true }, axisLabel: { color: "#aab5ae", fontSize: 9 }, axisLine: { lineStyle: { color: "#2b3832" } } },
    visualMap: { min: 0, max, calculable: true, orient: "horizontal", left: "center", bottom: 8, textStyle: { color: "#7f8d85", fontSize: 8 }, inRange: { color: ["#1a221e", "#7b5b22", "#f06a6a"] } },
    series: [{ type: "heatmap", data: data.edges.map((edge) => [names.indexOf(edge.callee), names.indexOf(edge.caller), edge.error_rate, edge.calls, edge.average_ms]) }],
  }), [data, max, names]);
  return <EChart option={option} height={380} label="Service dependency error matrix" />;
}

function EdgeList({ edges, onSelect }: { edges: Edge[]; onSelect: (id: string) => void }) {
  if (edges.length === 0) return null;
  return <section className="edges"><div className="edge-row edge-head"><span>Route</span><span>Calls</span><span>Latency</span><span>Errors</span></div>{edges.map((edge) => <button className="edge-row" key={`${edge.caller}-${edge.callee}-${edge.type}`} onClick={() => onSelect(edge.caller)}><span>{edge.caller} <b>→</b> {edge.callee}</span><span>{integer.format(edge.calls)}</span><span>{duration(edge.average_ms)}</span><span>{percent(edge.error_rate)}</span></button>)}</section>;
}

function healthColor(health: string) {
  if (health === "unhealthy") return "#f06a6a";
  if (health === "degraded") return "#e3a33d";
  return "#44c69a";
}

function acyclicEdges(edges: Edge[]) {
  const kept: Edge[] = [];
  const adjacency = new Map<string, Set<string>>();
  const reaches = (from: string, target: string, seen = new Set<string>()): boolean => {
    if (from === target) return true;
    if (seen.has(from)) return false;
    seen.add(from);
    for (const next of adjacency.get(from) ?? []) if (reaches(next, target, seen)) return true;
    return false;
  };
  for (const edge of [...edges].sort((left, right) => right.calls - left.calls || left.caller.localeCompare(right.caller) || left.callee.localeCompare(right.callee))) {
    if (edge.caller === edge.callee || reaches(edge.callee, edge.caller)) continue;
    const outgoing = adjacency.get(edge.caller) ?? new Set<string>();
    outgoing.add(edge.callee);
    adjacency.set(edge.caller, outgoing);
    kept.push(edge);
  }
  return kept;
}

createRoot(document.getElementById("root")!).render(<StrictMode><TopologyApp /></StrictMode>);
