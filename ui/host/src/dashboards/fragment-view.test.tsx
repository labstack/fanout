import { act } from "react";
import { createRoot } from "react-dom/client";
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { FragmentView } from "./fragment-view";
import { panelFragment } from "../../../panels/fragment";
import { fixture, traceFixture, presets, presetFixture, assertPresetData } from "../../tests/fixtures";
import type { VariableResolver } from "./use-variables";
import type { PanelFragment } from "../../../panels/fragment";

vi.mock("./echart-canvas", () => ({ EChartCanvas: ({ label }: { label: string }) => <div role="img" aria-label={label} /> }));
beforeEach(() => vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true));
afterEach(() => vi.unstubAllGlobals());
it("sizes fragments with dashboard defaults and a readable map minimum", async () => {
  const { fragmentPanelHeight } = await import("./layout");
  expect(fragmentPanelHeight({ id: "map", title: "Map", viz: "service_map" })).toBeGreaterThanOrEqual(460);
  expect(fragmentPanelHeight({ id: "map", title: "Map", viz: "service_map", height: "s" })).toBeGreaterThanOrEqual(460);
  expect(fragmentPanelHeight({ id: "p", title: "P", viz: "logs", height: "l" })).toBeGreaterThan(fragmentPanelHeight({ id: "p", title: "P", viz: "logs" }));
});
it("shows only the panel title for one panel and no refresh for snapshot answers", async () => {
  const view = await mount(fixture());
  try {
    expect(view.el.querySelector('[data-fragment-header]')).toBeNull();
    expect(view.el.querySelector('[aria-label="Refresh panels"]')).toBeNull();
  } finally { await view.cleanup(); }
});
it("uses the authored multi-panel preset title without a legacy title decoder", async () => {
  const fragment = presetFixture("performance");
  const view = await mount(fragment);
  try { expect(view.el.querySelector('[data-fragment-header]')?.textContent).toBe("Service performance"); }
  finally { await view.cleanup(); }
});
async function mount(fragment: PanelFragment, dark = false, onQuery = vi.fn().mockResolvedValue(fragment), resolveVariables?: VariableResolver) {
  const el = document.createElement("div"); document.body.append(el); const root = createRoot(el);
  const drill = { exemplars: vi.fn(), trace: vi.fn().mockResolvedValue(traceFixture) };
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => root.render(<MantineProvider forceColorScheme={dark ? "dark" : "light"}><QueryClientProvider client={client}><FragmentView fragment={fragment} dark={dark} drillClient={drill} resolveVariables={resolveVariables} onQuery={onQuery} /></QueryClientProvider></MantineProvider>));
  return { el, onQuery, drill, async cleanup() { await act(async () => root.unmount()); el.remove(); client.clear(); } };
}
it.each([false, true])("uses PanelCard for spec/data/empty states and never fetches the host (dark=%s)", async dark => {
  const fetch = vi.spyOn(globalThis, "fetch"); const view = await mount(fixture(), dark);
  try {
    expect(view.el.textContent).toContain("No logs for checkout");
    expect(view.el.querySelector('[data-panel-view="Data"]')).not.toBeNull();
    await act(async () => (view.el.querySelector('[data-panel-view="Spec"]') as HTMLButtonElement).click());
    expect(view.el.textContent).toContain('"viz": "logs"');
    expect(fetch).not.toHaveBeenCalled();
  } finally { await view.cleanup(); fetch.mockRestore(); }
});
it.each(["health", "service_map", "logs", "traces", "log_patterns"] as const)("decodes the %s visualization, variables and trace detail", viz => {
  const raw = { ...fixture(viz), vars: { all: "$__all", scalar: "checkout", empty: [], multi: ["a", "b"] }, trace: traceFixture };
  expect(panelFragment(raw)).toEqual(raw);
});
it("decodes query_telemetry and errors/empty/truncated frames", () => {
  const raw = fixture(); raw.dashboard.panels[0] = { id: "p", title: "Count", viz: "stat", query: { from: "spans", measures: ["count()"] } };
  for (const status of ["ok", "empty", "error"] as const) expect(panelFragment({ ...raw, results: [{ ...raw.results[0], status, error: "safe error", frame: { columns: [], values: [], rows: 0, truncated: true, note: "Limited rows" } }] })).toBeDefined();
});
it.each([{ ...fixture(), trace: {} }, { ...fixture(), trace: { data: { spans: null, logs: [], services: [] } } }, null, { data: {} }, { ...fixture(), results: [] }, { ...fixture(), results: [fixture().results[0], fixture().results[0]] }, { ...fixture(), results: [{ ...fixture().results[0], id: "other" }] }, { ...fixture(), vars: { service: [1] } }, { ...fixture(), vars: [] }, { ...fixture(), vars: null }])("rejects missing/duplicate/subset/old payloads and invalid vars (%j)", raw => {
  expect(() => panelFragment(raw)).toThrow();
});
it.each(["empty", "error", "ok"] as const)("hides chat mutations and unsaved Copy link in %s menus and full-screen", async status => {
  const raw = fixture(); raw.results[0] = { ...raw.results[0], status, error: "Panel query failed", frame: { columns: [], values: [], rows: 0, truncated: true } };
  const view = await mount(raw);
  const forbidden = /Explain|Ask Fanout to fix it|Duplicate|Remove panel|Copy link/;
  try {
    await act(async () => (view.el.querySelector('[aria-label="Checkout logs menu"]') as HTMLButtonElement).click());
    expect(document.body.textContent).not.toMatch(forbidden);
    const open = [...document.body.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(el => el.textContent === "View")!;
    await act(async () => open.click());
    expect(document.body.querySelector('[role="dialog"]')).not.toBeNull();
    await act(async () => (document.body.querySelector('[role="dialog"] [aria-label="Checkout logs menu"]') as HTMLButtonElement).click());
    expect(document.body.textContent).not.toMatch(forbidden);
    expect(view.onQuery).not.toHaveBeenCalled();
  } finally { await view.cleanup(); }
});
it("keeps stale rows, exposes failures and ignores earlier variable query completions", async () => {
  const old = fixture(); old.dashboard.variables = [{name:"service",kind:"text"}]; old.results[0].diagnosis = "old rows";
  let finish!: (f: PanelFragment) => void;
  const onQuery = vi.fn().mockImplementationOnce(() => new Promise<PanelFragment>(resolve => { finish = resolve; })).mockResolvedValueOnce({ ...old, results: [{ ...old.results[0], diagnosis: "new rows" }] }).mockRejectedValueOnce(new Error("Bridge unavailable"));
  const view = await mount(old, false, onQuery);
  try {
    const change = (value: string) => { const input = view.el.querySelector<HTMLInputElement>("input")!; input.value=value; input.dispatchEvent(new FocusEvent("focusout",{bubbles:true})); };
    await act(async () => change(String(onQuery.mock.calls.length+1)));
    expect(view.el.textContent).toContain("old rows"); expect(view.el.textContent).toContain("Stale:");
    await act(async () => change(String(onQuery.mock.calls.length+1)));
    await act(async () => finish(old));
    expect(view.el.textContent).toContain("new rows");
    await act(async () => change(String(onQuery.mock.calls.length+1)));
    expect(view.el.textContent).toContain("Bridge unavailable"); expect(view.el.textContent).toContain("new rows"); expect(view.el.textContent).toContain("Stale:");
    expect(onQuery.mock.calls.every(([body]) => !Object.hasOwn(body, "panels"))).toBe(true);
  } finally { await view.cleanup(); }
});

it.each(["traces", "logs", "table"] as const)("renders iframe %s trace IDs as drill buttons without navigation URLs", async viz => {
  const raw = fixture(); raw.dashboard.panels[0].viz = viz;
  if (viz === "table") raw.dashboard.panels[0].options = { columns: [{ field: "trace_id", format: "trace_link" }] }; raw.dashboard.panels[0].query = { from: "spans" };
  raw.results[0] = { ...raw.results[0], status: "ok", frame: { rows: 1, columns: [{ name: "trace_id", type: "string", role: "dimension" }, { name: "namespace", type: "string", role: "dimension" }], values: [["abc"], ["shop"]] } };
  const view = await mount(raw);
  try {
    const trace = view.el.querySelector<HTMLButtonElement>(viz === "table" ? '[title="abc"]' : '[aria-label="Trace ID abc"]');
    expect(trace?.tagName).toBe("BUTTON"); expect(trace?.hasAttribute("href")).toBe(false);
    await act(async () => trace!.click());
    expect(view.drill.trace).toHaveBeenCalledWith(expect.objectContaining({ trace_id: "abc", namespace: "shop" }), expect.any(AbortSignal));
  } finally { await view.cleanup(); }
});

it.each(presets.flatMap(preset => [false, true].map(dark => ({ preset, dark }))))("renders the real $preset preset through FragmentView (dark=$dark)", async ({ preset, dark }) => {
  const raw = presetFixture(preset); expect(panelFragment(raw)).toEqual(raw);
  const view = await mount(raw, dark);
  try {
    expect(view.el.querySelectorAll("[data-panel]")).toHaveLength(raw.dashboard.panels.length);
    for (const panel of raw.dashboard.panels) expect(view.el.querySelector(`[data-panel="${panel.id}"]`)?.textContent).toContain(panel.title);
    assertPresetData(view.el, preset);
  } finally { await view.cleanup(); }
});
it("keeps performance siblings visible with only one focused visualization", async () => {
  const view = await mount(presetFixture("performance"));
  try {
    await act(async () => view.el.querySelector<HTMLButtonElement>('[aria-label="p95 latency menu"]')!.click());
    await act(async () => [...document.body.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(el => el.textContent === "View")!.click());
    expect(view.el.querySelector('[data-panel="latency"]')?.textContent).toContain("Shown in full-screen");
    expect(view.el.querySelector('[data-panel="endpoints"]')?.textContent).toContain("/cart");
    expect(document.body.querySelectorAll('[data-panel="latency"]')).toHaveLength(2);
    expect(document.body.querySelectorAll('[data-panel]')).toHaveLength(5);
    expect(view.onQuery).not.toHaveBeenCalled();
  } finally { await view.cleanup(); }
});

it("keeps a focused query MultiSelect and its dropdown open across option and fragment updates", async () => {
  const raw = fixture(); raw.dashboard.variables = [{ name: "service", kind: "query", from: "spans", field: "service", multi: true, include_all: true }];
  const resolveVariables = vi.fn().mockResolvedValue({ service: [{ value: "checkout" }, { value: "cart" }] });
  let finish!: (value: PanelFragment) => void;
  const onQuery = vi.fn().mockImplementation(() => new Promise<PanelFragment>(resolve => { finish = resolve; }));
  const view = await mount(raw, false, onQuery, resolveVariables);
  try {
    await act(async () => { await new Promise(resolve => setTimeout(resolve, 20)); });
    const bar = view.el.querySelector('[aria-label="Dashboard variables"]');
    const input = bar!.querySelector<HTMLInputElement>("input:not([type=hidden])")!;
    await act(async () => { input.focus(); input.click(); });
    const choice = [...document.body.querySelectorAll<HTMLElement>('[role="option"]')].find(option => option.textContent === "cart")!;
    expect(choice).toBeDefined();
    await act(async () => { choice.click(); await new Promise(resolve => setTimeout(resolve, 20)); });
    expect(document.activeElement).toBe(input);
    expect(view.el.querySelector('[aria-label="Dashboard variables"]')).toBe(bar);
    expect(document.body.querySelector('[role="listbox"]')).not.toBeNull();
    await act(async () => { finish(structuredClone(raw)); await new Promise(resolve => setTimeout(resolve, 20)); });
    expect(document.activeElement).toBe(input);
    expect(view.el.querySelector('[aria-label="Dashboard variables"]')).toBe(bar);
    expect(document.body.querySelector('[role="listbox"]')).not.toBeNull();
    expect(resolveVariables).toHaveBeenCalledTimes(2);
    expect(onQuery.mock.lastCall![0].vars).toEqual({ service: ["cart"] });
  } finally { await view.cleanup(); }
});

it.each(["service_map", "table"] as const)("names the %s dialog and returns focus on Escape without querying", async viz => {
 const raw = presetFixture(viz === "table" ? "performance" : "topology");
 const panel = raw.dashboard.panels.find(p => p.viz === viz)!;
 raw.dashboard.panels = [panel]; raw.results = raw.results.filter(r => r.id === panel.id);
 const view = await mount(raw);
 try {
  const menu = view.el.querySelector<HTMLButtonElement>('[aria-label$=" menu"]')!;
  await act(async () => menu.click());
  await act(async () => [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(el => el.textContent === "View")!.click());
  const dialog = document.querySelector('[role="dialog"]')!;
  expect(dialog.getAttribute("aria-label")).toBe(menu.getAttribute("aria-label")!.replace(/ menu$/, ""));
  expect(dialog.textContent).toContain(viz === "table" ? "/cart" : "checkout");
  await act(async () => dialog.querySelector<HTMLButtonElement>('[aria-label="Close panel view"]')!.dispatchEvent(new KeyboardEvent("keydown", {key:"Escape",bubbles:true})));
  await act(async () => { await new Promise(resolve => setTimeout(resolve,250)); });
  expect(document.querySelector('[role="dialog"]')).toBeNull();expect(document.activeElement).toBe(menu);expect(view.onQuery).not.toHaveBeenCalled();
 } finally { await view.cleanup(); }
});
