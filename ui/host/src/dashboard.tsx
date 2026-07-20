import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Responsive, WidthProvider } from "react-grid-layout/legacy";
import { useEffect, useMemo, useState } from "react";
import { authorizedFetch } from "./auth";

const Grid = WidthProvider(Responsive);
type Widget = { id: string; type: "overview" | "topology" | "activity" | "assistant"; title: string; enabled: boolean };
type State = { layout: Array<{ i: string; x: number; y: number; w: number; h: number; minW?: number; minH?: number }>; widgets: Widget[]; filters: { window: string; namespace: string } };
type Envelope<T> = { data: T };

async function getJSON<T>(url: string): Promise<T> {
  const response = await authorizedFetch(url);
  if (!response.ok) throw new Error(`Request failed (${response.status})`);
  return response.json();
}

const emptyState: State = { layout: [], widgets: [], filters: { window: "1h", namespace: "" } };

export default function Dashboard({ onOpenChat }: { onOpenChat: (prompt?: string) => void }) {
  const queryClient = useQueryClient();
  const dashboard = useQuery({ queryKey: ["dashboard"], queryFn: () => getJSON<{ state: State }>("/api/dashboard"), staleTime: Infinity });
  const [state, setState] = useState<State>(emptyState);
  const [saving, setSaving] = useState(false);
  useEffect(() => { if (dashboard.data?.state) setState(dashboard.data.state); }, [dashboard.data]);
  const query = new URLSearchParams({ window: state.filters.window, limit: "40" });
  if (state.filters.namespace) query.set("namespace", state.filters.namespace);
  const overview = useQuery({ queryKey: ["overview", query.toString()], queryFn: () => getJSON<Envelope<any>>(`/api/observability/overview?${query}`), enabled: state.widgets.some((w) => w.type === "overview" || w.type === "activity") });
  const topology = useQuery({ queryKey: ["topology", query.toString()], queryFn: () => getJSON<Envelope<any>>(`/api/observability/topology?${query}`), enabled: state.widgets.some((w) => w.type === "topology") });
  const save = useMutation({ mutationFn: async (next: State) => { const r = await authorizedFetch("/api/dashboard", { method: "PUT", headers: { "content-type": "application/json" }, body: JSON.stringify(next) }); if (!r.ok) throw new Error("Unable to save dashboard"); return r.json(); }, onSuccess: (data) => queryClient.setQueryData(["dashboard"], data) });
  const layouts = useMemo<any>(() => ({ lg: state.layout, md: state.layout, sm: state.layout, xs: state.layout, xxs: state.layout }), [state.layout]);
  function update(next: State) { setState(next); setSaving(true); save.mutate(next, { onSettled: () => setSaving(false) }); }
  function add(type: Widget["type"]) { const id = `${type}-${crypto.randomUUID().slice(0, 6)}`; const title = type === "overview" ? "System health" : type === "topology" ? "Service map" : type === "activity" ? "Recent activity" : "Ask Fanout"; update({ ...state, widgets: [...state.widgets, { id, type, title, enabled: true }], layout: [...state.layout, { i: id, x: 0, y: Infinity, w: type === "topology" ? 8 : 4, h: type === "assistant" ? 2 : 3, minW: 3, minH: 2 }] }); }
  function remove(id: string) { update({ ...state, widgets: state.widgets.filter((w) => w.id !== id), layout: state.layout.filter((l) => l.i !== id) }); }
  if (dashboard.isLoading) return <main className="dashboard-loading">Loading your workspace…</main>;
  if (dashboard.isError) return <main className="dashboard-loading">Your workspace is unavailable. Try refreshing.</main>;
  return <main className="dashboard">
    <div className="dashboard-heading"><div><p className="eyebrow">WORKSPACE</p><h1>System overview</h1><p>Monitor the signals that matter and open a conversation when you need context.</p></div><button className="primary" onClick={() => void queryClient.invalidateQueries()}>{overview.isFetching || topology.isFetching ? "Refreshing…" : "Refresh"}</button></div>
    <div className="dashboard-toolbar"><label>Window<select value={state.filters.window} onChange={(e) => update({ ...state, filters: { ...state.filters, window: e.target.value } })}><option value="15m">15 minutes</option><option value="1h">1 hour</option><option value="6h">6 hours</option><option value="24h">24 hours</option></select></label><label>Namespace<input value={state.filters.namespace} onChange={(e) => setState({ ...state, filters: { ...state.filters, namespace: e.target.value } })} onBlur={() => update(state)} placeholder="All namespaces" /></label><span className="dashboard-save">{saving ? "Saving…" : "Saved"}</span><div className="widget-actions"><button className="ghost" onClick={() => add("overview")}>Add view</button><button className="ghost quiet" onClick={() => onOpenChat()}>Ask Fanout</button></div></div>
    <Grid className="dashboard-grid" layouts={layouts} breakpoints={{ lg: 1100, md: 800, sm: 600, xs: 420, xxs: 0 }} cols={{ lg: 12, md: 10, sm: 6, xs: 2, xxs: 1 }} rowHeight={76} margin={[16, 16]} compactType="vertical" onLayoutChange={(layout: any) => { const byID = new Map(layout.map((l: any) => [l.i, l])); const next = state.layout.map((item) => ({ ...item, ...(byID.get(item.i) ?? {}) })); if (next.some((item, index) => JSON.stringify(item) !== JSON.stringify(state.layout[index]))) update({ ...state, layout: next }); }}>
      {state.widgets.map((widget) => <div key={widget.id}><WidgetCard widget={widget} overview={overview.data?.data} topology={topology.data?.data} onRemove={() => remove(widget.id)} onOpenChat={onOpenChat} /></div>)}
    </Grid>
  </main>;
}

function WidgetCard({ widget, overview, topology, onRemove, onOpenChat }: { widget: Widget; overview?: any; topology?: any; onRemove: () => void; onOpenChat: (prompt?: string) => void }) {
  return <section className="dashboard-card"><div className="card-heading"><div><span className="card-kicker">{widget.type === "assistant" ? "GUIDANCE" : widget.type.toUpperCase()}</span><h2>{widget.title}</h2></div><button className="icon-button" aria-label={`Remove ${widget.title}`} onClick={onRemove}>×</button></div>{widget.type === "overview" && <div className="metric-grid"><Metric label="Health" value={overview?.health ?? "—"} /><Metric label="Services" value={overview?.service_count ?? "—"} /><Metric label="Spans" value={overview?.total_spans?.toLocaleString?.() ?? "—"} /><Metric label="Error rate" value={overview ? `${(overview.error_rate * 100).toFixed(2)}%` : "—"} /></div>}{widget.type === "topology" && <div className="service-list">{(topology?.nodes ?? []).slice(0, 6).map((node: any) => <div className="service-row" key={node.service}><span className={`status-dot ${node.health}`} /><strong>{node.service}</strong><span>{node.spans?.toLocaleString?.() ?? 0} spans</span><small>{node.p95_ms?.toFixed?.(1) ?? "—"} ms p95</small></div>)}{!topology?.nodes?.length && <Empty text="No service relationships in this window" />}</div>}{widget.type === "activity" && <div className="activity-list">{(overview?.services ?? []).slice(0, 5).map((service: any) => <div className="activity-row" key={service.service}><span className={`status-dot ${service.health}`} /><span>{service.service}</span><small>{service.error_rate ? `${(service.error_rate * 100).toFixed(2)}% errors` : "Operating normally"}</small></div>)}{!overview?.services?.length && <Empty text="No recent activity" />}</div>}{widget.type === "assistant" && <div className="assistant-card"><p>Ask a focused question about health, latency, errors, or dependencies.</p><button className="primary" onClick={() => onOpenChat("Summarize the most important system changes in the selected window")}>Start a conversation <span>↗</span></button></div>}</section>;
}
function Metric({ label, value }: { label: string; value: string | number }) { return <div className="metric"><small>{label}</small><strong>{value}</strong></div>; }
function Empty({ text }: { text: string }) { return <div className="empty-state">{text}</div>; }
