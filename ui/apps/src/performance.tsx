import { StrictMode, useMemo, useState } from "react";
import { ArrowUpRight, ArrowsLeftRight, GridFour, Pulse } from "@phosphor-icons/react";
import { createRoot } from "react-dom/client";
import { HeatmapChart, LineChart } from "echarts/charts";
import { EmptyState, RefreshButton, Tabs } from "./components";
import type { Endpoint, Performance, Result } from "./contracts";
import { EChart, useECharts } from "./echart";
import { duration, integer, percent, windowLabel } from "./format";
import { askAbout, useFanoutApp } from "./use-fanout-app";
import "./app.css";
import "./performance.css";

type View = "activity" | "latency" | "endpoints" | "compare";

useECharts([LineChart, HeatmapChart]);

function PerformanceApp() {
  const { app, callTool, error, host, result, toolError } = useFanoutApp<Result<Performance>>("Fanout service performance");
  const [view, setView] = useState<View>("activity");
  return <main className={`app ${host?.theme === "dark" ? "dark" : ""}`}>
    <header className="header"><div><div className="eyebrow">Trends and latency</div><h1 className="title">{result?.data.service || "System performance"}</h1>{result && <p className="summary">Traffic, latency, and errors {result.data.service ? `for ${result.data.service}` : "across all services"}</p>}</div><RefreshButton onClick={() => callTool("service_performance")} disabled={!app} /></header>
    {(error || toolError) && <div className="error">{toolError ?? "This view could not be loaded. Please try again."}</div>}
    {!result && !error && !toolError && <div className="loading">Loading performance signals…</div>}
    {result && <>
      <Tabs active={view} onChange={setView} items={[{ id: "activity", label: "Activity" }, { id: "latency", label: "Latency map" }, { id: "endpoints", label: "Endpoints", count: result.data.endpoints.length }, { id: "compare", label: "Compare" }]} />
      {view === "activity" && <ActivityView data={result.data} />}
      {view === "latency" && <HeatmapView data={result.data} />}
      {view === "endpoints" && <EndpointsView endpoints={result.data.endpoints} onEndpoint={(endpoint) => askAbout(app, `Investigate ${endpoint.method} ${endpoint.path}. Explain its latency and errors.`)} />}
      {view === "compare" && <ComparisonView data={result.data} />}
      <footer className="meta"><span>{windowLabel(result.provenance.window)}</span><span>Updated {new Date(result.provenance.generated_at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}</span></footer>
    </>}
  </main>;
}

function ActivityView({ data }: { data: Performance }) {
  const last = data.points.at(-1);
  if (!last) return <EmptyState icon={<Pulse size={18} weight="duotone" />} title="No activity in this window">Trends will appear as activity is recorded.</EmptyState>;
  const labels = data.points.map((point) => point.time);
  return <div className="performance-view">
    <section className="metrics performance-metrics"><Metric label="Operations" value={integer.format(last.spans)} /><Metric label="P95 latency" value={duration(last.p95_ms)} tone={last.p95_ms >= 750 ? "degraded" : "healthy"} /><Metric label="Error rate" value={percent(last.error_rate)} tone={last.error_rate >= .01 ? "degraded" : "healthy"} /></section>
    <PerformanceChart labels={labels} title="Traffic and logs" series={[{ name: "Operations", data: data.points.map((point) => point.spans), color: "#7bdff2" }, { name: "Logs", data: data.points.map((point) => point.log_count), color: "#a7f06a" }]} />
    <PerformanceChart labels={labels} title="Latency and error correlation" series={[{ name: "P95 latency", data: data.points.map((point) => point.p95_ms), color: "#e3a33d" }, { name: "Error rate × 1000", data: data.points.map((point) => point.error_rate * 1000), color: "#f06a6a" }]} />
  </div>;
}

function PerformanceChart({ labels, title, series }: { labels: string[]; title: string; series: Array<{ name: string; data: number[]; color: string }> }) {
  const option = useMemo(() => ({
    color: series.map((item) => item.color),
    grid: { left: 38, right: 18, top: 40, bottom: 30 },
    legend: { top: 5, left: 0, textStyle: { color: "#9eaca5", fontSize: 10 }, icon: "circle", itemWidth: 7, itemHeight: 7 },
    tooltip: { trigger: "axis", backgroundColor: "#1a221e", borderColor: "#2b3832", textStyle: { color: "#eff5f1", fontSize: 10 } },
    xAxis: { type: "category", data: labels.map((value) => new Date(value).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })), boundaryGap: false, axisLine: { lineStyle: { color: "#2b3832" } }, axisTick: { show: false }, axisLabel: { color: "#7f8d85", fontSize: 9, hideOverlap: true } },
    yAxis: { type: "value", splitLine: { lineStyle: { color: "#253029" } }, axisLabel: { color: "#7f8d85", fontSize: 9 } },
    series: series.map((item) => ({ name: item.name, type: "line", data: item.data, smooth: .22, showSymbol: false, lineStyle: { width: 2 }, areaStyle: { opacity: .045 } })),
  }), [labels, series]);
  return <section className="chart-panel"><h2>{title}</h2><EChart option={option} height={210} label={title} /></section>;
}

function Metric({ label, value, tone }: { label: string; value: string; tone?: string }) {
  return <div className="metric"><span>{label}</span><strong className={tone ? `health-${tone}` : ""}>{value}</strong></div>;
}

function HeatmapView({ data }: { data: Performance }) {
  const model = useMemo(() => {
    const services = [...new Set(data.heatmap.map((point) => point.service))];
    const times = [...new Set(data.heatmap.map((point) => point.time))];
    const values = new Map(data.heatmap.map((point) => [`${point.service}\u0000${point.time}`, point.p95_ms]));
    const max = Math.max(...data.heatmap.map((point) => point.p95_ms), 1);
    return { services, times, values, max };
  }, [data.heatmap]);
  if (model.services.length === 0) return <EmptyState icon={<GridFour size={18} weight="duotone" />} title="No latency samples yet">The heatmap will compare service latency across time buckets.</EmptyState>;
  const option = {
    grid: { left: 105, right: 20, top: 20, bottom: 45 },
    tooltip: { position: "top", backgroundColor: "#1a221e", borderColor: "#2b3832", textStyle: { color: "#eff5f1", fontSize: 10 }, formatter: (params: { data: [number, number, number] }) => `${model.services[params.data[1]]}<br/>${duration(params.data[2])}` },
    xAxis: { type: "category", data: model.times.map((time) => new Date(time).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })), splitArea: { show: true }, axisLabel: { color: "#7f8d85", fontSize: 9, hideOverlap: true }, axisLine: { lineStyle: { color: "#2b3832" } } },
    yAxis: { type: "category", data: model.services, splitArea: { show: true }, axisLabel: { color: "#aab5ae", fontSize: 9 }, axisLine: { lineStyle: { color: "#2b3832" } } },
    visualMap: { min: 0, max: model.max, calculable: true, orient: "horizontal", left: "center", bottom: 0, textStyle: { color: "#7f8d85", fontSize: 8 }, inRange: { color: ["#1a221e", "#7b5b22", "#e3a33d", "#f06a6a"] } },
    series: [{ type: "heatmap", data: model.services.flatMap((service, y) => model.times.map((time, x) => [x, y, model.values.get(`${service}\u0000${time}`) ?? 0])), emphasis: { itemStyle: { shadowBlur: 8, shadowColor: "rgba(0,0,0,.4)" } } }],
  };
  return <section className="heatmap-wrap"><EChart option={option} height={Math.max(280, model.services.length * 32 + 110)} label="Service P95 latency heatmap" /></section>;
}

function EndpointsView({ endpoints, onEndpoint }: { endpoints: Endpoint[]; onEndpoint: (endpoint: Endpoint) => void }) {
  if (endpoints.length === 0) return <EmptyState icon={<ArrowUpRight size={18} weight="duotone" />} title="No endpoints detected">HTTP routes and span operations will appear here as traffic arrives.</EmptyState>;
  return <section className="endpoint-list"><div className="endpoint-row endpoint-head"><span>Endpoint</span><span>Calls</span><span>P50</span><span>P95</span><span>P99</span><span>Errors</span></div>{endpoints.map((endpoint) => <button className="endpoint-row" key={`${endpoint.method}-${endpoint.path}`} onClick={() => onEndpoint(endpoint)}><span><i className={`method method-${endpoint.method.toLowerCase()}`}>{endpoint.method}</i><code>{endpoint.path}</code></span><span>{integer.format(endpoint.calls)}</span><span>{duration(endpoint.p50_ms)}</span><span>{duration(endpoint.p95_ms)}</span><span>{duration(endpoint.p99_ms)}</span><span className={`health-${endpoint.health}`}>{percent(endpoint.error_rate)}</span></button>)}</section>;
}

function ComparisonView({ data }: { data: Performance }) {
  if (data.comparison.length === 0) return <EmptyState icon={<ArrowsLeftRight size={18} weight="duotone" />} title="Nothing to compare yet">Fanout compares the first and second half of the selected window.</EmptyState>;
  return <section className="comparison"><div className="compare-head"><span>Signal</span><span>Earlier</span><span>Change</span><span>Recent</span></div>{data.comparison.map((metric) => <div className={`compare-row ${metric.direction}`} key={metric.label}><div><strong>{metric.label}</strong><small>{metric.unit}</small></div><strong>{formatMetric(metric.before, metric.unit)}</strong><span className="change"><b>{metric.change_pct > 0 ? "↑" : metric.change_pct < 0 ? "↓" : "→"} {Math.abs(metric.change_pct).toFixed(1)}%</b>{metric.significant && <small>notable</small>}</span><strong>{formatMetric(metric.after, metric.unit)}</strong></div>)}</section>;
}

function formatMetric(value: number, unit: string) {
  if (unit === "ms") return duration(value);
  if (unit === "%") return `${value.toFixed(2)}%`;
  return integer.format(value);
}

createRoot(document.getElementById("root")!).render(<StrictMode><PerformanceApp /></StrictMode>);
