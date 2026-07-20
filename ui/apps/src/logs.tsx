import { BarChart } from "echarts/charts";
import { StrictMode, useMemo, useState } from "react";
import { createRoot } from "react-dom/client";
import { EmptyState } from "./components";
import type { LogEntry, Logs, Result } from "./contracts";
import { EChart, useECharts } from "./echart";
import { windowLabel } from "./format";
import { askAbout, useFanoutApp } from "./use-fanout-app";
import "./app.css";
import "./logs.css";

useECharts([BarChart]);

function LogsApp() {
  const { app, callTool, error, host, result, toolError } = useFanoutApp<Result<Logs>>("Fanout log explorer");
  const [severity, setSeverity] = useState("ALL");
  const [search, setSearch] = useState("");
  const entries = useMemo(() => (result?.data.entries ?? []).filter((entry) => (severity === "ALL" || entry.severity.toUpperCase() === severity) && (!search || entry.body.toLowerCase().includes(search.toLowerCase()) || entry.service.toLowerCase().includes(search.toLowerCase()))), [result, search, severity]);
  return <main className={`app ${host?.theme === "dark" ? "dark" : ""}`}>
    <header className="header"><div><div className="eyebrow">Application activity</div><h1 className="title">Logs</h1>{result && <p className="summary">{result.data.entries.length} entries in this time range</p>}</div><button className="refresh" onClick={() => callTool("search_logs")} disabled={!app}>Refresh</button></header>
    {(error || toolError) && <div className="error">{toolError ?? "This view could not be loaded. Please try again."}</div>}
    {!result && !error && !toolError && <div className="loading">Searching logs…</div>}
    {result && result.data.entries.length === 0 && <><EmptyState icon="≡" title="No logs matched">Try a wider time window, a different service, or a less restrictive search.</EmptyState><footer className="meta"><span>{windowLabel(result.provenance.window)}</span><span>No entries found</span></footer></>}
    {result && result.data.entries.length > 0 && <>
      <LogHistogram data={result.data} />
      <section className="log-controls"><div className="severity-filter">{["ALL", "ERROR", "WARN", "INFO"].map((value) => <button key={value} className={severity === value ? "active" : ""} onClick={() => setSeverity(value)}>{value}</button>)}</div><input aria-label="Filter visible logs" type="search" value={search} onChange={(event) => setSearch(event.target.value)} placeholder="Filter visible logs…" /></section>
      <LogList entries={entries} onTrace={(entry) => askAbout(app, `Investigate trace ${entry.trace_id} related to this ${entry.severity} log from ${entry.service}.`)} />
      <footer className="meta"><span>{windowLabel(result.provenance.window)}</span><span>{entries.length} shown</span></footer>
    </>}
  </main>;
}

function LogHistogram({ data }: { data: Logs }) {
  const option = useMemo(() => {
    const times = [...new Set(data.buckets.map((bucket) => bucket.time))];
    const severities = [...new Set(data.buckets.map((bucket) => bucket.severity))];
    const values = new Map(data.buckets.map((bucket) => [`${bucket.time}\u0000${bucket.severity}`, bucket.count]));
    return { color: severities.map(severityColor), grid: { left: 38, right: 15, top: 25, bottom: 28 }, tooltip: { trigger: "axis", axisPointer: { type: "shadow" }, backgroundColor: "#1a221e", borderColor: "#2b3832", textStyle: { color: "#eff5f1", fontSize: 10 } }, legend: { top: 0, right: 0, textStyle: { color: "#9eaca5", fontSize: 9 }, itemWidth: 7, itemHeight: 7, icon: "circle" }, xAxis: { type: "category", data: times.map((time) => new Date(time).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })), axisLabel: { color: "#7f8d85", fontSize: 8, hideOverlap: true }, axisLine: { lineStyle: { color: "#2b3832" } } }, yAxis: { type: "value", minInterval: 1, splitLine: { lineStyle: { color: "#253029" } }, axisLabel: { color: "#7f8d85", fontSize: 8 } }, series: severities.map((severity) => ({ name: severity, type: "bar", stack: "logs", barMaxWidth: 22, data: times.map((time) => values.get(`${time}\u0000${severity}`) ?? 0), itemStyle: { borderRadius: [2, 2, 0, 0] } })) };
  }, [data.buckets]);
  return <section className="log-histogram"><EChart option={option} height={190} label="Log volume by severity over time" /></section>;
}

function LogList({ entries, onTrace }: { entries: LogEntry[]; onTrace: (entry: LogEntry) => void }) {
  if (entries.length === 0) return <EmptyState icon="⌕" title="No visible matches">Adjust the local severity or text filter.</EmptyState>;
  return <section className="log-list">{entries.map((entry, index) => <article key={`${entry.time}-${index}`}><time>{new Date(entry.time).toLocaleTimeString([], { hour: "numeric", minute: "2-digit", second: "2-digit" })}</time><span className={`severity severity-${entry.severity.toLowerCase()}`}>{entry.severity || "LOG"}</span><strong>{entry.service}</strong><p>{entry.body}</p>{entry.trace_id && <button onClick={() => onTrace(entry)}>Trace {entry.trace_id.slice(0, 8)}…</button>}</article>)}</section>;
}

function severityColor(value: string) { const severity = value.toUpperCase(); if (severity === "ERROR" || severity === "FATAL") return "#f06a6a"; if (severity === "WARN" || severity === "WARNING") return "#e3a33d"; if (severity === "INFO") return "#44c69a"; return "#7bdff2"; }

createRoot(document.getElementById("root")!).render(<StrictMode><LogsApp /></StrictMode>);
