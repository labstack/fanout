import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import type { ComponentProps } from "react";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { FragmentView } from "./fragment-view";
import type { PanelCard } from "./panel-card";
import type { VariableBar } from "./variable-bar";
import type { DrillDrawer } from "./drill";
import { fixture } from "../mcp-apps/fixtures";
import type { PanelFragment } from "../../../panels/fragment";
const ui = vi.hoisted(() => ({ card: null as unknown as ComponentProps<typeof PanelCard>, bar: null as unknown as ComponentProps<typeof VariableBar>, drill: null as unknown as ComponentProps<typeof DrillDrawer> }));
vi.mock("./panel-card", () => ({ PanelCard: (props: ComponentProps<typeof PanelCard>) => { ui.card = props; return <div data-card={props.panel.id} />; } }));
vi.mock("./variable-bar", () => ({ VariableBar: (props: ComponentProps<typeof VariableBar>) => { ui.bar = props; return <div data-bar />; } }));
vi.mock("./drill", () => ({ DrillDrawer: (props: ComponentProps<typeof DrillDrawer>) => { ui.drill = props; return <div data-drill={props.target?.trace_id} />; } }));
beforeEach(() => vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true));
afterEach(() => vi.unstubAllGlobals());
async function mount(fragment: PanelFragment, resolveVariables = vi.fn().mockResolvedValue({}), onQuery = vi.fn().mockResolvedValue(fragment)) {
  const el = document.createElement("div"); document.body.append(el); const root = createRoot(el);
  const client = { exemplars: vi.fn(), trace: vi.fn() };
  await act(async () => root.render(<MantineProvider><FragmentView fragment={fragment} dark={false} onQuery={onQuery} drillClient={client} resolveVariables={resolveVariables} /></MantineProvider>));
  return { el, onQuery, async cleanup() { await act(async () => root.unmount()); el.remove(); } };
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
it("uses the clicked result window and preserves closed drills after refresh completion", async () => {
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
it("loads query variable choices through the adapter before rendering VariableBar", async () => {
  const f = fixture(); f.dashboard.variables = [{ name: "service", kind: "query", from: "spans", field: "service" }];
  let finish!: (v: Record<string, { value: string }[]>) => void;
  const resolveVariables = vi.fn().mockImplementation(() => new Promise(resolve => { finish = resolve; }));
  const view = await mount(f, resolveVariables);
  try {
    expect(view.el.querySelector('[data-bar]')).toBeNull(); expect(view.onQuery).not.toHaveBeenCalled();
    expect(resolveVariables.mock.lastCall![0]).toEqual({ dashboard: f.dashboard, time: f.dashboard.time, vars: {} });
    expect(resolveVariables.mock.lastCall![1]).toBeInstanceOf(AbortSignal);
    await act(async () => finish({ service: [{ value: "checkout" }] }));
    expect(view.el.querySelector('[data-bar]')).not.toBeNull(); expect(ui.bar.options.service).toEqual([{ value: "checkout" }]);
    await act(async () => (view.el.querySelector('[aria-label="Refresh panels"]') as HTMLButtonElement).click());
    expect(view.onQuery.mock.lastCall![0].vars).toEqual({ service: "checkout" });
  } finally { await view.cleanup(); }
});
