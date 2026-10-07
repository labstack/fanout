import { act } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { usePanelResults } from "./use-panel-results";
import type { DashboardSpec, PanelResult } from "../../../panels/types";
import type { QueryBody } from "./api";
import { MantineProvider } from "@mantine/core";
import { PanelCard } from "./panel-card";
const wire = vi.hoisted(() => ({ panels: vi.fn(), annotations: vi.fn() }));
vi.mock("./api", async (importOriginal) => ({ ...await importOriginal<typeof import("./api")>(), queryPanels: wire.panels, queryAnnotations: wire.annotations }));
beforeEach(() => {
  wire.panels.mockReset();
  wire.annotations.mockReset();
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
});
afterEach(() => vi.unstubAllGlobals());
it("S8 integrates one annotation request into every 20-panel manual refresh", async () => {
  const spec: DashboardSpec = { version: 1, name: "S8", time: { range: "1h" }, panels: Array.from({ length: 20 }, (_, i) => ({ id: `p_${i}`, title: `P ${i}`, viz: "timeseries", query: { from: "spans", measures: ["count()"] } })) };
  wire.panels.mockResolvedValue(spec.panels.map(p => ({ id: p.id, status: "ok", elapsed_ms: 1, from_ms: 1000, to_ms: 2000, frame: { columns: [{ name: "time", type: "time", role: "time" }, { name: "count", type: "number", role: "measure" }], values: [[1000], [1]], rows: 1 } }))); wire.annotations.mockResolvedValue({ deploys: [], anomalies: [] });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } }); const node = document.createElement("div"); document.body.append(node); const root = createRoot(node); let current: ReturnType<typeof usePanelResults>;
  function Host() { current = usePanelResults({ dashboardId: "d", version: 1, spec, time: spec.time, vars: {}, compare: false, widths: {}, visible: spec.panels.map(p => p.id), refresh: "off" }); return null; }
  try {
    await act(async () => { root.render(<QueryClientProvider client={client}><Host /></QueryClientProvider>); });
    async function settled(expected: number) {
      const until = Date.now() + 5000;
      // HTTP mocks can finish before React Query's scheduled notification.
      do { await act(async () => { await new Promise(resolve => setTimeout(resolve, 10)); }); }
      while ((wire.annotations.mock.calls.length < expected || current!.fetching) && Date.now() < until);
      expect(current!.fetching).toBe(false); expect(wire.panels).toHaveBeenCalledTimes(expected); expect(wire.annotations).toHaveBeenCalledTimes(expected); expect(current!.results.size).toBe(20);
    }
    await settled(1); for (let i = 2; i <= 4; i++) { await act(async () => current!.refetch()); await settled(i); }
    wire.annotations.mockRejectedValueOnce(new Error("Unavailable")); await act(async () => current!.refetch()); await settled(5); expect(current!.annotationError).toBe("Unavailable"); expect(current!.results.get("p_0")?.status).toBe("ok");
  } finally {
    await act(async () => root.unmount()); client.clear(); node.remove();
  }
});

const visibilitySpec: DashboardSpec = {
  version: 1, name: "Visibility", time: { range: "1h" },
  panels: [
    ...["loaded", "a", "b", "c"].map(id => ({ id, title: id, viz: "stat" as const, query: { from: "spans" as const, measures: ["count()"] } })),
    { id: "note", title: "Note", viz: "text" },
  ],
};
const resultFor = (id: string): PanelResult => ({
  id, status: "ok", elapsed_ms: 1,
  frame: { columns: [{ name: "count", type: "number", role: "measure" }], values: [[1]], rows: 1 },
});
function deferred<T>() {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>(done => { resolve = done; });
  return { promise, resolve };
}
async function waitForHook(check: () => void) {
  await vi.waitFor(async () => {
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 5)); });
    check();
  });
}
async function mountVisibility(visible: string[], spec = visibilitySpec, renderPanels = false) {
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const node = document.createElement("div"); document.body.append(node);
  const root = createRoot(node);
  let current!: ReturnType<typeof usePanelResults>;
  function Host({ visible }: { visible: string[] }) {
    current = usePanelResults({ dashboardId: "d", version: 1, spec, time: spec.time, vars: {}, compare: false, widths: {}, visible, refresh: "off" });
    return renderPanels ? <MantineProvider>{spec.panels.filter(p => visible.includes(p.id)).map(panel => <PanelCard key={panel.id} panel={panel} title={panel.title} result={current.results.get(panel.id)} loading={current.fetchingIds.includes(panel.id)} height={200} group="g" editing={false} agentAvailable={false} onView={()=>{}} onInspect={()=>{}} onCopyLink={()=>{}} onExplain={()=>{}} />)}</MantineProvider> : null;
  }
  const show = async (visible: string[]) => {
    await act(async () => root.render(<QueryClientProvider client={client}><Host visible={visible} /></QueryClientProvider>));
  };
  await show(visible);
  return {
    get current() { return current; }, show, node,
    async dispose() { await act(async () => root.unmount()); client.clear(); node.remove(); },
  };
}

it("loads only missing visible panels after visibility changes during an in-flight batch", async () => {
  const a = deferred<PanelResult[]>(), b = deferred<PanelResult[]>();
  let active = 0, maxActive = 0;
  wire.panels.mockImplementation((body: QueryBody) => {
    active++; maxActive = Math.max(maxActive, active);
    const response = body.panels?.includes("a") ? a.promise : body.panels?.includes("b") ? b.promise : Promise.resolve([resultFor("loaded")]);
    return response.finally(() => { active--; });
  });
  const host = await mountVisibility(["loaded"]);
  try {
    await waitForHook(() => { expect(host.current.fetching).toBe(false); expect(host.current.results.has("loaded")).toBe(true); });
    await host.show(["a"]);
    await waitForHook(() => { expect(wire.panels).toHaveBeenCalledTimes(2); expect(host.current.fetchingIds).toEqual(["a"]); });
    // B is disjoint from A, but one of B's panels was loaded earlier under this key.
    const visibleB = ["c", "loaded", "b", "note", "unknown"];
    await host.show(visibleB);
    expect(wire.panels).toHaveBeenCalledTimes(2);
    expect(active).toBe(1);
    await act(async () => a.resolve([resultFor("a")]));
    await waitForHook(() => {
      expect(wire.panels).toHaveBeenCalledTimes(3);
      expect(wire.panels.mock.calls[2][0].panels).toEqual(["b", "c"]);
      expect(host.current.fetchingIds).toEqual(["b", "c"]);
      for (const id of ["c", "loaded", "b"]) {
        expect(host.current.results.has(id) || host.current.fetchingIds.includes(id)).toBe(true);
      }
    });
    expect(maxActive).toBe(1);
    await act(async () => b.resolve([resultFor("b"), resultFor("c")]));
    await waitForHook(() => {
      expect(host.current.fetching).toBe(false);
      for (const id of ["loaded", "a", "b", "c"]) expect(host.current.results.has(id)).toBe(true);
      expect(wire.panels).toHaveBeenCalledTimes(3);
    });
    expect(maxActive).toBe(1);
  } finally { await host.dispose(); }
});

it("keeps one batch in flight when manual refresh is repeated during a fetch", async () => {
  const pending = deferred<PanelResult[]>();
  wire.panels.mockResolvedValueOnce([resultFor("loaded")]).mockImplementation(() => pending.promise);
  const host = await mountVisibility(["loaded"]);
  try {
    await waitForHook(() => { expect(host.current.fetching).toBe(false); expect(host.current.results.has("loaded")).toBe(true); });
    await act(async () => host.current.refetch());
    await waitForHook(() => { expect(host.current.fetching).toBe(true); expect(wire.panels).toHaveBeenCalledTimes(2); });
    await act(async () => { host.current.refetch(); host.current.refetch(); });
    expect(wire.panels).toHaveBeenCalledTimes(2);
    await act(async () => pending.resolve([resultFor("loaded")]));
    await waitForHook(() => { expect(host.current.fetching).toBe(false); expect(wire.panels).toHaveBeenCalledTimes(2); });
  } finally { await host.dispose(); }
});

it("renders an error for an omitted result and allows a manual retry", async () => {
  const pending = deferred<PanelResult[]>();
  wire.panels.mockResolvedValueOnce([resultFor("loaded")]).mockImplementation(() => pending.promise);
  const host = await mountVisibility(["loaded", "a"], visibilitySpec, true);
  try {
    await waitForHook(() => {
      expect(host.current.fetching).toBe(false);
      expect(wire.panels).toHaveBeenCalledTimes(1);
      expect(host.current.results.has("loaded")).toBe(true);
      expect(host.current.results.get("a")).toMatchObject({status:"error",error:"No result was returned for this panel; refresh to retry."});
      expect(host.node.textContent).toContain("No result was returned for this panel; refresh to retry.");
      expect(host.node.querySelector('[aria-label="Loading panel"]')).toBeNull();
    });
    await act(async () => host.current.refetch());
    await waitForHook(() => { expect(host.current.fetching).toBe(true); expect(wire.panels).toHaveBeenCalledTimes(2); });
    expect(wire.panels.mock.calls[1][0].panels).toEqual(["a", "loaded"]);
    await act(async () => pending.resolve([resultFor("loaded"), resultFor("a")]));
    await waitForHook(() => { expect(host.current.fetching).toBe(false); expect(host.current.results.has("a")).toBe(true); });
    expect(wire.panels).toHaveBeenCalledTimes(2);
  } finally { await host.dispose(); }
});

it("keeps time-panel annotations when a lazy stat-only batch completes", async () => {
  const spec: DashboardSpec = {...visibilitySpec,panels:[...visibilitySpec.panels,{id:"trend",title:"Trend",viz:"timeseries",query:{from:"spans",measures:["count()"]}}]};
  const annotations = {deploys:[{namespace:"shop",service:"cart",version:"2.3.0",at:new Date(1000).toISOString()}],anomalies:[]};
  wire.annotations.mockResolvedValue(annotations);
  wire.panels.mockImplementation((body:QueryBody)=>Promise.resolve(body.panels!.map(id=>({...resultFor(id),from_ms:1000,to_ms:2000}))));
  const host = await mountVisibility(["trend"],spec);
  try {
    await waitForHook(()=>{expect(host.current.fetching).toBe(false);expect(host.current.annotations).toEqual(annotations);});
    await host.show(["loaded"]);
    await waitForHook(()=>{
      expect(host.current.fetching).toBe(false);
      expect(host.current.results.has("loaded")).toBe(true);
      expect(host.current.annotations).toEqual(annotations);
    });
    expect(wire.panels).toHaveBeenCalledTimes(2);
    expect(wire.panels.mock.calls[1][0].panels).toEqual(["loaded"]);
    expect(wire.annotations).toHaveBeenCalledTimes(1);
    const replacement = {deploys:[],anomalies:[]};
    wire.annotations.mockResolvedValue(replacement);
    await host.show(["trend"]);
    await act(async()=>host.current.refetch());
    await waitForHook(()=>{expect(host.current.fetching).toBe(false);expect(host.current.annotations).toEqual(replacement);});
    expect(wire.annotations).toHaveBeenCalledTimes(2);
  } finally { await host.dispose(); }
});

it("makes zero requests when scrolling between panels already loaded under the current key", async () => {
  wire.panels.mockImplementation((body: QueryBody) => Promise.resolve(body.panels!.map(resultFor)));
  const host = await mountVisibility(["loaded", "a", "b", "c"]);
  try {
    await waitForHook(() => { expect(host.current.fetching).toBe(false); expect(host.current.results.size).toBe(4); });
    expect(wire.panels).toHaveBeenCalledTimes(1);
    for (const visible of [["a"], ["c", "loaded"], ["b"], ["loaded", "a", "b", "c"]]) {
      await host.show(visible);
      await waitForHook(() => { expect(host.current.fetching).toBe(false); expect(host.current.results.size).toBe(4); });
      expect(wire.panels).toHaveBeenCalledTimes(1);
    }
  } finally { await host.dispose(); }
});

it("skips timed and manual refreshes after visibility becomes known empty", async () => {
 vi.useFakeTimers();
 wire.panels.mockResolvedValue([resultFor("loaded")]);
 const client = new QueryClient({defaultOptions:{queries:{retry:false}}});
 const node=document.createElement("div");document.body.append(node);const root=createRoot(node);
 let current!: ReturnType<typeof usePanelResults>;
 function Host({visible}:{visible:string[]}){current=usePanelResults({dashboardId:"empty",version:1,spec:visibilitySpec,time:visibilitySpec.time,vars:{},compare:false,widths:{},visible,refresh:"30s"});return null;}
 const show=async(visible:string[])=>act(async()=>{root.render(<QueryClientProvider client={client}><Host visible={visible}/></QueryClientProvider>);});
 try {
  await show(["loaded"]);
  await act(async()=>{await vi.advanceTimersByTimeAsync(100);});
  expect(current.results.has("loaded")).toBe(true);expect(wire.panels).toHaveBeenCalledTimes(1);
  await show([]);
  await act(async()=>{await vi.advanceTimersByTimeAsync(30100);});
  expect(wire.panels).toHaveBeenCalledTimes(1);
  await act(async()=>{current.refetch();await vi.advanceTimersByTimeAsync(100);});
  expect(wire.panels).toHaveBeenCalledTimes(1);
 }finally{await act(async()=>root.unmount());client.clear();node.remove();vi.useRealTimers();}
});

it("runs a manual refresh pressed during a partial lazy batch once that batch settles", async () => {
  const pending = deferred<PanelResult[]>();
  wire.panels.mockResolvedValueOnce([resultFor("loaded")]).mockImplementationOnce(() => pending.promise).mockImplementation((body: QueryBody) => Promise.resolve(body.panels!.map(resultFor)));
  const host = await mountVisibility(["loaded"]);
  try {
    await waitForHook(() => { expect(host.current.fetching).toBe(false); expect(host.current.results.has("loaded")).toBe(true); });
    await host.show(["loaded", "a"]);
    await waitForHook(() => { expect(host.current.fetching).toBe(true); expect(wire.panels).toHaveBeenCalledTimes(2); });
    expect(wire.panels.mock.calls[1][0].panels).toEqual(["a"]);
    await act(async () => { host.current.refetch(); host.current.refetch(); });
    expect(wire.panels).toHaveBeenCalledTimes(2);
    await act(async () => pending.resolve([resultFor("a")]));
    await waitForHook(() => { expect(wire.panels).toHaveBeenCalledTimes(3); expect(host.current.fetching).toBe(false); });
    expect(wire.panels.mock.calls[2][0].panels).toEqual(["a", "loaded"]);
  } finally { await host.dispose(); }
});
