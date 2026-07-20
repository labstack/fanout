import { StrictMode, useMemo, useState } from "react";
import type { CSSProperties } from "react";
import { createRoot } from "react-dom/client";
import { EmptyState, Hint, Tabs } from "./components";
import type { LogEntry, Result, TraceDetail, TraceSpan } from "./contracts";
import { duration, integer, windowLabel } from "./format";
import { askAbout, useFanoutApp } from "./use-fanout-app";
import "./app.css";
import "./trace.css";

type View = "waterfall" | "flame" | "logs";

function TraceApp() {
  const { app, callTool, error, host, result, toolError } = useFanoutApp<Result<TraceDetail>>("Fanout trace detail");
  const [view, setView] = useState<View>("waterfall");
  return <main className={`app ${host?.theme === "dark" ? "dark" : ""}`}>
    <header className="header"><div><div className="eyebrow">Request journey</div><h1 className="title">{result?.data.trace_id ? `Trace ${shortID(result.data.trace_id)}` : "Trace analysis"}</h1>{result && <p className="summary">{result.data.spans.length} spans across {result.data.services.length} services</p>}</div><button className="refresh" onClick={() => callTool("trace_detail")} disabled={!app}>Refresh</button></header>
    {(error || toolError) && <div className="error">{toolError ?? "This view could not be loaded. Please try again."}</div>}
    {!result && !error && !toolError && <div className="loading">Finding a representative trace…</div>}
    {result && result.data.spans.length === 0 && <><EmptyState icon="⌁" title="No traces in this window">Try a wider time window.</EmptyState><footer className="meta"><span>{windowLabel(result.provenance.window)}</span><span>No traces found</span></footer></>}
    {result && result.data.spans.length > 0 && <>
      <section className="trace-metrics"><div><span>Duration</span><strong>{duration(result.data.duration_ms)}</strong></div><div><span>Spans</span><strong>{integer.format(result.data.spans.length)}</strong></div><div><span>Services</span><strong>{integer.format(result.data.services.length)}</strong></div><div><span>Status</span><strong className={result.data.has_error ? "health-unhealthy" : "health-healthy"}>{result.data.has_error ? "Error" : "OK"}</strong></div></section>
      <Tabs active={view} onChange={setView} items={[{ id: "waterfall", label: "Waterfall", count: result.data.spans.length }, { id: "flame", label: "Flame graph" }, { id: "logs", label: "Correlated logs", count: result.data.logs.length }]} />
      {view === "waterfall" && <Waterfall spans={result.data.spans} onSpan={(span) => askAbout(app, `Investigate span ${span.span_id} (${span.service} ${span.operation}) in trace ${result.data.trace_id}.`)} />}
      {view === "flame" && <FlameGraph spans={result.data.spans} onSpan={(span) => askAbout(app, `Investigate span ${span.span_id} (${span.service} ${span.operation}) in trace ${result.data.trace_id}.`)} />}
      {view === "logs" && <TraceLogs entries={result.data.logs} />}
      <footer className="meta"><span>{windowLabel(result.provenance.window)}</span><span>Trace {shortID(result.data.trace_id)}</span></footer>
    </>}
  </main>;
}

function Waterfall({ spans, onSpan }: { spans: TraceSpan[]; onSpan: (span: TraceSpan) => void }) {
  const start = Math.min(...spans.map((span) => new Date(span.start).valueOf()));
  const end = Math.max(...spans.map((span) => new Date(span.start).valueOf() + span.duration_ms));
  const total = Math.max(end - start, 1);
  return <section className="waterfall"><div className="waterfall-head"><span>Operation</span><span>Timeline</span><span>Duration</span></div>{spans.map((span) => { const offset = (new Date(span.start).valueOf() - start) / total * 100; const width = Math.max(span.duration_ms / total * 100, .6); const failed = span.status.toUpperCase().includes("ERROR"); return <button className="waterfall-row" key={span.span_id} onClick={() => onSpan(span)}><span className="span-name"><i style={{ background: serviceColor(span.service) }} /><span><strong>{span.operation}</strong><small>{span.service}</small></span></span><span className="span-track"><Hint label={`${span.service} · ${span.operation} · ${duration(span.duration_ms)}`}><i className={`span-bar ${failed ? "failed" : ""}`} style={{ left: `${offset}%`, width: `${Math.min(width, 100 - offset)}%`, background: failed ? undefined : serviceColor(span.service) }} /></Hint></span><span>{duration(span.duration_ms)}</span></button>; })}</section>;
}

function FlameGraph({ spans, onSpan }: { spans: TraceSpan[]; onSpan: (span: TraceSpan) => void }) {
  const model = useMemo(() => flameModel(spans), [spans]);
  const services = [...new Set(spans.map((span) => span.service))];
  return <section className="flame-wrap">
    <div className="flame-toolbar">
      <div className="service-legend" aria-label="Services">{services.map((service) => <span key={service}><i style={{ background: serviceColor(service) }} />{service}</span>)}</div>
      <span className="flame-total">{duration(model.total)}</span>
    </div>
    <div className="flame-shell">
      <div className="flame-axis" aria-hidden="true">{[0, 25, 50, 75, 100].map((position) => <span key={position} style={{ left: `${position}%` }}>{duration(model.total * position / 100)}</span>)}</div>
      <div className="flame-scroll">
        <div className="flame" style={{ height: Math.max(150, model.laneCount * 34 + 12) }}>
          {[0, 25, 50, 75, 100].map((position) => <i className="flame-gridline" key={position} style={{ left: `${position}%` }} />)}
          {model.frames.map(({ span, lane, left, width }) => {
            const failed = span.status.toUpperCase().includes("ERROR");
            const size = width < 2.5 ? "tick" : width < 7 ? "compact" : "full";
            return <Hint key={span.span_id} label={`${span.service} · ${span.operation} · ${duration(span.duration_ms)}`}>
              <button
                className={`flame-frame ${failed ? "failed" : ""}`}
                data-size={size}
                onClick={() => onSpan(span)}
                style={{ left: `${left}%`, width: `${Math.max(width, .25)}%`, top: `${lane * 34 + 6}px`, "--service-color": serviceColor(span.service) } as CSSProperties}
              >
                <strong>{span.operation}</strong><small>{duration(span.duration_ms)}</small>
              </button>
            </Hint>;
          })}
        </div>
      </div>
    </div>
  </section>;
}

function flameModel(spans: TraceSpan[]) {
  const start = Math.min(...spans.map((span) => new Date(span.start).valueOf()));
  const end = Math.max(...spans.map((span) => new Date(span.start).valueOf() + span.duration_ms));
  const total = Math.max(end - start, 1);
  const byID = new Map(spans.map((span) => [span.span_id, span]));
  const depth = (span: TraceSpan, seen = new Set<string>()): number => {
    if (!span.parent_span_id || seen.has(span.span_id)) return 0;
    const parent = byID.get(span.parent_span_id);
    if (!parent) return 0;
    seen.add(span.span_id);
    return 1 + depth(parent, seen);
  };
  const raw = spans.map((span) => {
    const spanStart = new Date(span.start).valueOf();
    return { span, depth: depth(span), start: spanStart, end: spanStart + span.duration_ms, left: (spanStart - start) / total * 100, width: span.duration_ms / total * 100 };
  }).sort((a, b) => a.depth - b.depth || a.start - b.start || b.span.duration_ms - a.span.duration_ms);

  const frames: Array<(typeof raw)[number] & { lane: number }> = [];
  let laneOffset = 0;
  for (const currentDepth of [...new Set(raw.map((frame) => frame.depth))].sort((a, b) => a - b)) {
    const laneEnds: number[] = [];
    for (const frame of raw.filter((item) => item.depth === currentDepth)) {
      let localLane = laneEnds.findIndex((laneEnd) => laneEnd <= frame.start);
      if (localLane === -1) localLane = laneEnds.length;
      laneEnds[localLane] = frame.end;
      frames.push({ ...frame, lane: laneOffset + localLane });
    }
    laneOffset += Math.max(laneEnds.length, 1);
  }
  return { frames, laneCount: laneOffset, total };
}

function TraceLogs({ entries }: { entries: LogEntry[] }) {
  if (entries.length === 0) return <EmptyState icon="≡" title="No correlated logs">No logs in this window carry the selected trace ID.</EmptyState>;
  return <section className="trace-logs">{entries.map((entry, index) => <article key={`${entry.time}-${index}`}><time>{new Date(entry.time).toLocaleTimeString([], { hour: "numeric", minute: "2-digit", second: "2-digit" })}</time><span className={`severity severity-${entry.severity.toLowerCase()}`}>{entry.severity || "LOG"}</span><strong>{entry.service}</strong><p>{entry.body}</p></article>)}</section>;
}

function shortID(value: string) { return value.length > 12 ? `${value.slice(0, 8)}…${value.slice(-4)}` : value; }
function serviceColor(service: string) { const colors = ["#54c79b", "#7bdff2", "#e3a33d", "#b8a1ff", "#f28fad", "#8bd450"]; let hash = 0; for (const char of service) hash = (hash * 31 + char.charCodeAt(0)) | 0; return colors[Math.abs(hash) % colors.length]; }

createRoot(document.getElementById("root")!).render(<StrictMode><TraceApp /></StrictMode>);
