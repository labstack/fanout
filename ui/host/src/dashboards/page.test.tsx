import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSearch } from "./search";
import type { AnnotationsResponse } from "../../../panels/annotations";
const charts = vi.hoisted(() => ({ option: vi.fn() }));

vi.mock("./echart-canvas", () => ({ EChartCanvas: ({ option, label, onClick, onZoom }: { option: Record<string, unknown>; label: string; onClick?: (event: { name: string; seriesName: string; value: number[] }) => void; onZoom?: (from: number, to: number) => void }) => { charts.option(option); return <div data-chart={label}>
  {onClick && <button aria-label={`Select ${label}`} onClick={() => onClick({ name: "cart", seriesName: "cart", value: [1000, 3] })} />}
  {onZoom && <button aria-label={`Zoom ${label}`} onClick={() => onZoom(1000, 9000)} />}
</div>; } }));
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
let annotationResponse: () => Promise<Response>;
let visibility: IntersectionObserverCallback;
const cleanups: (() => void)[] = [];
const defaultPanels = () => json({ results: [{ id: "requests", status: "ok", frame, elapsed_ms: 2 }, { id: "latency", status: "empty", diagnosis: "No spans match service = 'cart'.", elapsed_ms: 3 }] });
const settle = async (client: QueryClient) => {
  const deadline = Date.now() + 3000;
  let idleCycles = 0;
  // Two idle notification cycles let React enable dependent queries and render
  // their results; a momentarily idle QueryClient does not imply a ready DOM.
  do {
    await act(async () => {
      await new Promise(resolve => setTimeout(resolve, 5));
    });
    idleCycles = client.isFetching() === 0 && client.isMutating() === 0 ? idleCycles + 1 : 0;
  }
  while (idleCycles < 2 && Date.now() < deadline);
  expect(idleCycles).toBe(2);
  expect(client.isFetching()).toBe(0);
  expect(client.isMutating()).toBe(0);
};

beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  queryBodies = [];
  app.agentAvailable = true;
  app.openChat.mockClear();
  servedRecord = record;
  listResponse = async () => json({ dashboards: [{ id: "d1", name: "Checkout", description: "", is_default: true, version: 4, panel_count: 2, updated_at: "t" }] });
  variableResponse = async () => json({ options: { service: [{ value: "checkout", count: 9 }, { value: "cart", count: 3 }] } });
  panelResponse = async () => defaultPanels();
  annotationResponse = async () => json({ deploys: [], anomalies: [] });
  charts.option.mockClear();
  fetchMock.mockClear();
  fetchMock.mockImplementation(async (input, init) => {
    const url = new URL(String(input), "http://localhost");
    if (url.pathname === "/api/dashboards") return listResponse();
    if (url.pathname === "/api/dashboards/d1") return json(servedRecord);
    if (url.pathname === "/api/dashboards/missing") return json({ message: "dashboard not found" }, 404);
    if (url.pathname === "/api/variables/resolve") return variableResponse();
    if (url.pathname === "/api/annotations") return annotationResponse();
    if (url.pathname === "/api/panels/query") {
      const body = JSON.parse(String(init?.body)) as { panels: string[]; dashboard: { panels: { id: string }[] } };
      queryBodies.push(body);
      if (body.panels.some((id) => !body.dashboard.panels.some((panel) => panel.id === id))) return json({ message: "no panel has id" }, 400);
      return panelResponse();
    }
    return json({}, 404);
  });
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("ResizeObserver", class {
    constructor(private callback: ResizeObserverCallback) {}
    observe(target: Element) { this.callback([{ target, contentRect: { width: 1200 } } as ResizeObserverEntry], this as unknown as ResizeObserver); }
    unobserve() {}
    disconnect() {}
  });
  vi.stubGlobal("IntersectionObserver", class { constructor(callback: IntersectionObserverCallback) { visibility = callback; } observe() {} unobserve() {} disconnect() {} });
});
afterEach(async () => {
  await act(async () => { cleanups.splice(0).forEach((cleanup) => cleanup()); });
  vi.unstubAllGlobals();
  vi.restoreAllMocks();
  document.body.innerHTML = "";
});

async function render(search: DashboardSearch = {}, dashboardId = "d1", waitForIdle = true) {
  const host = document.createElement("div");
  document.body.append(host);
  const onSearch = vi.fn();
  const onOpen = vi.fn();
  const client = new QueryClient({ defaultOptions: { queries: { retry: 2, retryDelay: 0 } } });
  const root = createRoot(host);
  cleanups.push(() => { root.unmount(); client.clear(); });
  const rerender = async (next: DashboardSearch, waitForIdle = true) => {
    await act(async () => {
      root.render(<MantineProvider><QueryClientProvider client={client}><DashboardPage dashboardId={dashboardId || undefined} search={next} onSearch={onSearch} onOpen={onOpen} /></QueryClientProvider></MantineProvider>);
    });
    if (waitForIdle) await settle(client);
  };
  await rerender(search, waitForIdle);
  return { host, onSearch, onOpen, rerender, client };
}

describe("DashboardPage", () => {
  it.each([1100, 1440])("Part 9 keeps a tall service-map dashboard alive at width %s", async width => {
    vi.stubGlobal("ResizeObserver", class {
      constructor(private callback: ResizeObserverCallback) {}
      observe(target: Element) { this.callback([{ target, contentRect: { width, height: 180 } } as ResizeObserverEntry], this as unknown as ResizeObserver); }
      unobserve() {}
      disconnect() {}
    });
    const original = HTMLElement.prototype.getBoundingClientRect;
    vi.spyOn(HTMLElement.prototype, "getBoundingClientRect").mockImplementation(function(this: HTMLElement) {
      return this.hasAttribute("data-service-viewport") ? DOMRect.fromRect({ width, height: 180 }) : original.call(this);
    });
    const columns = ["kind", "service", "health", "spans"];
    const rows = Array.from({ length: 40 }, (_, i) => ["node", `isolated-${i}`, "healthy", 10]);
    servedRecord = { ...record, spec: { ...spec, panels: [{ id: "map", title: "Map", viz: "service_map", grid: { x: 0, y: 0, w: 12, h: 6 }, query: { from: "spans" } }] } };
    panelResponse = async () => json({ results: [{ id: "map", status: "ok", elapsed_ms: 1, frame: {
      columns: columns.map(name => ({ name, type: "string", role: "dimension" })),
      values: columns.map((_, i) => rows.map(row => row[i])), rows: rows.length,
    } }] });
    const { host } = await render();
    await vi.waitFor(async()=>{
      await act(async()=>{await new Promise(resolve=>setTimeout(resolve,0));});
      expect(host.querySelector("[data-service-viewport]")).not.toBeNull();
    },{interval:5,timeout:3000});
    const viewport = host.querySelector<HTMLElement>("[data-service-viewport]")!;
    expect(viewport).not.toBeNull();
    expect(Number(viewport.dataset.contentHeight)).toBeGreaterThan(180);
    await act(async () => {
      for (const top of [45, 90]) {
        viewport.scrollTop = top;
        viewport.dispatchEvent(new Event("scroll", { bubbles: true }));
      }
    });
    expect(host.textContent).not.toContain("Something went wrong");
    expect(host.querySelector('[data-panel="map"]')).not.toBeNull();
    expect(host.querySelectorAll("[data-service-node]")).toHaveLength(40);
    expect(viewport.scrollTop).toBe(90);
  });
  it.each(["deploys", "anomalies"] as const)("shares annotations with time panels and refreshes when %s is disabled", async disabled => {
    const annotations: AnnotationsResponse = {
      deploys: [{ namespace: "shop", service: "checkout", version: "v2", at: new Date(1000).toISOString() }],
      anomalies: [{ namespace: "shop", service: "checkout", kind: "latency", from: new Date(3000).toISOString(), to: new Date(5000).toISOString(), title: "Slow", severity: "bad" }], truncated: true,
    };
    servedRecord = { ...record, spec: { ...spec, panels: [spec.panels[1]] } };
    panelResponse = async () => json({ results: [{ id: "latency", status: "ok", frame, elapsed_ms: 1, from_ms: 0, to_ms: 10000 }] });
    annotationResponse = async () => json(annotations);
    const { host, client } = await render(); await settle(client);
    const marks = () => (charts.option.mock.lastCall![0].series as { markLine: { data: unknown[] }; markArea: { data: unknown[] } }[])[0];
    await vi.waitFor(() => expect(marks().markLine.data).toHaveLength(1), { interval: 5, timeout: 3000 }); await vi.waitFor(() => expect(marks().markArea.data).toHaveLength(1), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(host.textContent).toContain("Annotation history is limited."), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(queryBodies).toHaveLength(1), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(fetchMock.mock.calls.filter(([url]) => String(url) === "/api/annotations")).toHaveLength(1), { interval: 5, timeout: 3000 });
    await act(async () => { client.setQueryData(["dashboard", "d1"], { ...record, version: 5, spec: { ...spec, panels: [spec.panels[1]], annotations: { [disabled]: false } } }); });
    await settle(client);
    await vi.waitFor(() => expect(queryBodies).toHaveLength(2), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(fetchMock.mock.calls.filter(([url]) => String(url) === "/api/annotations")).toHaveLength(2), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(marks().markLine.data).toHaveLength(disabled === "deploys" ? 0 : 1), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(marks().markArea.data).toHaveLength(disabled === "anomalies" ? 0 : 1), { interval: 5, timeout: 3000 });
  });

  it("shows annotation failures in the header while keeping successful panels", async () => {
    panelResponse = async () => json({ results: [{ id: "requests", status: "ok", frame, elapsed_ms: 1 }, { id: "latency", status: "ok", frame, elapsed_ms: 1, from_ms: 0, to_ms: 10000 }] });
    annotationResponse = async () => json({ message: "Unavailable" }, 503);
    const { host, client } = await render(); await settle(client);
    await vi.waitFor(() => expect(host.textContent).toContain("Annotations unavailable"), { interval: 5, timeout: 3000 }); await vi.waitFor(() => expect(host.textContent).toContain("Unavailable"), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(host.textContent).toContain("120"), { interval: 5, timeout: 3000 }); await vi.waitFor(() => expect(host.textContent).not.toContain("Panels could not be loaded"), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(queryBodies).toHaveLength(1), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(fetchMock.mock.calls.filter(([url]) => String(url) === "/api/annotations")).toHaveLength(1), { interval: 5, timeout: 3000 });
  });

  it("filters via panel.click.set_variable, removes chips with replace, and pushes brush ranges", async () => {
    servedRecord = { ...record, spec: { ...spec, panels: [{ ...spec.panels[1], title: "Services", click: { set_variable: "service" }, time: { shift: "1d" }, query: { from: "spans", measures: ["count()"], by: ["service"], bucket: "auto" } }] } };
    panelResponse = async () => json({ results: [{ id: "latency", status: "ok", frame: { columns: [...frame.columns, { name: "service", type: "string", role: "dimension" }], values: [...frame.values, ["cart", "cart"]], rows: 2 }, elapsed_ms: 1 }] });
    const initial: DashboardSearch = { range: "1h", compare: "1", vars: { other: "kept" } };
    const { host, onSearch, rerender } = await render(initial);
    await vi.waitFor(() => expect(host.querySelector('[data-panel="latency"] [role="status"]')?.textContent).toBe("Shifted 1d"), { interval: 5, timeout: 3000 });
    await act(async () => host.querySelector<HTMLButtonElement>('[aria-label="Select Services: time series"]')!.click());
    expect(onSearch).toHaveBeenLastCalledWith({ ...initial, vars: { other: "kept", service: "cart" } }, true);
    const filtered = onSearch.mock.lastCall![0] as DashboardSearch;
    await rerender(filtered);
    const chip = host.querySelector<HTMLButtonElement>('[aria-label="Remove filter service"]')!;
    await vi.waitFor(() => expect(chip.textContent).toContain("$service = cart"), { interval: 5, timeout: 3000 });
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
    await vi.waitFor(() => expect(host.textContent).toContain("Checkout"), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(host.textContent).toContain("Latency for cart"), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(queryBodies).toHaveLength(1), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(queryBodies[0]).toMatchObject({ time: { range: "6h" }, vars: { service: "cart" } }), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(host.textContent).toContain("No spans match service = 'cart'."), { interval: 5, timeout: 3000 });
  });

  it("answers a missing dashboard instead of showing another", async () => {
    const { host } = await render({}, "missing");
    await vi.waitFor(() => expect(host.textContent).toContain("This dashboard isn't here"), { interval: 5, timeout: 3000 });
  });

  it("shows an empty dashboard list and opens chat with a starter prompt", async () => {
    listResponse = async () => json({ dashboards: [] });
    const { host, onOpen } = await render({}, "");
    await vi.waitFor(() => expect(host.textContent).toContain("No dashboards yet"), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(host.textContent).not.toContain("Loading your dashboard"), { interval: 5, timeout: 3000 });
    await act(async () => { host.querySelector("button")!.click(); });
    expect(app.openChat).toHaveBeenCalledWith(expect.stringMatching(/^Build me a dashboard for /));
    expect(onOpen).not.toHaveBeenCalled();
  });

  it("hides the empty-state chat action when the agent is unavailable", async () => {
    app.agentAvailable = false;
    listResponse = async () => json({ dashboards: [] });
    const { host } = await render({}, "");
    await vi.waitFor(() => expect(host.textContent).toContain("No dashboards yet"), { interval: 5, timeout: 3000 });
    expect(host.querySelector("button")).toBeNull();
  });

  it("shows a dashboard list error", async () => {
    listResponse = async () => json({ message: "List failed" }, 500);
    const { host } = await render({}, "");
    await vi.waitFor(() => expect(host.textContent).toContain("Dashboards could not be loaded"), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(host.textContent).toContain("List failed"), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(host.textContent).not.toContain("Loading your dashboard"), { interval: 5, timeout: 3000 });
  });

  it("loads an explicit dashboard without waiting for its list", async () => {
    listResponse = () => new Promise(() => {});
    const { host } = await render({}, "d1", false);
    await vi.waitFor(() => expect(host.textContent).toContain("Checkout"), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(queryBodies).toHaveLength(1), { interval: 5, timeout: 3000 });
  });

  it("redirects a populated index to the default dashboard with replace", async () => {
    const { onOpen } = await render({}, "");
    expect(onOpen).toHaveBeenCalledWith("d1", true);
  });

  it("waits for defaultless variable options and sends exactly one panel request with opts[0]", async () => {
    servedRecord = { ...record, spec: { ...spec, variables: [{ name: "service", kind: "query", from: "spans", field: "service" }] } };
    let resolve!: (response: Response) => void;
    variableResponse = () => new Promise((done) => { resolve = done; });
    const { host, client } = await render({}, "d1", false);
    await vi.waitFor(() => expect(host.querySelector('[aria-label="Refresh now"]')).not.toBeNull(), { interval: 5, timeout: 3000 });
    expect(queryBodies).toHaveLength(0);
    // Manual refresh must obey the same gate as the initial request.
    await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.click(); });
    expect(queryBodies).toHaveLength(0);
    await act(async () => { resolve(json({ options: { service: [{ value: "cart" }] } })); });
    await settle(client);
    await vi.waitFor(() => expect(queryBodies).toHaveLength(1), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(queryBodies[0]).toMatchObject({ vars: { service: "cart" } }), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(host.textContent).toContain("Latency for cart"), { interval: 5, timeout: 3000 });
  });

  it("queries panels after a variables request error and omits unresolved query variables", async () => {
    servedRecord = { ...record, spec: { ...spec, variables: [{ name: "service", kind: "query", from: "spans", field: "service" }] } };
    variableResponse = async () => json({ message: "Variables failed" }, 400);
    const { host } = await render();
    await vi.waitFor(() => expect(host.textContent).toContain("Variables could not be loaded"), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(host.textContent).toContain("Variables failed"), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(queryBodies).toHaveLength(1), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect((queryBodies[0] as { vars: unknown }).vars).toEqual({}), { interval: 5, timeout: 3000 });
  });

  it("keeps placeholder options on screen but gates panels on the current key's options", async () => {
    servedRecord = { ...record, spec: { ...spec, variables: [{ name: "service", kind: "query", from: "spans", field: "service" }] } };
    const { host, rerender, client } = await render();
    const input = () => host.querySelector<HTMLInputElement>('[aria-label="Dashboard variables"] input:not([type="hidden"])')!;
    await vi.waitFor(() => expect(input().value).toBe("checkout"), { interval: 5, timeout: 3000 });
    let resolve!: (response: Response) => void;
    variableResponse = () => new Promise((done) => { resolve = done; });
    await rerender({ range: "6h" }, false);
    await vi.waitFor(() => expect(input().value).toBe("checkout"), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(queryBodies).toHaveLength(1), { interval: 5, timeout: 3000 });
    await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.click(); });
    await vi.waitFor(() => expect(queryBodies).toHaveLength(1), { interval: 5, timeout: 3000 });
    await act(async () => { resolve(json({ options: { service: [{ value: "cart" }] } })); });
    await settle(client);
    await vi.waitFor(() => expect(input().value).toBe("cart"), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(queryBodies).toHaveLength(2), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(queryBodies[1]).toMatchObject({ vars: { service: "cart" } }), { interval: 5, timeout: 3000 });
  });

  it("omits an empty query option list from panel variables instead of sending an empty string", async () => {
    servedRecord = { ...record, spec: { ...spec, variables: [{ name: "service", kind: "query", from: "spans", field: "service" }] } };
    variableResponse = async () => json({ options: { service: [] } });
    await render();
    await vi.waitFor(() => expect(queryBodies).toHaveLength(1), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect((queryBodies[0] as { vars: unknown }).vars).toEqual({}), { interval: 5, timeout: 3000 });
  });

  it("uses validated variable values for both the bar and panels without rewriting the URL", async () => {
    servedRecord = { ...record, spec: { ...spec, variables: [{ name: "service", kind: "custom", options: ["checkout", "cart"] }] } };
    const { host, onSearch } = await render({ vars: { service: "missing" } });
    await vi.waitFor(() => expect(queryBodies).toHaveLength(1), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(queryBodies[0]).toMatchObject({ vars: { service: "checkout" } }), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(host.querySelector<HTMLInputElement>('[aria-label="Dashboard variables"] input:not([type="hidden"])')!.value).toBe("checkout"), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(host.textContent).toContain("Latency for checkout"), { interval: 5, timeout: 3000 });
    expect(onSearch).not.toHaveBeenCalled();
  });

  it("renders every ApiError problem path, message and optional hint", async () => {
    panelResponse = async () => json({ message: "Invalid dashboard", problems: [
      { path: "panels[0].query", message: "bad measure", hint: "Use count()" },
      { path: "variables[0].field", message: "unknown field" },
    ] }, 400);
    const { host } = await render();
    await vi.waitFor(() => expect(host.textContent).toContain("panels[0].query: bad measure (Use count())"), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(host.textContent).toContain("variables[0].field: unknown field"), { interval: 5, timeout: 3000 });
  });

  it("drops old panel results on a range change, including panels absent from the new response", async () => {
    const { host, rerender, client } = await render();
    await vi.waitFor(() => expect(host.textContent).toContain("No spans match service = 'cart'."), { interval: 5, timeout: 3000 });
    let resolve!: (response: Response) => void;
    panelResponse = () => new Promise((done) => { resolve = done; });
    await rerender({ range: "6h" }, false);
    await vi.waitFor(() => expect(host.textContent).not.toContain("No spans match service = 'cart'."), { interval: 5, timeout: 3000 });
    await act(async () => { resolve(json({ results: [{ id: "requests", status: "ok", elapsed_ms: 1 }] })); });
    await settle(client);
    await vi.waitFor(() => expect(queryBodies).toHaveLength(2), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(host.textContent).not.toContain("No spans match service = 'cart'."), { interval: 5, timeout: 3000 });
  });

  it("passes query signals to fetch and aborts superseded panel and variable requests", async () => {
    variableResponse = () => new Promise(() => {});
    panelResponse = () => new Promise(() => {});
    const { rerender } = await render({}, "d1", false);
    await vi.waitFor(() => expect(fetchMock.mock.calls.filter(([input]) => /\/api\/(panels\/query|variables\/resolve)$/.test(String(input)))).toHaveLength(2), { interval: 5, timeout: 3000 });
    const signals = fetchMock.mock.calls.filter(([input]) => /\/api\/(panels\/query|variables\/resolve)$/.test(String(input)))
      .map(([, init]) => init?.signal);
    expect(signals).toHaveLength(2);
    expect(signals.every((signal) => signal instanceof AbortSignal)).toBe(true);
    await rerender({ range: "6h" }, false);
    expect(signals.every((signal) => signal?.aborted)).toBe(true);
  });

  it("updates a text variable input when URL state changes", async () => {
    servedRecord = { ...record, spec: { ...spec, variables: [{ name: "service", kind: "text" }] } };
    const { host, rerender } = await render({ vars: { service: "checkout" } });
    await vi.waitFor(() => expect(host.querySelector<HTMLInputElement>('[aria-label="Dashboard variables"] input')!.value).toBe("checkout"), { interval: 5, timeout: 3000 });
    await rerender({ vars: { service: "cart" } }, false);
    await vi.waitFor(() => expect(host.querySelector<HTMLInputElement>('[aria-label="Dashboard variables"] input')!.value).toBe("cart"), { interval: 5, timeout: 3000 });
    await rerender({ vars: { service: "checkout" } });
    await vi.waitFor(() => expect(host.querySelector<HTMLInputElement>('[aria-label="Dashboard variables"] input')!.value).toBe("checkout"), { interval: 5, timeout: 3000 });
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
    await settle(client);
    expect(client.getQueryData(["dashboard", "d1"])).toEqual(servedRecord);
    await vi.waitFor(() => expect(queryBodies).toHaveLength(2), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(queryBodies[1]).toMatchObject({ panels: ["requests"] }), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(host.textContent).not.toContain("Panels could not be loaded"), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(host.textContent).not.toContain("Latency for checkout"), { interval: 5, timeout: 3000 });
  });

  it("never sends an unknown URL view id even after visibility changes", async () => {
    const { host, client } = await render({ view: "gone" });
    await act(async () => { visibility([], {} as IntersectionObserver); });
    await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.click(); });
    await settle(client);
    await vi.waitFor(() => expect(queryBodies).toHaveLength(2), { interval: 5, timeout: 3000 });
    for (const body of queryBodies) expect((body as { panels: string[] }).panels).toEqual(["latency", "requests"]);
    await vi.waitFor(() => expect(host.textContent).not.toContain("Panels could not be loaded"), { interval: 5, timeout: 3000 });
  });

  it("preserves results and avoids requests and loaders after a layout-only version save", async () => {
    const { host, client } = await render();
    await vi.waitFor(() => expect(host.textContent).toContain("120"), { interval: 5, timeout: 3000 });
    const calls = fetchMock.mock.calls.length;
    await act(async () => { client.setQueryData(["dashboard", "d1"], { ...record, version: 5, spec: { ...spec, panels: spec.panels.map((panel) => ({ ...panel, grid: { ...panel.grid, w: 6 } })) } }); });
    await settle(client);
    await vi.waitFor(() => expect(queryBodies).toHaveLength(1), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(fetchMock.mock.calls).toHaveLength(calls), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(host.textContent).toContain("120"), { interval: 5, timeout: 3000 });
    expect(host.querySelector('[aria-label="Refreshing"]')).toBeNull();
    // Panel-relevant edits must still discard old results and fetch.
    panelResponse = () => new Promise(() => {});
    await act(async () => { client.setQueryData(["dashboard", "d1"], { ...record, version: 6, spec: { ...spec, panels: spec.panels.map((panel) => ({ ...panel, title: "Changed" })) } }); });
    await vi.waitFor(() => expect(queryBodies).toHaveLength(2), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(host.textContent).not.toContain("120"), { interval: 5, timeout: 3000 });
  });

  it("refreshes only the visible in-flight panel headers", async () => {
    const { host } = await render();
    const hidden = host.querySelector('.react-grid-item[data-panel="latency"]')!;
    await act(async () => { visibility([{ target: hidden, isIntersecting: false } as IntersectionObserverEntry], {} as IntersectionObserver); });
    panelResponse = () => new Promise(() => {});
    await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.click(); });
    await vi.waitFor(() => expect(queryBodies).toHaveLength(2), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect((queryBodies.at(-1) as { panels: string[] }).panels).toEqual(["requests"]), { interval: 5, timeout: 3000 });
    await vi.waitFor(() => expect(host.querySelector('[data-panel="requests"] [aria-label="Refreshing"]')).not.toBeNull(), { interval: 5, timeout: 3000 });
    expect(host.querySelector('[data-panel="latency"] [aria-label="Refreshing"]')).toBeNull();
  });

  it.each(["panels", "variables"])("does not retry a 400 from %s", async (kind) => {
    if (kind === "panels") panelResponse = async () => json({ message: "Invalid" }, 400);
    else variableResponse = async () => json({ message: "Invalid" }, 400);
    const { client } = await render();
    await settle(client);
    const path = kind === "panels" ? "/api/panels/query" : "/api/variables/resolve";
    await vi.waitFor(() => expect(fetchMock.mock.calls.filter(([input]) => String(input) === path)).toHaveLength(1), { interval: 5, timeout: 3000 });
  });
});


it("keeps scrolled-out results for the same key when another panel refreshes", async () => {
  const { host, rerender, client } = await render();
  const hidden = host.querySelector('.react-grid-item[data-panel="latency"]')!;
  await act(async () => { visibility([{ target: hidden, isIntersecting: false } as IntersectionObserverEntry], {} as IntersectionObserver); });
  panelResponse = async () => json({ results: [{ id: "requests", status: "ok", frame, elapsed_ms: 9 }] });
  await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.click(); });
  await settle(client);
  await vi.waitFor(() => expect(host.textContent).toContain("No spans match service = 'cart'."), { interval: 5, timeout: 3000 });
  panelResponse = () => new Promise(() => {});
  await rerender({ vars: { service: "cart" } }, false);
  await vi.waitFor(() => expect(host.textContent).not.toContain("No spans match service = 'cart'."), { interval: 5, timeout: 3000 });
});


it.each(["panels", "variables"])("retries server errors at most twice for %s", async (kind) => {
  if (kind === "panels") panelResponse = async () => json({ message: "Server failed" }, 500);
  else variableResponse = async () => json({ message: "Server failed" }, 500);
  const { host, client } = await render();
  await settle(client);
  const path = kind === "panels" ? "/api/panels/query" : "/api/variables/resolve";
  await vi.waitFor(() => expect(fetchMock.mock.calls.filter(([input]) => String(input) === path)).toHaveLength(3), { interval: 5, timeout: 3000 });
  await vi.waitFor(() => expect(host.textContent).toContain("Server failed"), { interval: 5, timeout: 3000 });
});


it("marks older panels stale after a failed refresh and clears stale on recovery", async()=>{
 const {host,client}=await render();
 // Network idle precedes React Query's scheduled render under CPU load.
 // A disabled loading button cannot accept the test's manual refresh.
 await vi.waitFor(async () => {
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)); });
  expect(host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.disabled).toBe(false);
  expect(host.querySelector('[data-panel="requests"]')!.textContent).toContain("120");
 }, { interval: 5, timeout: 3000 });
 panelResponse=async()=>json({message:"Refresh failed"},400);
 await act(async()=>{host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.click();});await settle(client);
 await vi.waitFor(() => expect(host.textContent).toContain("Panels could not be loaded"), { interval: 5, timeout: 3000 });await vi.waitFor(() => expect(host.textContent).toContain("120"), { interval: 5, timeout: 3000 });
 await vi.waitFor(() => expect(host.querySelector('[data-panel="requests"]')!.textContent).toContain("Stale: last updated"), { interval: 5, timeout: 3000 });
 panelResponse=async()=>defaultPanels();
 await act(async()=>{host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.click();});await settle(client);
 await vi.waitFor(() => expect(host.textContent).not.toContain("Stale: last updated"), { interval: 5, timeout: 3000 });
});

it("never retries a 504 panel batch",async()=>{
 panelResponse=async()=>json({message:"Time limit"},504);const {client}=await render();await settle(client);
 await vi.waitFor(() => expect(queryBodies).toHaveLength(1), { interval: 5, timeout: 3000 });
});

it("keeps explicit None selected in the URL and outgoing request",async()=>{
 servedRecord={...record,spec:{...spec,variables:[{name:"service",kind:"custom",options:["checkout","cart"],multi:true}]}};
 const {host}=await render({vars:{service:[]}});
 await vi.waitFor(() => expect(queryBodies[0]).toMatchObject({vars:{service:[]}}), { interval: 5, timeout: 3000 });
 expect(host.querySelector('[placeholder="None"]')).not.toBeNull();
});


it("keeps only the failed panel stale when the next partial refresh succeeds",async()=>{
 const {host,client}=await render();
 await vi.waitFor(async()=>{
  await act(async()=>{await new Promise(resolve=>setTimeout(resolve,0));});
  expect(host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.disabled).toBe(false);
  expect(host.querySelector('[data-panel="requests"]')!.textContent).toContain("120");
 },{interval:5,timeout:3000});
 panelResponse=async()=>json({results:[{id:"requests",status:"error",error:"DuckDB failed",elapsed_ms:3},{id:"latency",status:"empty",diagnosis:"empty",elapsed_ms:2}]});
 await act(async()=>{host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.click();});await settle(client);
 await vi.waitFor(() => expect(host.querySelector('[data-panel="requests"]')!.textContent).toContain("Stale: last updated"), { interval: 5, timeout: 3000 });
 await vi.waitFor(() => expect(host.querySelector('[data-panel="requests"]')!.textContent).toContain("120"), { interval: 5, timeout: 3000 });
 await vi.waitFor(() => expect(host.textContent).toContain("Panels could not be loaded"), { interval: 5, timeout: 3000 });
 const hidden=host.querySelector('.react-grid-item[data-panel="requests"]')!;
 await act(async()=>{visibility([{target:hidden,isIntersecting:false} as IntersectionObserverEntry],{} as IntersectionObserver);});
 panelResponse=async()=>json({results:[{id:"latency",status:"empty",diagnosis:"still empty",elapsed_ms:2}]});
 await vi.waitFor(async()=>{
  await act(async()=>{await new Promise(resolve=>setTimeout(resolve,0));});
  expect(host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.disabled).toBe(false);
 },{interval:5,timeout:3000});
 await act(async()=>{host.querySelector<HTMLButtonElement>('[aria-label="Refresh now"]')!.click();});await settle(client);
 await vi.waitFor(() => expect(host.querySelector('[data-panel="requests"]')!.textContent).toContain("Stale: last updated"), { interval: 5, timeout: 3000 });
 await vi.waitFor(() => expect(host.querySelector('[data-panel="latency"]')!.textContent).not.toContain("Stale:"), { interval: 5, timeout: 3000 });
});


it("pushes drill and click variables atomically while preserving the captured panel window", async () => {
  servedRecord = { ...record, spec: { ...spec, panels: [{ ...spec.panels[1], title: "Services", drill: "traces", click: { set_variable: "service" }, query: { from: "spans", measures: ["count()"], by: ["service"] } }] } };
  panelResponse = async () => json({ results: [{ id: "latency", status: "ok", frame: { columns: [...frame.columns, { name: "service", type: "string", role: "dimension" }], values: [...frame.values, ["cart", "cart"]], rows: 2 }, elapsed_ms: 1, interval: "1m", from_ms: 0, to_ms: 10000 }] });
  const initial: DashboardSearch = { range: "1h", compare: "1", vars: { other: "kept" } };
  const { host, onSearch, rerender, client } = await render(initial);
  // The grid renders once its container width is measured; wait for the chart's select control.
  const select = await vi.waitFor(() => { const button = host.querySelector<HTMLButtonElement>('[aria-label="Select Services: time series"]'); expect(button).not.toBeNull(); return button!; }, { interval: 5, timeout: 3000 });
  await act(async () => select.click());
  expect(onSearch).toHaveBeenCalledOnce();
  const next = onSearch.mock.calls[0][0] as DashboardSearch;
  expect(onSearch.mock.calls[0][1]).toBe(false);
  expect(next).toMatchObject({ ...initial, vars: { other: "kept", service: "cart" } });
  const target = JSON.parse(next.drill!);
  expect(target).toMatchObject({ panel_id: "latency", kind: "traces", from: new Date(1000).toISOString(), to: new Date(10000).toISOString(), window_from: new Date(0).toISOString(), window_to: new Date(10000).toISOString(), dimensions: { service: "cart" } });
  const implementation = fetchMock.getMockImplementation()!;
  fetchMock.mockImplementation(async (input, init) => String(input) === "/api/panels/exemplars" ? json({ traces: [] }) : implementation(input, init));
  await rerender(next); await settle(client);
  const request = fetchMock.mock.calls.find(([url]) => String(url) === "/api/panels/exemplars")!;
  expect(JSON.parse(String(request[1]?.body))).toMatchObject({ kind: "traces", from: target.from, to: target.to, time: { from: target.window_from, to: target.window_to, refresh: "off" }, vars: { service: "cart" } });
  await vi.waitFor(() => expect(document.body.textContent).toContain("No exemplar traces match this selection."), { interval: 5, timeout: 3000 });
});
