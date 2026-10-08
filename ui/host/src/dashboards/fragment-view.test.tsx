import { act } from "react";
import { createRoot } from "react-dom/client";
import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { FragmentView } from "./fragment-view";
import { panelFragment } from "../../../panels/fragment";
import { fixture, traceFixture } from "../mcp-apps/fixtures";
import type { PanelFragment } from "../../../panels/fragment";

vi.mock("./echart-canvas", () => ({ EChartCanvas: ({ label }: { label: string }) => <div role="img" aria-label={label} /> }));
beforeEach(() => vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true));
afterEach(() => vi.unstubAllGlobals());
async function mount(fragment: PanelFragment, dark = false, onQuery = vi.fn().mockResolvedValue(fragment)) {
  const el = document.createElement("div"); document.body.append(el); const root = createRoot(el);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => root.render(<MantineProvider forceColorScheme={dark ? "dark" : "light"}><QueryClientProvider client={client}><FragmentView fragment={fragment} dark={dark} drillClient={{ exemplars: vi.fn(), trace: vi.fn() }} onQuery={onQuery} /></QueryClientProvider></MantineProvider>));
  return { el, onQuery, async cleanup() { await act(async () => root.unmount()); el.remove(); client.clear(); } };
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
it.each(["health", "service_map", "logs", "traces", "log_patterns"] as const)("decodes the %s preset, variables and trace detail", viz => {
  const raw = { ...fixture(viz), vars: { all: "$__all", scalar: "checkout", empty: [], multi: ["a", "b"] }, trace: traceFixture };
  expect(panelFragment(raw)).toEqual(raw);
});
it("decodes query_telemetry and errors/empty/truncated frames", () => {
  const raw = fixture(); raw.dashboard.panels[0] = { id: "p", title: "Count", viz: "stat", query: { from: "spans", measures: ["count()"] } };
  for (const status of ["ok", "empty", "error"] as const) expect(panelFragment({ ...raw, results: [{ ...raw.results[0], status, error: "safe error", frame: { columns: [], values: [], rows: 0, truncated: true, note: "Limited rows" } }] })).toBeDefined();
});
it.each([null, { data: {} }, { ...fixture(), results: [] }, { ...fixture(), results: [fixture().results[0], fixture().results[0]] }, { ...fixture(), results: [{ ...fixture().results[0], id: "other" }] }, { ...fixture(), vars: { service: [1] } }, { ...fixture(), vars: [] }, { ...fixture(), vars: null }])("rejects missing/duplicate/subset/old payloads and invalid vars (%j)", raw => {
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
it("keeps stale rows, exposes failures and ignores earlier refresh completions", async () => {
  const old = fixture(); old.results[0].diagnosis = "old rows";
  let finish!: (f: PanelFragment) => void;
  const onQuery = vi.fn().mockImplementationOnce(() => new Promise<PanelFragment>(resolve => { finish = resolve; })).mockResolvedValueOnce({ ...old, results: [{ ...old.results[0], diagnosis: "new rows" }] }).mockRejectedValueOnce(new Error("Bridge unavailable"));
  const view = await mount(old, false, onQuery);
  try {
    const refresh = view.el.querySelector<HTMLButtonElement>('[aria-label="Refresh panels"]')!;
    await act(async () => refresh.click());
    expect(view.el.textContent).toContain("old rows"); expect(view.el.textContent).toContain("Stale:");
    await act(async () => refresh.click());
    await act(async () => finish(old));
    expect(view.el.textContent).toContain("new rows");
    await act(async () => refresh.click());
    expect(view.el.textContent).toContain("Bridge unavailable"); expect(view.el.textContent).toContain("new rows"); expect(view.el.textContent).toContain("Stale:");
    expect(onQuery.mock.calls.every(([body]) => !Object.hasOwn(body, "panels"))).toBe(true);
  } finally { await view.cleanup(); }
});
