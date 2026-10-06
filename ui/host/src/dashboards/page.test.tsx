import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSearch } from "./search";

vi.mock("./echart-canvas", () => ({ EChartCanvas: ({ label, onClick, onZoom }: { label: string; onClick?: (event: { name: string; seriesName: string; value: number[] }) => void; onZoom?: (from: number, to: number) => void }) => <div data-chart={label}>
  {onClick && <button aria-label={`Select ${label}`} onClick={() => onClick({ name: "cart", seriesName: "cart", value: [1000, 3] })} />}
  {onZoom && <button aria-label={`Zoom ${label}`} onClick={() => onZoom(1000, 9000)} />}
</div> }));
const app = vi.hoisted(() => ({ agentAvailable: true, openChat: vi.fn() }));
vi.mock("../app-context", async (importOriginal) => ({
  ...await importOriginal<typeof import("../app-context")>(),
  useFanoutApp: () => app,
}));

import { DashboardPage } from "./page";

const spec = {
  version: 1, name: "Checkout", description: "Money path", time: { range: "1h", refresh: "30s" },
  variables: [{ name: "service", kind: "query", from: "spans", field: "service", default: "checkout" }],
  panels: [
    { id: "requests", title: "Requests", viz: "stat", width: 3, height: "s", query: { from: "spans", measures: ["count()"] }, grid: { x: 0, y: 0, w: 3, h: 3 } },
    { id: "latency", title: "Latency for $service", viz: "timeseries", width: 9, height: "m", unit: "ms", query: { from: "spans", measures: ["p95(duration_ms)"], bucket: "auto" }, grid: { x: 3, y: 0, w: 9, h: 6 } },
  ],
};
const record = { id: "d1", name: "Checkout", description: "Money path", is_default: true, version: 4, spec, created_at: "t", updated_at: "t" };
const frame = { columns: [{ name: "time", type: "time", role: "time" }, { name: "count", type: "number", role: "measure", unit: "count" }], values: [[1, 2], [3, 4]], rows: 2, totals: [null, 120] };

const fetchMock = vi.fn<typeof fetch>();
const json = (body: unknown, status = 200) => new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
let queryBodies: unknown[] = [];
let servedRecord: unknown;
let listResponse: () => Promise<Response>;
let variableResponse: () => Promise<Response>;
let panelResponse: () => Promise<Response>;
let visibility: IntersectionObserverCallback;
const cleanups: (() => void)[] = [];
const defaultPanels = () => json({ results: [{ id: "requests", status: "ok", frame, elapsed_ms: 2 }, { id: "latency", status: "empty", diagnosis: "No spans match service = 'cart'.", elapsed_ms: 3 }] });
const settle = () => act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); });

beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  queryBodies = [];
  app.agentAvailable = true;
  app.openChat.mockClear();
  servedRecord = record;
  listResponse = async () => json({ dashboards: [{ id: "d1", name: "Checkout", description: "", is_default: true, version: 4, panel_count: 2, updated_at: "t" }] });
  variableResponse = async () => json({ options: { service: [{ value: "checkout", count: 9 }, { value: "cart", count: 3 }] } });
  panelResponse = async () => defaultPanels();
  fetchMock.mockClear();
  fetchMock.mockImplementation(async (input, init) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/dashboards") return listResponse();
    if (url.pathname === "/api/dashboards/d1") return json(servedRecord);
    if (url.pathname === "/api/dashboards/missing") return json({ message: "dashboard not found" }, 404);
    if (url.pathname === "/api/variables/resolve") return variableResponse();
    if (url.pathname === "/api/panels/query") {
      const body = JSON.parse(String(init?.body)) as { panels: string[]; dashboard: { panels: { id: string }[] } };
      queryBodies.push(body);
      if (body.panels.some((id) => !body.dashboard.panels.some((panel) => panel.id === id))) return json({ message: "no panel has id" }, 400);
      return panelResponse();
    }
    return json({}, 404);
  });
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  vi.stubGlobal("IntersectionObserver", class { constructor(callback: IntersectionObserverCallback) { visibility = callback; } observe() {} unobserve() {} disconnect() {} });
});
afterEach(async () => {
  await act(async () => { cleanups.splice(0).forEach((cleanup) => cleanup()); });
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
});

async function render(search: DashboardSearch = {}, dashboardId = "d1") {
  const host = document.createElement("div");
  document.body.append(host);
  const onSearch = vi.fn();
  const onOpen = vi.fn();
  const client = new QueryClient({ defaultOptions: { queries: { retry: 2, retryDelay: 0 } } });
  const root = createRoot(host);
  cleanups.push(() => { root.unmount(); client.clear(); });
  const rerender = async (next: DashboardSearch) => {
    await act(async () => {
      root.render(<MantineProvider><QueryClientProvider client={client}><DashboardPage dashboardId={dashboardId || undefined} search={next} onSearch={onSearch} onOpen={onOpen} /></QueryClientProvider></MantineProvider>);
    });
    await settle();
  };
  await rerender(search);
  return { host, onSearch, onOpen, rerender, client };
}

describe("DashboardPage", () => {
  it("filters via panel.click.set_variable, removes chips with replace, and pushes brush ranges", async () => {
    servedRecord = { ...record, spec: { ...spec, panels: [{ ...spec.panels[1], title: "Services", click: { set_variable: "service" }, time: { shift: "1d" }, query: { from: "spans", measures: ["count()"], by: ["service"], bucket: "auto" } }] } };
    panelResponse = async () => json({ results: [{ id: "latency", status: "ok", frame: { columns: [...frame.columns, { name: "service", type: "string", role: "dimension" }], values: [...frame.values, ["cart", "cart"]], rows: 2 }, elapsed_ms: 1 }] });
    const initial: DashboardSearch = { range: "1h", compare: "1", vars: { other: "kept" } };
    const { host, onSearch, rerender } = await render(initial);
    expect(host.querySelector('[data-panel="latency"] [role="status"]')?.textContent).toBe("Shifted 1d");
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Select Services: time series"]')!.click());
    expect(onSearch).toHaveBeenLastCalledWith({ ...initial, vars: { other: "kept", service: "cart" } }, true);
    const filtered = onSearch.mock.lastCall![0] as DashboardSearch;
    await rerender(filtered);
    const chip = host.querySelector<HTMLButtonElement>('[aria-label="Remove filter service"]')!;
    expect(chip.textContent).toContain("$service = cart");
    await act(async () => chip.click());
    expect(onSearch).toHaveBeenLastCalledWith(initial, true);
    await rerender(initial);
    expect(host.querySelector('[aria-label="Remove filter service"]')).toBeNull();
    await act(async () => {
      const zoom = host.querySelector<HTMLButtonElement>('[aria-label="Zoom Services: time series"]')!;
      for (let i = 0; i < 20; i++) zoom.click();
    });
    expect(onSearch).toHaveBeenCalledTimes(3);
    expect(onSearch).toHaveBeenLastCalledWith({ ...initial, range: undefined, from: new Date(1000).toISOString(), to: new Date(9000).toISOString() }, false);
  });

  it("loads the spec and sends one batch with the URL's view state", async () => {
    const { host } = await render({ range: "6h", vars: { service: "cart" } });
    expect(host.textContent).toContain("Checkout");
    expect(host.textContent).toContain("Latency for cart");
    expect(queryBodies).toHaveLength(1);
    expect(queryBodies[0]).toMatchObject({ time: { range: "6h" }, vars: { service: "cart" } });
    expect(host.textContent).toContain("No spans match service = 'cart'.");
  });

  it("answers a missing dashboard instead of showing another", async () => {
    const { host } = await render({}, "missing");
    expect(host.textContent).toContain("This dashboard isn't here");
  });

  it("shows an empty dashboard list and opens chat with a starter prompt", async () => {
    listResponse = async () => json({ dashboards: [] });
    const { host, onOpen } = await render({}, "");
    expect(host.textContent).toContain("No dashboards yet");
    expect(host.textContent).not.toContain("Loading your dashboard");
    await act(async () => { host.querySelector("button")!.click(); });
    expect(app.openChat).toHaveBeenCalledWith(expect.stringMatching(/^Build me a dashboard for /));
    expect(onOpen).not.toHaveBeenCalled();
  });

  it("hides the empty-state chat action when the agent is unavailable", async () => {
    app.agentAvailable = false;
    listResponse = async () => json({ dashboards: [] });
    const { host } = await render({}, "");
    expect(host.textContent).toContain("No dashboards yet");
    expect(host.querySelector("button")).toBeNull();
  });

  it("shows a dashboard list error", async () => {
    listResponse = async () => json({ message: "List failed" }, 500);
    const { host } = await render({}, "");
    expect(host.textContent).toContain("Dashboards could not be loaded");
    expect(host.textContent).toContain("List failed");
    expect(host.textContent).not.toContain("Loading your dashboard");
  });

  it("loads an explicit dashboard without waiting for its list", async () => {
    listResponse = () => new Promise(() => {});
    const { host } = await render();
    expect(host.textContent).toContain("Checkout");
    expect(queryBodies).toHaveLength(1);
  });

  it("redirects a populated index to the default dashboard with replace", async () => {
    const { onOpen } = await render({}, "");
    expect(onOpen).toHaveBeenCalledWith("d1", true);
  });

  it("waits for defaultless variable options and sends exactly one panel request with opts[0]", async () => {
    servedRecord = { ...record, spec: { ...spec, variables: [{ name: "service", kind: "query", from: "spans", field: "service" }] } };
    let resolve!: (response: Response) => void;
    variableResponse = () => new Promise((done) => { resolve = done; });
    const { host } = await render();
    expect(queryBodies).toHaveLength(0);
    // Manual refresh must obey the same gate as the initial request.
    await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.click(); });
    expect(queryBodies).toHaveLength(0);
    await act(async () => { resolve(json({ options: { service: [{ value: "cart" }] } })); });
    await settle();
    expect(queryBodies).toHaveLength(1);
    expect(queryBodies[0]).toMatchObject({ vars: { service: "cart" } });
    expect(host.textContent).toContain("Latency for cart");
  });

  it("queries panels after a variables request error and omits unresolved query variables", async () => {
    servedRecord = { ...record, spec: { ...spec, variables: [{ name: "service", kind: "query", from: "spans", field: "service" }] } };
    variableResponse = async () => json({ message: "Variables failed" }, 400);
    const { host } = await render();
    expect(host.textContent).toContain("Variables could not be loaded");
    expect(host.textContent).toContain("Variables failed");
    expect(queryBodies).toHaveLength(1);
    expect((queryBodies[0] as { vars: unknown }).vars).toEqual({});
  });

  it("keeps placeholder options on screen but gates panels on the current key's options", async () => {
    servedRecord = { ...record, spec: { ...spec, variables: [{ name: "service", kind: "query", from: "spans", field: "service" }] } };
    const { host, rerender } = await render();
    const input = () => host.querySelector<HTMLInputElement>('[aria-label="Dashboard variables"] input:not([type="hidden"])')!;
    expect(input().value).toBe("checkout");
    let resolve!: (response: Response) => void;
    variableResponse = () => new Promise((done) => { resolve = done; });
    await rerender({ range: "6h" });
    expect(input().value).toBe("checkout");
    expect(queryBodies).toHaveLength(1);
    await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.click(); });
    expect(queryBodies).toHaveLength(1);
    await act(async () => { resolve(json({ options: { service: [{ value: "cart" }] } })); });
    await settle();
    expect(input().value).toBe("cart");
    expect(queryBodies).toHaveLength(2);
    expect(queryBodies[1]).toMatchObject({ vars: { service: "cart" } });
  });

  it("omits an empty query option list from panel variables instead of sending an empty string", async () => {
    servedRecord = { ...record, spec: { ...spec, variables: [{ name: "service", kind: "query", from: "spans", field: "service" }] } };
    variableResponse = async () => json({ options: { service: [] } });
    await render();
    expect(queryBodies).toHaveLength(1);
    expect((queryBodies[0] as { vars: unknown }).vars).toEqual({});
  });

  it("uses validated variable values for both the bar and panels without rewriting the URL", async () => {
    servedRecord = { ...record, spec: { ...spec, variables: [{ name: "service", kind: "custom", options: ["checkout", "cart"] }] } };
    const { host, onSearch } = await render({ vars: { service: "missing" } });
    expect(queryBodies).toHaveLength(1);
    expect(queryBodies[0]).toMatchObject({ vars: { service: "checkout" } });
    expect(host.querySelector<HTMLInputElement>('[aria-label="Dashboard variables"] input:not([type="hidden"])')!.value).toBe("checkout");
    expect(host.textContent).toContain("Latency for checkout");
    expect(onSearch).not.toHaveBeenCalled();
  });

  it("renders every ApiError problem path, message and optional hint", async () => {
    panelResponse = async () => json({ message: "Invalid dashboard", problems: [
      { path: "panels[0].query", message: "bad measure", hint: "Use count()" },
      { path: "variables[0].field", message: "unknown field" },
    ] }, 400);
    const { host } = await render();
    expect(host.textContent).toContain("panels[0].query: bad measure (Use count())");
    expect(host.textContent).toContain("variables[0].field: unknown field");
  });

  it("drops old panel results on a range change, including panels absent from the new response", async () => {
    const { host, rerender } = await render();
    expect(host.textContent).toContain("No spans match service = 'cart'.");
    let resolve!: (response: Response) => void;
    panelResponse = () => new Promise((done) => { resolve = done; });
    await rerender({ range: "6h" });
    expect(host.textContent).not.toContain("No spans match service = 'cart'.");
    await act(async () => { resolve(json({ results: [{ id: "requests", status: "ok", elapsed_ms: 1 }] })); });
    await settle();
    expect(queryBodies).toHaveLength(2);
    expect(host.textContent).not.toContain("No spans match service = 'cart'.");
  });

  it("passes query signals to fetch and aborts superseded panel and variable requests", async () => {
    variableResponse = () => new Promise(() => {});
    panelResponse = () => new Promise(() => {});
    const { rerender } = await render();
    const signals = fetchMock.mock.calls.filter(([input]) => /\/api\/(panels\/query|variables\/resolve)$/.test(String(input)))
      .map(([, init]) => init?.signal);
    expect(signals).toHaveLength(2);
    expect(signals.every((signal) => signal instanceof AbortSignal)).toBe(true);
    await rerender({ range: "6h" });
    expect(signals.every((signal) => signal?.aborted)).toBe(true);
  });

  it("updates a text variable input when URL state changes", async () => {
    servedRecord = { ...record, spec: { ...spec, variables: [{ name: "service", kind: "text" }] } };
    const { host, rerender } = await render({ vars: { service: "checkout" } });
    expect(host.querySelector<HTMLInputElement>('[aria-label="Dashboard variables"] input')!.value).toBe("checkout");
    await rerender({ vars: { service: "cart" } });
    expect(host.querySelector<HTMLInputElement>('[aria-label="Dashboard variables"] input')!.value).toBe("cart");
    await rerender({ vars: { service: "checkout" } });
    expect(host.querySelector<HTMLInputElement>('[aria-label="Dashboard variables"] input')!.value).toBe("checkout");
  });

  it.each([true, false])("clearing a multi-select uses All or an explicit empty URL list (include_all=%s)", async (include_all) => {
    servedRecord = { ...record, spec: { ...spec, variables: [{ name: "service", kind: "custom", options: ["checkout", "cart"], multi: true, include_all }] } };
    const { host, onSearch } = await render({ vars: { service: ["cart"], other: "kept" } });
    const clear = host.querySelector<HTMLButtonElement>('.mantine-InputClearButton-root')!;
    expect(clear).not.toBeNull();
    await act(async () => { clear.click(); });
    expect(onSearch).toHaveBeenLastCalledWith({ vars: include_all ? { service: "$__all", other: "kept" } : { service: [], other: "kept" } }, true);
  });

  it("doubles an absolute range around its midpoint when zooming out", async () => {
    const { host, onSearch } = await render({ from: "2026-10-01T12:00:00Z", to: "2026-10-01T13:00:00Z" });
    await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Zoom out"]')!.click(); });
    expect(onSearch).toHaveBeenCalledWith({ range: undefined, from: "2026-10-01T11:30:00.000Z", to: "2026-10-01T13:30:00.000Z" });
  });
});


describe("dashboard batch regressions", () => {
  it("excludes a removed on-screen panel from the next batch without a page error", async () => {
    const { host, client } = await render({ edit: "1" });
    servedRecord = { ...record, version: 5, spec: { ...spec, panels: [spec.panels[0]] } };
    await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Latency for checkout menu"]')!.click(); });
    await act(async () => { [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find((el) => el.textContent === "Remove panel")!.click(); });
    await settle();
    expect(client.getQueryData(["dashboard", "d1"])).toEqual(servedRecord);
    expect(queryBodies).toHaveLength(2);
    expect(queryBodies[1]).toMatchObject({ panels: ["requests"] });
    expect(host.textContent).not.toContain("Panels could not be loaded");
    expect(host.textContent).not.toContain("Latency for checkout");
  });

  it("never sends an unknown URL view id even after visibility changes", async () => {
    const { host } = await render({ view: "gone" });
    await act(async () => { visibility([], {} as IntersectionObserver); });
    await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.click(); });
    await settle();
    expect(queryBodies).toHaveLength(2);
    for (const body of queryBodies) expect((body as { panels: string[] }).panels).toEqual(["latency", "requests"]);
    expect(host.textContent).not.toContain("Panels could not be loaded");
  });

  it("preserves results and avoids requests and loaders after a layout-only version save", async () => {
    const { host, client } = await render();
    expect(host.textContent).toContain("120");
    const calls = fetchMock.mock.calls.length;
    await act(async () => { client.setQueryData(["dashboard", "d1"], { ...record, version: 5, spec: { ...spec, panels: spec.panels.map((panel) => ({ ...panel, grid: { ...panel.grid, w: 6 } })) } }); });
    await settle();
    expect(queryBodies).toHaveLength(1);
    expect(fetchMock.mock.calls).toHaveLength(calls);
    expect(host.textContent).toContain("120");
    expect(host.querySelector('[aria-label="Refreshing"]')).toBeNull();
    // Panel-relevant edits must still discard old results and fetch.
    panelResponse = () => new Promise(() => {});
    await act(async () => { client.setQueryData(["dashboard", "d1"], { ...record, version: 6, spec: { ...spec, panels: spec.panels.map((panel) => ({ ...panel, title: "Changed" })) } }); });
    await settle();
    expect(queryBodies).toHaveLength(2);
    expect(host.textContent).not.toContain("120");
  });

  it("refreshes only the visible in-flight panel headers", async () => {
    const { host } = await render();
    const hidden = host.querySelector('.react-grid-item[data-panel="latency"]')!;
    await act(async () => { visibility([{ target: hidden, isIntersecting: false } as IntersectionObserverEntry], {} as IntersectionObserver); });
    panelResponse = () => new Promise(() => {});
    await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.click(); });
    await settle();
    expect((queryBodies.at(-1) as { panels: string[] }).panels).toEqual(["requests"]);
    expect(host.querySelector('[data-panel="requests"] [aria-label="Refreshing"]')).not.toBeNull();
    expect(host.querySelector('[data-panel="latency"] [aria-label="Refreshing"]')).toBeNull();
  });

  it.each(["panels", "variables"])("does not retry a 400 from %s", async (kind) => {
    if (kind === "panels") panelResponse = async () => json({ message: "Invalid" }, 400);
    else variableResponse = async () => json({ message: "Invalid" }, 400);
    await render();
    await act(async () => { await new Promise((resolve) => setTimeout(resolve, 1100)); });
    const path = kind === "panels" ? "/api/panels/query" : "/api/variables/resolve";
    expect(fetchMock.mock.calls.filter(([input]) => String(input) === path)).toHaveLength(1);
  });
});


it("keeps scrolled-out results for the same key when another panel refreshes", async () => {
  const { host, rerender } = await render();
  const hidden = host.querySelector('.react-grid-item[data-panel="latency"]')!;
  await act(async () => { visibility([{ target: hidden, isIntersecting: false } as IntersectionObserverEntry], {} as IntersectionObserver); });
  panelResponse = async () => json({ results: [{ id: "requests", status: "ok", frame, elapsed_ms: 9 }] });
  await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.click(); });
  await settle();
  expect(host.textContent).toContain("No spans match service = 'cart'.");
  panelResponse = () => new Promise(() => {});
  await rerender({ vars: { service: "cart" } });
  expect(host.textContent).not.toContain("No spans match service = 'cart'.");
});


it.each(["panels", "variables"])("retries server errors at most twice for %s", async (kind) => {
  if (kind === "panels") panelResponse = async () => json({ message: "Server failed" }, 500);
  else variableResponse = async () => json({ message: "Server failed" }, 500);
  const { host } = await render();
  await settle();
  const path = kind === "panels" ? "/api/panels/query" : "/api/variables/resolve";
  expect(fetchMock.mock.calls.filter(([input]) => String(input) === path)).toHaveLength(3);
  expect(host.textContent).toContain("Server failed");
});


it("marks older panels stale after a failed refresh and clears stale on recovery", async()=>{
 const {host}=await render();
 panelResponse=async()=>json({message:"Refresh failed"},400);
 await act(async()=>{host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.click();});await settle();
 expect(host.textContent).toContain("Panels could not be loaded");expect(host.textContent).toContain("120");
 expect(host.querySelector('[data-panel="requests"]')!.textContent).toContain("Stale: last updated");
 panelResponse=async()=>defaultPanels();
 await act(async()=>{host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.click();});await settle();
 expect(host.textContent).not.toContain("Stale: last updated");
});

it("never retries a 504 panel batch",async()=>{
 panelResponse=async()=>json({message:"Time limit"},504);await render();await settle();
 expect(queryBodies).toHaveLength(1);
});

it("keeps explicit None selected in the URL and outgoing request",async()=>{
 servedRecord={...record,spec:{...spec,variables:[{name:"service",kind:"custom",options:["checkout","cart"],multi:true}]}};
 const {host}=await render({vars:{service:[]}});
 expect(queryBodies[0]).toMatchObject({vars:{service:[]}});
 expect(host.querySelector('[placeholder="None"]')).not.toBeNull();
});


it("keeps only the failed panel stale when the next partial refresh succeeds",async()=>{
 const {host}=await render();
 panelResponse=async()=>json({results:[{id:"requests",status:"error",error:"DuckDB failed",elapsed_ms:3},{id:"latency",status:"empty",diagnosis:"empty",elapsed_ms:2}]});
 await act(async()=>{host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.click();});await settle();
 expect(host.querySelector('[data-panel="requests"]')!.textContent).toContain("Stale: last updated");
 expect(host.querySelector('[data-panel="requests"]')!.textContent).toContain("120");
 expect(host.textContent).toContain("Panels could not be loaded");
 const hidden=host.querySelector('.react-grid-item[data-panel="requests"]')!;
 await act(async()=>{visibility([{target:hidden,isIntersecting:false} as IntersectionObserverEntry],{} as IntersectionObserver);});
 panelResponse=async()=>json({results:[{id:"latency",status:"empty",diagnosis:"still empty",elapsed_ms:2}]});
 await act(async()=>{host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.click();});await settle();
 expect(host.querySelector('[data-panel="requests"]')!.textContent).toContain("Stale: last updated");
 expect(host.querySelector('[data-panel="latency"]')!.textContent).not.toContain("Stale:");
});
