import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { DashboardSpec, PanelResult } from "../../../panels/types";
import { PanelGrid } from "./grid";
import { DashboardPage } from "./page";
import { parseSearch, toSearchParams } from "./search";

vi.mock("../app-context", async (importOriginal) => ({ ...await importOriginal<typeof import("../app-context")>(), useFanoutApp: () => ({ agentAvailable: true, openChat: vi.fn() }) }));
vi.mock("../auth", () => ({ useViewer: () => ({ role: "viewer" }), authorizedFetch: (input: RequestInfo | URL, init?: RequestInit) => fetch(input, init) }));

const viz = vi.hoisted(() => ({ props: vi.fn() }));
vi.mock("./viz", () => ({ Viz: (props: { result: PanelResult }) => { viz.props(props); return <div data-active-viz>{props.result?.id}</div>; } }));
const spec: DashboardSpec = { version: 1, name: "Observed", time: { range: "1h" }, panels: [{ id: "latency", title: "Latency", viz: "timeseries", query: { from: "spans", measures: ["count()"] } }] };
const result: PanelResult = { id: "latency", status: "ok", from_ms: 0, to_ms: 3600000, elapsed_ms: 1, frame: { columns: [], values: [], rows: 1, truncated: true }, previous: { columns: [], values: [], rows: 1 } };
const cleanups: (() => void)[] = [];
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("ResizeObserver", class {
    constructor(private callback: ResizeObserverCallback) {}
    observe(target: Element) { this.callback([{ target, contentRect: { width: 1200 } } as ResizeObserverEntry], this as unknown as ResizeObserver); }
    unobserve() {}
    disconnect() {}
  });
  viz.props.mockClear();
});
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(fn => fn())); vi.unstubAllGlobals(); document.body.innerHTML = ""; });
async function mount(view?: string, dark = false, observed = result) {
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host), client = new QueryClient();
  cleanups.push(() => { root.unmount(); client.clear(); });
  const onView = vi.fn(), onOpenChat = vi.fn();
  const draw = async (view?: string) => act(async () => root.render(<MantineProvider forceColorScheme={dark ? "dark" : "light"}><QueryClientProvider client={client}><PanelGrid dashboardId="board" version={7} spec={spec} results={new Map([[result.id, observed]])} vars={{ service: ["checkout"] }} fetching={false} editing={false} canManage agentAvailable onOpenChat={onOpenChat} onView={onView} onVariable={vi.fn()} onVisible={vi.fn()} view={view} staleAt={new Map([[result.id, 1]])} time={{ range: "1h", compare: "previous_period" }} /></QueryClientProvider></MantineProvider>));
  await draw(view);
  return { host, draw, onView, onOpenChat };
}
it.each([false, true])("uses one active visualization and the identical observed result in full-screen (dark=%s)", async dark => {
  const { host, draw } = await mount(undefined, dark);
  expect(viz.props.mock.lastCall![0].result).toBe(result);
  await draw("latency");
  expect(document.querySelectorAll("[data-active-viz]")).toHaveLength(1);
  expect(host.querySelector('[data-panel="latency"]')).not.toBeNull();
  expect(host.querySelector("[data-active-viz]")).toBeNull();
  const dialog = document.querySelector('[role="dialog"]')!;
  expect(dialog.getAttribute("aria-label")).toBe("Latency");
  expect(viz.props.mock.lastCall![0]).toMatchObject({ result, compare: true });
  expect(viz.props.mock.lastCall![0].result).toBe(result);
  expect(dialog.textContent).toContain("Stale:");
  expect(dialog.textContent).toContain("Truncated:");
  expect(document.activeElement).toBe(dialog.querySelector('[aria-label="Close panel view"]'));
  const height = viz.props.mock.lastCall![0].height;
  vi.stubGlobal("innerHeight", window.innerHeight + 100);
  await act(async () => window.dispatchEvent(new Event("resize")));
  expect(viz.props.mock.lastCall![0].height).toBe(height + 100);
});
it("returns focus to the grid menu on Escape and direct-link close", async () => {
  const { host, draw, onView } = await mount("latency");
  const close = document.querySelector<HTMLButtonElement>('[aria-label="Close panel view"]')!;
  await act(async () => close.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
  expect(onView).toHaveBeenLastCalledWith(undefined);
  await draw(undefined);
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 250)); });
  expect(document.activeElement).toBe(host.querySelector('[aria-label="Latency menu"]'));
  expect(document.querySelectorAll("[data-active-viz]")).toHaveLength(1);
});
it("shows an explicit missing panel dialog for an unknown shared view", async () => {
  await mount("gone");
  const dialog = document.querySelector('[role="dialog"]');
  expect(dialog?.textContent).toContain("This panel is missing");
  expect(dialog?.querySelector("[data-active-viz]")).toBeNull();
});
it.each(["empty", "error"] as const)("preserves %s state and separate answer/edit actions in full-screen", async status => {
  const { onOpenChat } = await mount("latency", false, { ...result, status, diagnosis: "No rows here", error: "Invalid measure" });
  const dialog = document.querySelector('[role="dialog"]')!;
  expect(dialog.textContent).toContain(status === "empty" ? "No rows here" : "Invalid measure");
  await act(async () => dialog.querySelector<HTMLButtonElement>('[aria-label="Latency menu"]')!.click());
  await act(async () => [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(el => el.textContent === "Explain in chat")!.click());
  expect(onOpenChat.mock.calls[0][1]).toEqual({ answer_only: true });
  expect(onOpenChat.mock.calls[0][0]).not.toContain("Please fix");
  if (status === "error") {
    await act(async () => [...dialog.querySelectorAll<HTMLButtonElement>("button")].find(el => el.textContent === "Ask Fanout to fix it")!.click());
    expect(onOpenChat.mock.calls[1][1]).toBeUndefined();
  }
});

it.each(["Back", "Escape then Back"])("opens shared URL state (%s) without a query or lost filters", async mode => {
  let queries = 0;
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL) => {
    const path = new URL(String(input), "http://localhost").pathname;
    if (path === "/api/dashboards") return Response.json({ dashboards: [{ id: "board", name: "Observed" }] });
    if (path === "/api/dashboards/board") return Response.json({ id: "board", version: 7, spec: { ...spec, time: { ...spec.time, refresh: "off" } } });
    if (path === "/api/panels/query") { queries++; return Response.json({ results: [result] }); }
    if (path === "/api/annotations") return Response.json({ deploys: [], anomalies: [] });
    return Response.json({ options: {} });
  }));
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const rootRoute = createRootRoute();
  const route = createRoute({ getParentRoute: () => rootRoute, path: "/dashboards/$dashboardId", validateSearch: parseSearch, component: () => {
    const search = route.useSearch(), navigate = route.useNavigate();
    return <DashboardPage dashboardId="board" search={search} onSearch={(next, replace) => { void navigate({ search: toSearchParams(next), replace }); }} onOpen={() => {}} />;
  } });
  const initial = "/dashboards/board?from=2026-10-08T00%3A00%3A00.123456789Z&to=2026-10-08T01%3A00%3A00.987654321Z&compare=1&var-service=checkout";
  const history = createMemoryHistory({ initialEntries: [initial] });
  const router = createRouter({ routeTree: rootRoute.addChildren([route]), history });
  const host = document.createElement("div"); document.body.append(host); const root = createRoot(host);
  cleanups.push(() => { root.unmount(); client.clear(); });
  await act(async () => root.render(<MantineProvider><QueryClientProvider client={client}><RouterProvider router={router} /></QueryClientProvider></MantineProvider>));
  await vi.waitFor(async () => { await act(async () => { await new Promise(resolve => setTimeout(resolve, 0)); }); expect(host.querySelector("[data-active-viz]")).not.toBeNull(); });
  expect(queries).toBe(1);
  const menu = host.querySelector<HTMLButtonElement>('[aria-label="Latency menu"]')!;
  await act(async () => menu.click());
  await act(async () => [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(el => el.textContent === "View")!.click());
  await vi.waitFor(() => expect(router.state.location.search).toMatchObject({ view: "latency", compare: "1", vars: { service: "checkout" } }));
  expect(document.querySelector('[role="dialog"]')).not.toBeNull();
  expect(queries).toBe(1);
  if (mode === "Escape then Back") {
    await act(async () => document.querySelector<HTMLButtonElement>('[aria-label="Close panel view"]')!.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true })));
    await vi.waitFor(() => expect(router.state.location.search.view).toBeUndefined());
  }
  await act(async () => history.back());
  await vi.waitFor(() => expect(router.state.location.search.view).toBeUndefined());
  await act(async () => { await new Promise(resolve => setTimeout(resolve, 250)); });
  expect(document.querySelector('[role="dialog"]')).toBeNull();
  expect(document.activeElement).toBe(menu);
  expect(router.state.location.search).toMatchObject({ from: "2026-10-08T00:00:00.123456789Z", to: "2026-10-08T01:00:00.987654321Z", compare: "1", vars: { service: "checkout" } });
  expect(queries).toBe(1);
  // A shared view can be opened directly using the same address-bar contract.
  await act(async () => { await router.navigate({ to: "/dashboards/$dashboardId", params: { dashboardId: "board" }, search: toSearchParams({ ...router.state.location.search, view: "latency" }) }); });
  expect(document.querySelector('[role="dialog"]')?.getAttribute("aria-label")).toBe("Latency");
  expect(queries).toBe(1);
});
