import { act } from "react";
import { within } from "@testing-library/dom";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { usePanelResults } from "./use-panel-results";
import type { DashboardSpec, PanelResult } from "../../../panels/types";
import type { QueryBody } from "./api";
import { MantineProvider } from "@mantine/core";
import { ApiError } from "./api";
import { PanelCard } from "./panel-card";
const wire = vi.hoisted(() => ({ panels: vi.fn(), annotations: vi.fn() }));
vi.mock("./api", async (importOriginal) => ({ ...await importOriginal<typeof import("./api")>(), queryPanels: wire.panels, queryAnnotations: wire.annotations }));
beforeEach(() => {
  wire.panels.mockReset();
  wire.annotations.mockReset();
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
});
afterEach(() => {vi.unstubAllGlobals(); document.body.innerHTML = "";});
it("S8 integrates one annotation request into every 20-panel manual refresh", async () => {
  const spec: DashboardSpec = { version: 1, name: "S8", time: { range: "1h" }, panels: Array.from({ length: 20 }, (_, i) => ({ id: `p_${i}`, title: `P ${i}`, viz: "timeseries", query: { from: "spans", measures: ["count()"] } })) };
  wire.panels.mockResolvedValue(spec.panels.map(p => ({ id: p.id, status: "ok", elapsed_ms: 1, from_ms: 1000, to_ms: 2000, frame: { columns: [{ name: "time", type: "time", role: "time" }, { name: "count", type: "number", role: "measure" }], values: [[1000], [1]], rows: 1 } }))); wire.annotations.mockResolvedValue({ deploys: [], anomalies: [] });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } }); const node = document.createElement("div"); document.body.append(node); const root = createRoot(node); let current: ReturnType<typeof usePanelResults>;
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
async function waitForHook<T>(check: () => T): Promise<T> {
  return await vi.waitFor(async () => {
    await act(async () => {});
    return check();
  }, {timeout: 3000, interval: 5});
}
async function mountVisibility(visible: string[], spec = visibilitySpec, renderPanels = false, options: {height?: number; agentAvailable?: boolean; onFix?: () => void; client?: QueryClient} = {}) {
  const client = options.client ?? new QueryClient({ defaultOptions: { queries: { retry: false, retryDelay: 0 } } });
  const node = document.createElement("div"); document.body.append(node);
  const root = createRoot(node);
  let current!: ReturnType<typeof usePanelResults>;
  function Host({ visible }: { visible: string[] }) {
    current = usePanelResults({ dashboardId: "d", version: 1, spec, time: spec.time, vars: {}, compare: false, widths: {}, visible, refresh: "off" });
    return renderPanels ? <MantineProvider>{spec.panels.filter(p => visible.includes(p.id)).map(panel => <PanelCard key={panel.id} panel={panel} title={panel.title} result={current.results.get(panel.id)} loading={current.fetchingIds.includes(panel.id)} staleAt={current.staleAt.get(panel.id)} height={options.height ?? 200} group="g" editing={false} agentAvailable={options.agentAvailable ?? true} onFix={options.onFix ?? (()=>{})} onRetry={()=>current.retry(panel.id)} onView={()=>{}} onCopyLink={()=>{}} onExplain={()=>{}} />)}</MantineProvider> : null;
  }
  const show = async (visible: string[]) => {
    await act(async () => root.render(<QueryClientProvider client={client}><Host visible={visible} /></QueryClientProvider>));
  };
  await show(visible);
  return {
    get current() { return current; }, show, node,
    async dispose() { await act(async () => root.unmount()); if (!options.client) client.clear(); node.remove(); },
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

it("prioritizes a queued full refresh over a newly visible lazy batch", async () => {
 const pending = deferred<PanelResult[]>();
 wire.panels.mockResolvedValueOnce([resultFor("loaded")]).mockImplementationOnce(() => pending.promise).mockImplementation((body: QueryBody) => Promise.resolve(body.panels!.map(resultFor)));
 const host = await mountVisibility(["loaded"]);
 try {
  await waitForHook(() => { expect(host.current.fetching).toBe(false); expect(host.current.results.has("loaded")).toBe(true); });
  await host.show(["loaded", "a"]);
  await waitForHook(() => { expect(host.current.fetching).toBe(true); expect(wire.panels).toHaveBeenCalledTimes(2); });
  await act(async () => host.current.refetch());
  await host.show(["loaded", "a", "b"]);
  await act(async () => pending.resolve([resultFor("a")]));
  await waitForHook(() => { expect(wire.panels.mock.calls[2][0].panels).toEqual(["a", "b", "loaded"]); expect(host.current.fetching).toBe(false); });
  expect(wire.panels).toHaveBeenCalledTimes(3);
 } finally { await host.dispose(); }
});


it.each([new Error("Network disconnected"),new DOMException("Request aborted","AbortError"),new ApiError("Server unavailable",500)])("settles failed batch panels into retryable error cards (%s)",async error=>{
  wire.panels.mockResolvedValueOnce([resultFor("loaded")]);
  const host=await mountVisibility(["loaded"],visibilitySpec,true);
  try{
    await waitForHook(()=>{expect(host.current.fetching).toBe(false);expect(host.current.results.has("loaded")).toBe(true);});
    wire.panels.mockRejectedValue(error);
    await host.show(["loaded","a","b"]);
    await waitForHook(()=>{
      expect(host.current.fetching).toBe(false);expect(host.current.error?.message).toBe(error.message);
      for(const id of ["a","b"]){
        const card=host.node.querySelector(`[data-panel="${id}"]`)!;
        expect(card.textContent).toContain("This panel failed");expect(card.querySelector<HTMLElement>('[data-panel-error]')!.title).toBe(error.message);
        expect(card.textContent).not.toContain("Ask Fanout to fix it");
        expect([...card.querySelectorAll("button")].some(button=>button.textContent==="Retry")).toBe(true);
      }
      expect(host.node.querySelector('[aria-label="Loading panel"],[aria-label="Refreshing"]')).toBeNull();
    });
    const before=wire.panels.mock.calls.length;
    wire.panels.mockImplementation((body:QueryBody)=>Promise.resolve(body.panels!.map(resultFor)));
    const retry=[...host.node.querySelectorAll<HTMLButtonElement>('[data-panel="a"] button')].find(button=>button.textContent==="Retry")!;
    await act(async()=>retry.click());
    await waitForHook(()=>{expect(host.current.fetching).toBe(false);expect(host.current.results.get("a")?.status).toBe("ok");expect(host.current.results.get("b")?.status).toBe("ok");});
    expect(wire.panels).toHaveBeenCalledTimes(before+1);expect(wire.panels.mock.lastCall![0].panels).toEqual(["a","b"]);
    expect(host.node.textContent).not.toContain("This panel failed");
  }finally{await host.dispose();}
});

it("shows a retryable panel error while preserving the last successful frame as stale",async()=>{
  wire.panels.mockResolvedValue([resultFor("loaded")]);const host=await mountVisibility(["loaded"],visibilitySpec,true);
  try{
    await waitForHook(()=>expect(host.current.results.get("loaded")?.frame).toBeDefined());
    const successfulAt = host.current.updatedAt;
    wire.panels.mockRejectedValue(new Error("Refresh disconnected"));await act(async()=>host.current.refetch());
    await waitForHook(()=>{
      expect(host.current.fetching).toBe(false);expect(host.current.staleAt.get("loaded")).toBe(successfulAt);
      expect(within(host.node).queryByRole("button", {name: /Refresh failed: Refresh disconnected/})).not.toBeNull();
      expect(host.current.results.get("loaded")?.frame).toEqual(resultFor("loaded").frame);
    });
    await act(async()=>within(host.node).getByRole("button", {name: /loaded menu/}).click());
    expect([...document.querySelectorAll('[role="menuitem"]')].some(button=>button.textContent==="Retry")).toBe(true);
  }finally{await host.dispose();}
});


it("loads a newly visible panel normally after a different lazy batch failed", async () => {
  wire.panels.mockResolvedValueOnce([resultFor("loaded")]).mockRejectedValue(new Error("Offline"));
  const host = await mountVisibility(["loaded"], visibilitySpec, true);
  try {
    await waitForHook(() => { expect(host.current.fetching).toBe(false); expect(host.current.results.has("loaded")).toBe(true); });
    await host.show(["a"]);
    await waitForHook(() => { expect(host.current.fetching).toBe(false); expect(host.current.results.get("a")?.error).toBe("Offline"); });
    wire.panels.mockResolvedValue([resultFor("c")]);
    await host.show(["c"]);
    await waitForHook(() => { expect(wire.panels.mock.lastCall![0].panels).toEqual(["c"]); expect(host.current.fetching).toBe(false); expect(host.current.results.get("c")?.status).toBe("ok"); });
    expect(host.node.querySelector('[aria-label="Loading panel"]')).toBeNull();
    expect(host.current.results.get("a")?.error).toBe("Offline");
  } finally { await host.dispose(); }
});


it.each(["Invalid measure","Query execution failed"])("retries only failed transport panels, excluding a returned panel error: %s", async message => {
  const queryError = {id:"loaded",elapsed_ms:0,status:"error" as const,error:message};
  wire.panels.mockResolvedValueOnce([queryError]).mockRejectedValue(new Error("Offline"));
  const host = await mountVisibility(["loaded"], visibilitySpec, true);
  try {
    await waitForHook(() => { expect(host.current.fetching).toBe(false); expect(host.current.results.get("loaded")?.error).toBe(message); });
    await act(async()=>within(host.node).getByRole("button", {name: /loaded menu/}).click());
    expect(document.body.textContent).toContain("Ask Fanout to fix it");
    const before = wire.panels.mock.calls.length;
    await act(async () => host.current.retry());
    expect(wire.panels).toHaveBeenCalledTimes(before);
    await host.show(["loaded","a","b"]);
    await waitForHook(() => { expect(host.current.fetching).toBe(false); expect(host.current.results.get("a")?.error).toBe("Offline"); });
    wire.panels.mockImplementation((body:QueryBody) => Promise.resolve(body.panels!.map(resultFor)));
    await act(async () => host.current.retry());
    await waitForHook(() => { expect(host.current.fetching).toBe(false); expect(host.current.results.get("a")?.status).toBe("ok"); });
    expect(wire.panels.mock.lastCall![0].panels).toEqual(["a","b"]);
    expect(host.current.results.get("loaded")).toEqual(queryError);
  } finally { await host.dispose(); }
});

it.each([false,true])("queues a transport Retry during an unrelated lazy batch and sends its exact panels after settlement (unrelated failure=%s)", async failure => {
  wire.panels.mockResolvedValueOnce([resultFor("loaded")]).mockRejectedValue(new Error("Offline"));
  const host = await mountVisibility(["loaded"]);
  try {
    await waitForHook(() => { expect(host.current.fetching).toBe(false); expect(host.current.results.has("loaded")).toBe(true); });
    await host.show(["a","b"]);
    await waitForHook(() => { expect(host.current.fetching).toBe(false); expect(host.current.results.get("a")?.error).toBe("Offline"); });
    const pending = deferred<PanelResult[]>();
    wire.panels.mockImplementation((body:QueryBody) => body.panels!.includes("c") ? pending.promise.then(results => { if(failure)throw new ApiError("Unrelated failure",504);return results; }) : Promise.resolve(body.panels!.map(resultFor)));
    await host.show(["c"]);
    await waitForHook(() => expect(host.current.fetchingIds).toEqual(["c"]));
    const before = wire.panels.mock.calls.length;
    await act(async () => {host.current.retry();host.current.retry();});
    expect(wire.panels).toHaveBeenCalledTimes(before);
    await act(async () => pending.resolve([resultFor("c")]));
    await waitForHook(() => { expect(wire.panels).toHaveBeenCalledTimes(before+1); expect(host.current.fetching).toBe(false); expect(host.current.results.get("a")?.status).toBe("ok"); });
    expect(wire.panels.mock.lastCall![0].panels).toEqual(["a","b"]);
  } finally { await host.dispose(); }
});


it("retries the clicked transport batch without re-requesting a different failed batch", async () => {
  wire.panels.mockResolvedValueOnce([resultFor("loaded")]).mockRejectedValue(new Error("Offline"));
  const host=await mountVisibility(["loaded"],visibilitySpec,true);
  try {
    await waitForHook(()=>{expect(host.current.fetching).toBe(false);expect(host.current.results.has("loaded")).toBe(true);});
    await host.show(["a","b"]);
    await waitForHook(()=>{expect(host.current.fetching).toBe(false);expect(host.current.results.get("a")?.error).toBe("Offline");});
    await host.show(["a","b","c"]);
    await waitForHook(()=>{expect(host.current.fetching).toBe(false);expect(host.current.results.get("c")?.error).toBe("Offline");});
    wire.panels.mockImplementation((body:QueryBody)=>Promise.resolve(body.panels!.map(resultFor)));
    const retry=[...host.node.querySelectorAll<HTMLButtonElement>('[data-panel="a"] button')].find(button=>button.textContent==="Retry")!;
    await act(async()=>retry.click());
    await waitForHook(()=>{expect(host.current.fetching).toBe(false);expect(host.current.results.get("a")?.status).toBe("ok");});
    expect(wire.panels.mock.lastCall![0].panels).toEqual(["a","b"]);
    expect(host.current.results.get("c")?.error).toBe("Offline");
  } finally {await host.dispose();}
});

it("restores query error actions when a transport retry returns a panel error while keeping the successful frame",async()=>{
  wire.panels.mockResolvedValue([resultFor("loaded")]);
  const host=await mountVisibility(["loaded"],visibilitySpec,true);
  try {
    await waitForHook(()=>{expect(host.current.fetching).toBe(false);expect(host.current.results.get("loaded")?.frame).toBeDefined();});
    wire.panels.mockRejectedValue(new Error("Offline"));await act(async()=>host.current.refetch());
    await waitForHook(()=>{expect(host.current.fetching).toBe(false);expect(host.current.results.get("loaded")?.error).toBe("Offline");});
    wire.panels.mockResolvedValue([{id:"loaded",status:"error",elapsed_ms:0,error:"Invalid query"}]);
    await act(async()=>host.current.retry());
    await waitForHook(()=>{expect(host.current.fetching).toBe(false);expect(host.current.results.get("loaded")?.error).toBe("Invalid query");});
    expect(host.current.results.get("loaded")?.frame).toEqual(resultFor("loaded").frame);
    await act(async()=>within(host.node).getByRole("button", {name: /loaded menu/}).click());
    expect(document.body.textContent).toContain("Ask Fanout to fix it");
    const before=wire.panels.mock.calls.length;await act(async()=>host.current.retry());expect(wire.panels).toHaveBeenCalledTimes(before);
  } finally {await host.dispose();}
});

const failureCases = [
  {name: "network", failure: new Error("Failed to fetch"), retryable: true},
  {name: "unintentional abort", failure: new DOMException("Request aborted", "AbortError"), retryable: true},
  ...[408, 429, 500, 504].map(status => ({name: "HTTP " + status, failure: new ApiError("Request failed", status), retryable: true})),
  {name: "missing result", message: undefined, retryable: true},
  {name: "dashboard execution timeout", message: "Not run: the dashboard ran out of time. Narrow the time range or split the dashboard.", retryable: true},
  {name: "query execution timeout", message: "The query took longer than 10 seconds. Narrow the time range or add filters.", retryable: true},
  {name: "returned validation", message: "Invalid measure", retryable: false},
  {name: "returned invalid query", message: "Invalid query", retryable: false},
  ...[400, 401, 403, 404, 422].map(status => ({name: "HTTP " + status, failure: new ApiError("Invalid request", status), retryable: false})),
];
it.each([200, 388].flatMap(height => [false, true].flatMap(stale => failureCases.map(failure => ({height, stale, ...failure})))))
("offers working Retry or Fix for $name at height=$height with retained frame=$stale", async scenario => {
  const fix = vi.fn();
  const failedResults = ["a", "b"].map(id => ({id, status: "error" as const, elapsed_ms: 0, error: scenario.message}));
  const fail = () => "failure" in scenario ? Promise.reject(scenario.failure) : Promise.resolve(scenario.message === undefined ? [] : failedResults);
  wire.panels.mockImplementation(scenario.stale ? (body: QueryBody) => Promise.resolve(body.panels!.map(resultFor)) : fail);
  const host = await mountVisibility(scenario.stale ? ["a", "b", "c"] : ["a", "b"], visibilitySpec, true, {height: scenario.height, onFix: fix});
  try {
    if (scenario.stale) {
      await waitForHook(() => {expect(host.current.fetching).toBe(false); expect(host.current.results.get("a")?.frame).toBeDefined();});
      await host.show(["a", "b"]);
      wire.panels.mockImplementation(fail);
      await act(async () => host.current.refetch());
    }
    await waitForHook(() => {expect(host.current.fetching).toBe(false); expect(host.current.results.get("a")?.error).toBeDefined();});
    const card = host.node.querySelector<HTMLElement>('[data-panel="a"]')!;
    const query = within(card);
    if (scenario.stale) await act(async () => query.getByRole("button", {name: /a menu/}).click());
    const actions = scenario.stale ? within(document.body) : query;
    // happy-dom has zero anchor bounds: Mantine hides detached dropdowns.
    // Menu actions are queried with hidden:true; the focusable warning uses normal accessible queries.
    const role = scenario.stale ? "menuitem" : "button";
    if (scenario.retryable) {
      expect(actions.queryByRole(role, {name: "Ask Fanout to fix it", hidden: role === "menuitem"})).toBeNull();
      const retry = await waitForHook(() => actions.getByRole(role, {name: "Retry", hidden: role === "menuitem"}));
      const before = wire.panels.mock.calls.length;
      wire.panels.mockImplementation((body: QueryBody) => Promise.resolve(body.panels!.map(resultFor)));
      await act(async () => retry.click());
      await waitForHook(() => {expect(wire.panels).toHaveBeenCalledTimes(before + 1); expect(host.current.fetching).toBe(false); expect(host.current.results.get("a")?.error).toBeUndefined();});
      expect(wire.panels.mock.lastCall![0].panels).toEqual(["a", "b"]);
      if (scenario.stale) expect(host.current.results.get("c")?.frame).toEqual(resultFor("c").frame);
    } else {
      expect(actions.queryByRole(role, {name: "Retry", hidden: role === "menuitem"})).toBeNull();
      const fixAction = await waitForHook(() => actions.getByRole(role, {name: "Ask Fanout to fix it", hidden: role === "menuitem"}));
      await act(async () => fixAction.click());
      expect(fix).toHaveBeenCalledOnce();
      if (!scenario.stale) {
        await act(async () => query.getByRole("button", {name: "a menu"}).click());
        expect(within(document.body).queryByRole("menuitem", {name: "Retry", hidden: true})).toBeNull();
        const menuFix = within(document.body).getByRole("menuitem", {name: "Ask Fanout to fix it", hidden: true});
        await act(async () => menuFix.click());
        expect(fix).toHaveBeenCalledTimes(2);
      }
    }
  } finally {await host.dispose();}
});

it.each([200, 388])("shows only a fixable error message without an agent at height=%i", async height => {
  wire.panels.mockResolvedValue([{id: "a", status: "error", elapsed_ms: 0, error: "Invalid query"}]);
  const host = await mountVisibility(["a"], visibilitySpec, true, {height, agentAvailable: false});
  try {
    await waitForHook(() => {expect(host.current.fetching).toBe(false); expect(host.node.textContent).toContain("Invalid query");});
    const card = within(host.node.querySelector<HTMLElement>('[data-panel="a"]')!);
    expect(card.queryByRole("button", {name: "Retry", hidden: false})).toBeNull();
    expect(card.queryByRole("button", {name: "Ask Fanout to fix it", hidden: false})).toBeNull();
    await act(async () => card.getByRole("button", {name: "a menu"}).click());
    expect(within(document.body).queryByRole("menuitem", {name: "Retry", hidden: true})).toBeNull();
    expect(within(document.body).queryByRole("menuitem", {name: "Ask Fanout to fix it", hidden: true})).toBeNull();
  } finally {await host.dispose();}
});

it("retries only timeouts and missing results in a mixed returned batch", async () => {
  wire.panels.mockResolvedValue([
    resultFor("loaded"),
    {id: "a", status: "error", elapsed_ms: 0, error: "The query took longer than 10 seconds. Narrow the time range or add filters."},
    {id: "b", status: "error", elapsed_ms: 0, error: "Invalid query"},
  ]);
  const host = await mountVisibility(["loaded", "a", "b", "c"], visibilitySpec, true);
  try {
    await waitForHook(() => {expect(host.current.fetching).toBe(false); expect(host.current.results.get("c")?.error).toContain("No result was returned");});
    const query = within(host.node);
    const retries = query.getAllByRole("button", {name: "Retry"});
    expect(retries).toHaveLength(2);
    expect(within(host.node.querySelector<HTMLElement>('[data-panel="b"]')!).queryByRole("button", {name: "Retry"})).toBeNull();
    wire.panels.mockImplementation((body: QueryBody) => Promise.resolve(body.panels!.map(resultFor)));
    const before = wire.panels.mock.calls.length;
    await act(async () => retries[0].click());
    await waitForHook(() => {expect(wire.panels).toHaveBeenCalledTimes(before + 1); expect(host.current.fetching).toBe(false); expect(host.current.results.get("c")?.status).toBe("ok");});
    expect(wire.panels.mock.lastCall![0].panels).toEqual(["a", "c"]);
    expect(host.current.results.get("b")?.error).toBe("Invalid query");
    expect(host.current.results.get("loaded")?.frame).toEqual(resultFor("loaded").frame);
  } finally {await host.dispose();}
});

it("keeps separate lazy batch results completed in the same millisecond", async () => {
  const clock = vi.spyOn(Date, "now").mockReturnValue(Date.parse("2026-10-08T12:00:00Z"));
  wire.panels.mockImplementation((body: QueryBody) => Promise.resolve(body.panels!.map(resultFor)));
  const host = await mountVisibility(["loaded"]);
  try {
    await waitForHook(() => {expect(host.current.fetching).toBe(false); expect(host.current.results.get("loaded")?.status).toBe("ok");});
    await host.show(["a"]);
    await waitForHook(() => {expect(wire.panels).toHaveBeenCalledTimes(2); expect(host.current.fetching).toBe(false); expect(host.current.results.get("a")?.status).toBe("ok");});
    expect(host.current.results.get("loaded")?.status).toBe("ok");
  } finally {await host.dispose(); clock.mockRestore();}
});

it("merges new batches after remounting a cached panel query", async () => {
  const clock = vi.spyOn(Date, "now").mockReturnValue(Date.parse("2026-10-08T12:00:00Z"));
  const client = new QueryClient({defaultOptions: {queries: {retry: false, retryDelay: 0}}});
  wire.panels.mockResolvedValue([resultFor("loaded")]);
  const first = await mountVisibility(["loaded"], visibilitySpec, false, {client});
  try {
    await waitForHook(() => {expect(first.current.fetching).toBe(false); expect(first.current.results.get("loaded")?.frame?.values).toEqual([[1]]);});
  } finally {await first.dispose();}
  const second = await mountVisibility(["loaded"], visibilitySpec, false, {client});
  try {
    await waitForHook(() => expect(second.current.results.get("loaded")?.frame?.values).toEqual([[1]]));
    wire.panels.mockResolvedValue([{...resultFor("loaded"), frame: {...resultFor("loaded").frame!, values: [[8]]}}]);
    await act(async () => second.current.refetch());
    await waitForHook(() => {expect(second.current.fetching).toBe(false); expect(second.current.results.get("loaded")?.frame?.values).toEqual([[8]]);});
  } finally {await second.dispose(); client.clear(); clock.mockRestore();}
});
