import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { ArrowClockwise, ArrowUpRight, ListMagnifyingGlass, Plus, Sparkle, SquaresFour, X } from "@phosphor-icons/react";
import { Responsive, WidthProvider } from "react-grid-layout/legacy";
import { useEffect, useMemo, useState } from "react";
import { authorizedFetch } from "./auth";
import { createID } from "./id";

const Grid = WidthProvider(Responsive);
const dashboardKey = "fanout.dashboard-id";
type WidgetType = "overview" | "topology" | "activity" | "assistant" | "performance" | "trace" | "logs";
type Widget = { id: string; type: WidgetType; title: string; config?: Record<string, unknown>; enabled: boolean };
type State = { layout: Array<{ i: string; x: number; y: number; w: number; h: number; minW?: number; minH?: number }>; widgets: Widget[]; filters: { window: string; namespace: string } };
type DashboardRecord = { id: string; name: string; description: string; is_default: boolean; state: State; updated_at: string };
type DashboardSummary = { id: string; name: string; description: string; is_default: boolean; widget_count: number; updated_at: string };
type Envelope<T> = { data: T };

async function getJSON<T>(url: string): Promise<T> {
  const response = await authorizedFetch(url);
  if (!response.ok) throw new Error(`Request failed (${response.status})`);
  return response.json();
}

const emptyState: State = { layout: [], widgets: [], filters: { window: "1h", namespace: "" } };
const widgetTitles: Record<WidgetType, string> = { overview: "System health", topology: "Service map", activity: "Recent activity", assistant: "Ask Fanout", performance: "Performance", trace: "Trace focus", logs: "Logs" };

export default function Dashboard({ onOpenChat }: { onOpenChat: (prompt?: string) => void }) {
  const queryClient = useQueryClient();
  const dashboards = useQuery({ queryKey: ["dashboards"], queryFn: () => getJSON<{ dashboards: DashboardSummary[] }>("/api/dashboards"), refetchInterval: 3000 });
  const [selectedID, setSelectedID] = useState(() => localStorage.getItem(dashboardKey) ?? "");
  const selected = useQuery({ queryKey: ["dashboard", selectedID], queryFn: () => getJSON<DashboardRecord>(`/api/dashboards/${encodeURIComponent(selectedID)}`), enabled: Boolean(selectedID), refetchInterval: 3000 });
  const [state, setState] = useState<State>(emptyState);
  const [breakpoint, setBreakpoint] = useState("lg");

  useEffect(() => {
    const items = dashboards.data?.dashboards;
    if (!items?.length) return;
    if (!items.some((item) => item.id === selectedID)) {
      const next = items.find((item) => item.is_default) ?? items[0];
      setSelectedID(next.id);
      localStorage.setItem(dashboardKey, next.id);
    }
  }, [dashboards.data, selectedID]);
  useEffect(() => { if (selected.data?.state) setState(selected.data.state); }, [selected.data?.updated_at]);

  const save = useMutation({
    mutationFn: async (next: State) => {
      if (!selected.data) throw new Error("No dashboard selected");
      const response = await authorizedFetch(`/api/dashboards/${encodeURIComponent(selected.data.id)}`, { method: "PUT", headers: { "content-type": "application/json" }, body: JSON.stringify({ name: selected.data.name, description: selected.data.description, state: next }) });
      if (!response.ok) throw new Error("Unable to save dashboard");
      return response.json() as Promise<DashboardRecord>;
    },
    scope: { id: `dashboard-${selectedID}` },
    onSuccess: (data) => {
      queryClient.setQueryData(["dashboard", data.id], data);
      void queryClient.invalidateQueries({ queryKey: ["dashboards"] });
    },
  });
  const layouts = useMemo<any>(() => ({ lg: state.layout }), [state.layout]);
  function choose(id: string) { setSelectedID(id); localStorage.setItem(dashboardKey, id); }
  function update(next: State) { setState(next); save.mutate(next); }
  function add(type: WidgetType) {
    const id = `${type}-${createID().slice(0, 6)}`;
    const wide = type === "topology" || type === "performance" || type === "trace" || type === "logs";
    update({ ...state, widgets: [...state.widgets, { id, type, title: widgetTitles[type], enabled: true }], layout: [...state.layout, { i: id, x: 0, y: Infinity, w: wide ? 8 : 4, h: type === "assistant" ? 2 : wide ? 4 : 3, minW: 3, minH: 2 }] });
  }
  function remove(id: string) { update({ ...state, widgets: state.widgets.filter((widget) => widget.id !== id), layout: state.layout.filter((item) => item.i !== id) }); }

  if (dashboards.isLoading || (selectedID && selected.isLoading)) return <main className="dashboard-loading">Loading your workspace…</main>;
  if (dashboards.isError || selected.isError) return <main className="dashboard-loading">Your workspace is unavailable. Try refreshing.</main>;
  const item = selected.data;
  if (!item) return <main className="dashboard-loading">Preparing your workspace…</main>;

  return <main className="dashboard">
    <div className="dashboard-heading">
      <div className="dashboard-title"><label className="dashboard-picker"><SquaresFour size={16} weight="fill" aria-hidden="true" /><span className="sr-only">Dashboard</span><select aria-label="Dashboard" value={selectedID} onChange={(event) => choose(event.target.value)}>{dashboards.data?.dashboards.map((dashboard) => <option key={dashboard.id} value={dashboard.id}>{dashboard.name}</option>)}</select></label><h1>{item.name}</h1><p>{item.description || "A focused view of the signals that matter now."}</p></div>
      <div className="dashboard-heading-actions"><button className="ghost ai-dashboard" onClick={() => onOpenChat("Create a new dashboard for me. First ask what I want to monitor, then design it when you have enough context.")}><Sparkle size={16} weight="fill" aria-hidden="true" />Create with AI</button><button className="primary" onClick={() => void queryClient.invalidateQueries()}><ArrowClockwise size={16} weight="bold" aria-hidden="true" className={selected.isFetching ? "button-icon spinning" : "button-icon"} />{selected.isFetching ? "Refreshing" : "Refresh"}</button></div>
    </div>
    <div className="dashboard-toolbar"><label>Window<select value={state.filters.window} onChange={(event) => update({ ...state, filters: { ...state.filters, window: event.target.value } })}><option value="15m">15 minutes</option><option value="1h">1 hour</option><option value="6h">6 hours</option><option value="24h">24 hours</option></select></label><label>Namespace<input value={state.filters.namespace} onChange={(event) => setState({ ...state, filters: { ...state.filters, namespace: event.target.value } })} onBlur={() => update(state)} placeholder="All namespaces" /></label><span className="dashboard-save"><i />{save.isPending ? "Saving" : save.isError ? "Save failed" : "Saved"}</span><div className="widget-actions"><label className="add-widget"><Plus size={15} weight="bold" aria-hidden="true" /><span className="sr-only">Add view</span><select aria-label="Add view" value="" onChange={(event) => { if (event.target.value) add(event.target.value as WidgetType); }}><option value="">Add view</option>{Object.entries(widgetTitles).map(([type, title]) => <option key={type} value={type}>{title}</option>)}</select></label><button className="ghost quiet" onClick={() => onOpenChat()}>Ask Fanout<ArrowUpRight size={15} weight="bold" aria-hidden="true" /></button></div></div>
    <Grid className="dashboard-grid" layouts={layouts} breakpoints={{ lg: 1100, md: 800, sm: 600, xs: 420, xxs: 0 }} cols={{ lg: 12, md: 10, sm: 6, xs: 2, xxs: 1 }} rowHeight={76} margin={[16, 16]} containerPadding={[0, 0]} compactType="vertical" draggableCancel="button,input,select,textarea,a,label" onBreakpointChange={setBreakpoint} onDragStop={(layout: any) => { if (breakpoint === "lg") update({ ...state, layout }); }} onResizeStop={(layout: any) => { if (breakpoint === "lg") update({ ...state, layout }); }}>
      {state.widgets.map((widget) => <div key={widget.id}><WidgetCard widget={widget} filters={state.filters} onRemove={() => remove(widget.id)} onOpenChat={onOpenChat} /></div>)}
    </Grid>
  </main>;
}

function WidgetCard({ widget, filters, onRemove, onOpenChat }: { widget: Widget; filters: State["filters"]; onRemove: () => void; onOpenChat: (prompt?: string) => void }) {
  const base = new URLSearchParams({ window: filters.window, limit: "40" });
  if (filters.namespace) base.set("namespace", filters.namespace);
  const service = typeof widget.config?.service === "string" ? widget.config.service : "";
  if (service) base.set("service", service);
  const overview = useQuery({ queryKey: ["overview", base.toString()], queryFn: () => getJSON<Envelope<any>>(`/api/observability/overview?${base}`), enabled: widget.type === "overview" || widget.type === "activity" });
  const topology = useQuery({ queryKey: ["topology", base.toString()], queryFn: () => getJSON<Envelope<any>>(`/api/observability/topology?${base}`), enabled: widget.type === "topology" });
  const performance = useQuery({ queryKey: ["performance", base.toString()], queryFn: () => getJSON<Envelope<any>>(`/api/observability/performance?${base}`), enabled: widget.type === "performance" });
  const logQuery = new URLSearchParams(base); if (typeof widget.config?.severity === "string") logQuery.set("severity", widget.config.severity); if (typeof widget.config?.search === "string") logQuery.set("search", widget.config.search);
  const logs = useQuery({ queryKey: ["logs", logQuery.toString()], queryFn: () => getJSON<Envelope<any>>(`/api/observability/logs?${logQuery}`), enabled: widget.type === "logs" });
  const traceQuery = new URLSearchParams(base); if (typeof widget.config?.trace_id === "string") traceQuery.set("trace_id", widget.config.trace_id);
  const trace = useQuery({ queryKey: ["trace", traceQuery.toString()], queryFn: () => getJSON<Envelope<any>>(`/api/observability/trace?${traceQuery}`), enabled: widget.type === "trace" });
  const health = overview.data?.data;
  return <section className="dashboard-card"><div className="card-heading"><div><span className="card-kicker">{widget.type === "assistant" ? "GUIDANCE" : widget.type.toUpperCase()}</span><h2>{widget.title}</h2></div><button className="icon-button" title={`Remove ${widget.title}`} aria-label={`Remove ${widget.title}`} onClick={onRemove}><X size={16} weight="bold" aria-hidden="true" /></button></div>
    {widget.type === "overview" && <div className="metric-grid"><Metric label="Health" value={health?.health ?? "—"} /><Metric label="Services" value={health?.service_count ?? "—"} /><Metric label="Spans" value={health?.total_spans?.toLocaleString?.() ?? "—"} /><Metric label="Error rate" value={health ? `${(health.error_rate * 100).toFixed(2)}%` : "—"} /></div>}
    {widget.type === "topology" && <div className="service-list">{(topology.data?.data?.nodes ?? []).slice(0, 6).map((node: any) => <div className="service-row" key={node.service}><span className={`status-dot ${node.health}`} /><strong>{node.service}</strong><span>{node.spans?.toLocaleString?.() ?? 0} spans</span><small>{node.p95_ms?.toFixed?.(1) ?? "—"} ms p95</small></div>)}{!topology.data?.data?.nodes?.length && <Empty text="No service relationships in this window" />}</div>}
    {widget.type === "activity" && <div className="activity-list">{(health?.services ?? []).slice(0, 5).map((entry: any) => <div className="activity-row" key={entry.service}><span className={`status-dot ${entry.health}`} /><span>{entry.service}</span><small>{entry.error_rate ? `${(entry.error_rate * 100).toFixed(2)}% errors` : "Operating normally"}</small></div>)}{!health?.services?.length && <Empty text="No recent activity" />}</div>}
    {widget.type === "performance" && <div className="compact-table">{(performance.data?.data?.endpoints ?? []).slice(0, 5).map((endpoint: any) => <div key={`${endpoint.method}-${endpoint.path}`}><strong>{endpoint.method} {endpoint.path}</strong><span>{endpoint.calls?.toLocaleString?.()} calls</span><small>{endpoint.p95_ms?.toFixed?.(1)} ms p95</small></div>)}{!performance.data?.data?.endpoints?.length && <Empty text="No endpoint activity in this window" />}</div>}
    {widget.type === "logs" && <div className="compact-table logs-preview">{(logs.data?.data?.entries ?? []).slice(0, 5).map((entry: any, index: number) => <div key={`${entry.time}-${index}`}><strong className={`severity-${String(entry.severity).toLowerCase()}`}>{entry.severity}</strong><span>{entry.service}</span><small>{entry.body}</small></div>)}{!logs.data?.data?.entries?.length && <Empty text="No matching logs in this window" />}</div>}
    {widget.type === "trace" && <div className="trace-preview"><div className="metric-grid"><Metric label="Duration" value={trace.data?.data ? `${trace.data.data.duration_ms.toFixed?.(1)} ms` : "—"} /><Metric label="Spans" value={trace.data?.data?.spans?.length ?? "—"} /><Metric label="Services" value={trace.data?.data?.services?.length ?? "—"} /><Metric label="Status" value={trace.data?.data ? trace.data.data.has_error ? "Error" : "Healthy" : "—"} /></div><small>{trace.data?.data?.trace_id ? `Trace ${trace.data.data.trace_id.slice(0, 12)}…` : "Most relevant recent trace"}</small></div>}
    {widget.type === "assistant" && <div className="assistant-card"><p>Ask a focused question about health, latency, errors, or dependencies.</p><button className="primary" onClick={() => onOpenChat("Summarize the most important system changes in the selected window")}><Sparkle size={16} weight="fill" aria-hidden="true" />Start a conversation</button></div>}
  </section>;
}
function Metric({ label, value }: { label: string; value: string | number }) { return <div className="metric"><small>{label}</small><strong>{value}</strong></div>; }
function Empty({ text }: { text: string }) { return <div className="empty-state"><ListMagnifyingGlass size={20} aria-hidden="true" /><span>{text}</span></div>; }
