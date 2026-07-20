import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import type { Overview, Result, ServiceHealth } from "./contracts";
import { duration, integer, percent, windowLabel } from "./format";
import { askAbout, useFanoutApp } from "./use-fanout-app";
import "./app.css";
import "./overview.css";

function OverviewApp() {
  const { app, callTool, error, host, result, toolError } = useFanoutApp<Result<Overview>>("Fanout system health");
  const dark = host?.theme === "dark";

  async function refresh() {
    await callTool("observability_overview");
  }

  return (
    <main className={`app ${dark ? "dark" : ""}`}>
      <header className="header">
        <div>
          <div className="eyebrow">Live system view</div>
          <h1 className="title">System health</h1>
          {result && <p className="summary">{result.summary}</p>}
        </div>
        <button className="refresh" onClick={refresh} disabled={!app}>Refresh</button>
      </header>
      {(error || toolError) && <div className="error">{toolError ?? "This view could not be loaded. Please try again."}</div>}
      {!result && !error && !toolError && <div className="loading">Loading system health…</div>}
      {result && <OverviewBody result={result} onService={(service) => askAbout(app, `Investigate the ${service} service. Explain its errors and latency.`)} />}
    </main>
  );
}

function OverviewBody({ result, onService }: { result: Result<Overview>; onService: (service: string) => void }) {
  const { data } = result;
  const total = Math.max(data.service_count, 1);
  return <>
    <section className="metrics">
      <Metric label="Services" value={integer.format(data.service_count)} />
      <Metric label="Operations" value={integer.format(data.total_spans)} />
      <Metric label="Error rate" value={percent(data.error_rate)} tone={data.health} />
    </section>
    <section className="health-strip" aria-label="Service health distribution">
      <span className="segment healthy" style={{ flex: data.counts.healthy / total }} />
      <span className="segment degraded" style={{ flex: data.counts.degraded / total }} />
      <span className="segment unhealthy" style={{ flex: data.counts.unhealthy / total }} />
    </section>
    <div className="legend">
      <span><i className="legend-dot healthy" />{data.counts.healthy} healthy</span><span><i className="legend-dot degraded" />{data.counts.degraded} degraded</span><span><i className="legend-dot unhealthy" />{data.counts.unhealthy} unhealthy</span>
    </div>
    {data.services.length === 0 ? <section className="empty-state">
      <span className="empty-icon" aria-hidden="true">⌁</span>
      <div><strong>No activity in this window</strong><p>Services will appear as data begins to arrive.</p></div>
    </section> : <section className="service-list">
      <div className="service-row service-head"><span>Service</span><span>Traffic</span><span>P95</span><span>Errors</span></div>
      {data.services.map((service) => <ServiceRow key={service.service} service={service} onClick={() => onService(service.service)} />)}
    </section>}
    <footer className="meta"><span>{windowLabel(result.provenance.window)}</span><span>Updated {new Date(result.provenance.generated_at).toLocaleTimeString([], { hour: "numeric", minute: "2-digit" })}</span></footer>
  </>;
}

function Metric({ label, value, tone }: { label: string; value: string; tone?: string }) {
  return <div className="metric"><span>{label}</span><strong className={tone ? `health-${tone}` : ""}>{value}</strong></div>;
}

function ServiceRow({ service, onClick }: { service: ServiceHealth; onClick: () => void }) {
  return <button className="service-row service-button" onClick={onClick}>
    <span className={`badge health-${service.health}`}><i className="dot" />{service.service}</span>
    <span>{integer.format(service.spans)}</span><span>{duration(service.p95_ms)}</span><span>{percent(service.error_rate)}</span>
  </button>;
}

createRoot(document.getElementById("root")!).render(<StrictMode><OverviewApp /></StrictMode>);
