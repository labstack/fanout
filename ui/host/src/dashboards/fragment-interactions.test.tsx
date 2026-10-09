import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { ComponentProps } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { FragmentView } from "./fragment-view";
import type { PanelCard } from "./panel-card";
import type { VariableBar } from "./variable-bar";
import type { DrillDrawer } from "./drill";
import { fixture, presetFixture } from "../../tests/fixtures";
import type { PanelFragment } from "../../../panels/fragment";
const ui = vi.hoisted(() => ({ cards: {} as Record<string, ComponentProps<typeof PanelCard>>, card: null as unknown as ComponentProps<typeof PanelCard>, bar: null as unknown as ComponentProps<typeof VariableBar>, drill: null as unknown as ComponentProps<typeof DrillDrawer> }));
vi.mock("./panel-card", () => ({ PanelCard: (props: ComponentProps<typeof PanelCard>) => { ui.card = props; ui.cards[props.panel.id] = props; return <div data-card={props.panel.id} data-suspended={props.suspended || undefined} />; } }));
vi.mock("./variable-bar", () => ({ VariableBar: (props: ComponentProps<typeof VariableBar>) => { ui.bar = props; return <div data-bar />; } }));
vi.mock("./drill", () => ({ DrillDrawer: (props: ComponentProps<typeof DrillDrawer>) => { ui.drill = props; return <div data-drill={props.target?.trace_id} />; } }));
beforeEach(() => vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true));
afterEach(() => { vi.useRealTimers(); vi.unstubAllGlobals(); });
async function mount(fragment: PanelFragment, resolveVariables = vi.fn().mockResolvedValue({}), onQuery = vi.fn().mockResolvedValue(fragment)) {
  const el = document.createElement("div"); document.body.append(el); const root = createRoot(el);
  const queryClient = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const client = { exemplars: vi.fn(), trace: vi.fn() };
  await act(async () => root.render(<MantineProvider><QueryClientProvider client={queryClient}><FragmentView fragment={fragment} dark={false} onQuery={onQuery} drillClient={client} resolveVariables={resolveVariables} /></QueryClientProvider></MantineProvider>));
  return { el, onQuery, async cleanup() { await act(async () => root.unmount()); el.remove(); queryClient.clear(); } };
}
it("queries one complete batch for each variable change, preserving scalar/All/empty/multi", async () => {
  const f = fixture(); f.dashboard.variables = [{ name: "service", kind: "custom", options: ["checkout", "cart"], multi: true, include_all: true }];
  const view = await mount(f);
  try {
    for (const value of ["checkout", "$__all", [], ["checkout", "cart"]]) {
      const before = view.onQuery.mock.calls.length;
      await act(async () => ui.bar.onChange("service", value));
      expect(view.onQuery).toHaveBeenCalledTimes(before + 1);
      expect(view.onQuery.mock.lastCall![0].vars.service).toEqual(value);
      expect(view.onQuery.mock.lastCall![0]).not.toHaveProperty("panels");
    }
  } finally { await view.cleanup(); }
});
it("deduplicates linked zooms into one absolute batch and restores the prior time", async () => {
  const f = fixture(); f.dashboard.panels[0].viz = "timeseries";
  const view = await mount(f);
  try {
    await act(async () => { ui.card.onZoom!(1000, 5000); ui.card.onZoom!(1000, 5000); });
    expect(view.onQuery).toHaveBeenCalledOnce();
    expect(view.onQuery.mock.lastCall![0].time).toEqual({ from: "1970-01-01T00:00:01.000Z", to: "1970-01-01T00:00:05.000Z", refresh: "off", compare: undefined });
    expect(ui.card.zoomed).toBe(true);
    await act(async () => ui.card.onZoomReset!());
    expect(view.onQuery).toHaveBeenCalledTimes(2); expect(view.onQuery.mock.lastCall![0].time).toEqual(f.dashboard.time);
    expect(ui.card.zoomed).toBe(false);
  } finally { await view.cleanup(); }
});
it("uses the clicked result window and preserves closed drills after query completion", async () => {
  const f = fixture(); const p = f.dashboard.panels[0]; p.drill = "traces"; p.click = { set_variable: "service" };
  f.dashboard.variables = [{ name: "service", kind: "text" }];
  let finish!: (f: PanelFragment) => void;
  const onQuery = vi.fn().mockImplementation(() => new Promise<PanelFragment>(resolve => { finish = resolve; }));
  const view = await mount(f, vi.fn(), onQuery);
  try {
    expect(ui.card.onSelect).toBeUndefined();
    await act(async () => ui.card.onPoint!({ trace_id: "abc", namespace: "shop", dimensions: { service: "checkout" } }));
    expect(onQuery).toHaveBeenCalledOnce(); expect(onQuery.mock.lastCall![0].vars).toEqual({ service: "checkout" });
    expect(ui.drill.target).toMatchObject({ trace_id: "abc", namespace: "shop", window_from: "1970-01-01T00:00:00.000Z", window_to: "1970-01-01T01:00:00.000Z" });
    await act(async () => ui.drill.onChange(undefined));
    await act(async () => finish(f));
    expect(ui.drill.target).toBeUndefined();
  } finally { await view.cleanup(); }
});
it("simple click filtering is applied only once through grid callback precedence", async () => {
  const f = fixture(); f.dashboard.panels[0].viz = "bar"; f.dashboard.panels[0].click = { set_variable: "service" };
  const view = await mount(f);
  try {
    await act(async () => { ui.card.onPoint!({ dimensions: { service: "checkout" } }); ui.card.onSelect!("checkout"); });
    expect(view.onQuery).toHaveBeenCalledOnce();
  } finally { await view.cleanup(); }
});
it("keeps VariableBar mounted while query choices load through the adapter", async () => {
  const f = fixture(); f.dashboard.variables = [{ name: "service", kind: "query", from: "spans", field: "service" }];
  let finish!: (v: Record<string, { value: string }[]>) => void;
  const resolveVariables = vi.fn().mockImplementation(() => new Promise(resolve => { finish = resolve; }));
  const view = await mount(f, resolveVariables);
  try {
    expect(view.el.querySelector('[data-bar]')).not.toBeNull(); expect(view.onQuery).not.toHaveBeenCalled();
    expect(resolveVariables.mock.lastCall![0]).toEqual({ dashboard: f.dashboard, time: f.dashboard.time, vars: {} });
    expect(resolveVariables.mock.lastCall![1]).toBeInstanceOf(AbortSignal);
    await act(async () => finish({ service: [{ value: "checkout" }] }));
    await vi.waitFor(async () => { await act(async () => {}); expect(ui.bar.options.service).toEqual([{value:"checkout"}]); });
    expect(view.el.querySelector('[data-bar]')).not.toBeNull(); expect(ui.bar.options.service).toEqual([{ value: "checkout" }]);
    await act(async () => ui.bar.onChange("service", ui.drill.vars.service ?? "checkout"));
    expect(view.onQuery.mock.lastCall![0].vars).toEqual({ service: "checkout" });
  } finally { await view.cleanup(); }
});

it("keeps options and the picker mounted with one resolution per content/time/vars", async () => {
  const f = fixture(); f.dashboard.variables = [{ name: "service", kind: "query", multi: true, from: "spans", field: "service" }];
  const resolver = vi.fn().mockResolvedValue({ service: [{ value: "checkout" }, { value: "cart" }] });
  const onQuery = vi.fn().mockImplementation(async () => structuredClone(f));
  const view = await mount(f, resolver, onQuery);
  try {
    const bar = view.el.querySelector('[data-bar]');
    expect(resolver).toHaveBeenCalledOnce();
    await act(async () => ui.bar.onChange("service", ["checkout"]));
    expect(view.el.querySelector('[data-bar]')).toBe(bar);
    expect(resolver).toHaveBeenCalledTimes(2);
    await act(async () => ui.bar.onChange("service", ui.drill.vars.service ?? "checkout"));
    expect(resolver).toHaveBeenCalledTimes(2);
    expect(view.el.querySelector('[data-bar]')).toBe(bar);
  } finally { await view.cleanup(); }
});
it.each([false, true])("does not attach point handlers for empty trace columns (formatted=%s)", async formatted => {
  const f = fixture(); f.dashboard.panels[0].viz = "table";
  f.dashboard.panels[0].options = formatted ? { columns: [{ field: "trace", format: "trace_link" }] } : undefined;
  f.results[0].frame = { rows: 2, columns: [{ name: formatted ? "trace" : "trace_id", type: "string", role: "dimension" }], values: [["", null]] };
  const view = await mount(f);
  try { expect(ui.card.onPoint).toBeUndefined(); } finally { await view.cleanup(); }
});
it("uses the same resolved variables for a batch and drill", async () => {
  const f = fixture(); f.dashboard.variables = [{ name: "service", kind: "custom", options: ["checkout"], default: "checkout" }, { name: "missing", kind: "query" }];
  const view = await mount(f);
  try {
    expect(ui.drill.vars).toEqual({ service: "checkout" });
    await act(async () => ui.bar.onChange("service", ui.drill.vars.service ?? "checkout"));
    expect(view.onQuery.mock.lastCall![0].vars).toEqual(ui.drill.vars);
  } finally { await view.cleanup(); }
});
it("never polls persisted chat answers even when the authored refresh is enabled", async () => {
  vi.useFakeTimers(); const f = fixture(); f.dashboard.time.refresh = "30s";
  const view = await mount(f);
  try { await act(async () => vi.advanceTimersByTime(120000)); expect(view.onQuery).not.toHaveBeenCalled(); }
  finally { await view.cleanup(); }
});
it("keeps the grid card with only one active full-screen body and tracks viewport resize", async () => {
  const view = await mount(fixture()); const original = window.innerHeight;
  try {
    await act(async () => ui.card.onView!());
    expect(view.el.querySelector('[data-card]')?.getAttribute('data-suspended')).toBe('true');
    expect(document.body.querySelectorAll('[data-card]:not([data-suspended])')).toHaveLength(1);
    await act(async () => { window.innerHeight = 750; window.dispatchEvent(new Event("resize")); });
    expect(ui.card.height).toBe(610);
  } finally { window.innerHeight = original; await view.cleanup(); }
});

it("shares one zoom group and batches all performance panels when brushing a sibling", async () => {
  const f = presetFixture("performance"); const view = await mount(f);
  try {
    const latency = ui.cards.latency, errors = ui.cards.errors, requests = ui.cards.requests;
    expect(latency.group).toBe(errors.group); expect(errors.group).toBe(requests.group);
    await act(async () => { latency.onZoom!(100000, 200000); errors.onZoom!(100000, 200000); });
    expect(view.onQuery).toHaveBeenCalledTimes(1);
    expect(view.onQuery.mock.lastCall![0].dashboard.panels.map((p: { id: string }) => p.id)).toEqual(["latency", "errors", "requests", "endpoints"]);
    for (const id of ["latency", "errors", "requests"]) expect(ui.cards[id].zoomed).toBe(true);
    await act(async () => ui.cards.requests.onZoomReset!());
    expect(view.onQuery.mock.lastCall![0].time).toEqual(f.dashboard.time);
    expect(ui.cards.latency.zoomed).toBe(false);
  } finally { await view.cleanup(); }
});
