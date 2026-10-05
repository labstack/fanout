import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act, type ReactNode } from "react";
import { createRoot } from "react-dom/client";
import type { Layout } from "react-grid-layout/legacy";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSpec } from "../../../panels/types";

type GridOptions = { children: ReactNode; layouts: { lg: Layout; sm: Layout }; isDraggable: boolean; isResizable: boolean; rowHeight: number; onBreakpointChange(breakpoint: string): void; onDragStop(next: Layout): void; onResizeStop(next: Layout): void; onLayoutChange?: (next: Layout) => void };
const grid = vi.hoisted(() => ({ current: undefined as GridOptions | undefined }));
vi.mock("react-grid-layout/legacy", () => ({
  WidthProvider: (component: unknown) => component,
  Responsive: (props: GridOptions) => { grid.current = props; return <div>{props.children}</div>; },
}));
import { PanelGrid, rowHeight } from "./grid";

const spec: DashboardSpec = { version: 1, name: "Notes", time: { range: "1h" }, panels: [
  { id: "note", title: "Note", viz: "text", content: "Hello", grid: { x: 3, y: 0, w: 6, h: 3 } },
] };
const record = { id: "d1", name: "Notes", version: 3, spec, is_default: false, description: "", created_at: "t", updated_at: "t" };
const cleanups: (() => void)[] = [];
const fetchMock = vi.fn<typeof fetch>();
const settle = () => act(async () => { await new Promise((resolve) => setTimeout(resolve, 20)); });
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  fetchMock.mockReset();
  fetchMock.mockResolvedValue(new Response(JSON.stringify(record), { headers: { "content-type": "application/json" } }));
});
afterEach(async () => {
  await act(async () => { cleanups.splice(0).forEach((cleanup) => cleanup()); });
  vi.unstubAllGlobals();
  document.body.innerHTML = "";
});
async function render(editing = true, dashboardSpec = spec) {
  const host = document.createElement("div");
  document.body.append(host);
  const root = createRoot(host);
  const client = new QueryClient();
  cleanups.push(() => { root.unmount(); client.clear(); });
  const onEditExit = vi.fn();
  const rerender = async (editing: boolean) => { await act(async () => { root.render(<MantineProvider><QueryClientProvider client={client}>
    <PanelGrid dashboardId="d1" version={2} spec={dashboardSpec} vars={{}} results={new Map()} fetching={false} editing={editing}
      agentAvailable={false} onOpenChat={vi.fn()} onVariable={vi.fn()} onView={vi.fn()} onVisible={vi.fn()} onEditExit={onEditExit} />
  </QueryClientProvider></MantineProvider>); }); };
  await rerender(editing);
  return { host, client, rerender, onEditExit, save: () => [...host.querySelectorAll<HTMLButtonElement>("button")].find((el) => el.textContent === "Save layout")! };
}

describe("dashboard layout editing", () => {
  it("uses 40px rows and keeps drag and resize disabled in view mode", async () => {
    await render(false);
    expect(rowHeight).toBe(40);
    expect(grid.current!.rowHeight).toBe(40);
    expect(grid.current!.isDraggable).toBe(false);
    expect(grid.current!.isResizable).toBe(false);
  });

  it("saves user drag and resize changes with the base version and updates the dashboard cache", async () => {
    const { client, save } = await render();
    expect(save().disabled).toBe(true);
    // Initial small-screen compaction must not turn the 12-column preview into an edit.
    await act(async () => { grid.current!.onLayoutChange?.(grid.current!.layouts.sm); });
    expect(save().disabled).toBe(true);
    await act(async () => { grid.current!.onDragStop([{ i: "note", x: 0, y: 3, w: 6, h: 3 }]); });
    await act(async () => { grid.current!.onResizeStop([{ i: "note", x: 0, y: 3, w: 9, h: 6 }]); });
    expect(save().disabled).toBe(false);
    await act(async () => { save().click(); });
    await settle();
    expect(fetchMock).toHaveBeenCalledTimes(1);
    const [url, init] = fetchMock.mock.calls[0];
    expect(url).toBe("/api/dashboards/d1");
    expect(init!.method).toBe("PUT");
    expect(JSON.parse(String(init!.body))).toMatchObject({ base_version: 2, message: "Edited layout", spec: { panels: [{ grid: { x: 0, y: 3, w: 9, h: 6 } }] } });
    expect(client.getQueryData(["dashboard", "d1"])).toEqual(record);
  });

  it("shows a version conflict without overwriting newer data", async () => {
    fetchMock.mockResolvedValue(new Response(JSON.stringify({ message: "Conflict" }), { status: 409, headers: { "content-type": "application/json" } }));
    const { host, client, save } = await render();
    await act(async () => { grid.current!.onDragStop([{ i: "note", x: 0, y: 3, w: 6, h: 3 }]); });
    await act(async () => { save().click(); });
    await settle();
    expect(host.textContent).toContain("Someone saved this dashboard since you opened it.");
    expect(client.getQueryData(["dashboard", "d1"])).toBeUndefined();
    expect(fetchMock).toHaveBeenCalledTimes(1);
  });

  it("removes a panel with a versioned patch only in edit mode", async () => {
    const { host, client } = await render();
    await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Note menu"]')!.click(); });
    const remove = [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find((el) => el.textContent === "Remove panel")!;
    await act(async () => { remove.click(); });
    await settle();
    const [, init] = fetchMock.mock.calls[0];
    expect(init!.method).toBe("PATCH");
    expect(JSON.parse(String(init!.body))).toEqual({ operations: [{ op: "remove_panel", id: "note" }], base_version: 2, message: "Removed a panel" });
    expect(client.getQueryData(["dashboard", "d1"])).toEqual(record);
  });
});


it("ignores narrow breakpoint stop events and never dirties or saves the compact layout", async () => {
  const { host, save } = await render();
  await act(async () => { grid.current!.onBreakpointChange("sm"); });
  expect(grid.current!.isDraggable).toBe(false);
  expect(grid.current!.isResizable).toBe(false);
  expect(host.textContent).toContain("only on a wider screen");
  await act(async () => { grid.current!.onDragStop(grid.current!.layouts.sm); grid.current!.onResizeStop(grid.current!.layouts.sm); });
  expect(save().disabled).toBe(true);
  await act(async () => { save().click(); grid.current!.onBreakpointChange("lg"); });
  expect(save().disabled).toBe(true);
  expect(grid.current!.layouts.lg[0]).toMatchObject({ x: 3, w: 6 });
  expect(fetchMock).not.toHaveBeenCalled();
});

it("loads the latest dashboard on conflict and clears errors when leaving edit mode", async () => {
  fetchMock.mockResolvedValue(new Response(JSON.stringify({ message: "Conflict" }), { status: 409, headers: { "content-type": "application/json" } }));
  const { host, client, save, rerender, onEditExit } = await render();
  const invalidate = vi.spyOn(client, "invalidateQueries");
  const conflict = async () => {
    await act(async () => { grid.current!.onDragStop([{ i: "note", x: 0, y: 3, w: 6, h: 3 }]); });
    await act(async () => { save().click(); });
    await settle();
    expect(host.textContent).toContain("Someone saved");
  };
  await conflict();
  await act(async () => { [...host.querySelectorAll<HTMLButtonElement>("button")].find((el) => el.textContent === "Load latest")!.click(); });
  await settle();
  expect(invalidate).toHaveBeenCalledWith({ queryKey: ["dashboard", "d1"] });
  expect(onEditExit).toHaveBeenCalledOnce();
  expect(host.textContent).not.toContain("Someone saved");
  await conflict();
  await rerender(false);
  await settle();
  expect(host.textContent).not.toContain("Someone saved");
  await rerender(true);
  expect(host.textContent).not.toContain("Someone saved");
});


it("keeps unsaved survivor grids when removing a panel", async () => {
 const two = { ...spec, panels: [...spec.panels, { ...spec.panels[0], id: "other", title: "Other", grid: { x: 0, y: 3, w: 6, h: 3 } }] };
 const { host } = await render(true, two);
 await act(async () => { grid.current!.onResizeStop([{ i: "note", x: 0, y: 0, w: 8, h: 3 }, { i: "other", x: 0, y: 3, w: 6, h: 3 }]); });
 await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Other menu"]')!.click(); });
 await act(async () => { [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(el => el.textContent === "Remove panel")!.click(); });
 await settle();
 const [,init] = fetchMock.mock.calls[0];
 expect(init!.method).toBe("PUT");
 expect(JSON.parse(String(init!.body))).toMatchObject({ base_version: 2, spec: { panels: [{ id: "note", grid: { x: 0, y: 0, w: 8, h: 3 } }] } });
});

it("duplicates with a unique id after the original in a user edit version", async () => {
 const {host}=await render();
 await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Note menu"]')!.click(); });
 const copy=[...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(el=>el.textContent==="Duplicate");
 expect(copy).toBeDefined();
 await act(async () => {copy!.click();});await settle();
 const [,init]=fetchMock.mock.calls[0];const body=JSON.parse(String(init!.body));
 expect(init!.method).toBe("PATCH");expect(body.base_version).toBe(2);
 expect(body.operations[0]).toMatchObject({op:"add_panel",after:"note",panel:{title:"Note (copy)",content:"Hello"}});
 expect(body.operations[0].panel.id).toMatch(/^[a-z][a-z0-9_]{0,39}$/);expect(body.operations[0].panel.id).not.toBe("note");
});

it("hides Duplicate when the user cannot edit", async()=>{
 const {host}=await render(false);
 await act(async()=>{host.querySelector<HTMLButtonElement>('[aria-label="Note menu"]')!.click();});
 expect([...document.querySelectorAll('[role="menuitem"]')].some(el=>el.textContent==="Duplicate")).toBe(false);
});

it("duplicates with unsaved layout in one PUT on plain HTTP", async () => {
 vi.stubGlobal("crypto", { randomUUID: undefined });
 const two = { ...spec, panels: [...spec.panels, { ...spec.panels[0], id: "other", title: "Other", grid: { x: 0, y: 3, w: 6, h: 3 } }] };
 const { host } = await render(true, two);
 await act(async () => { grid.current!.onResizeStop([{ i: "note", x: 0, y: 0, w: 8, h: 3 }, { i: "other", x: 0, y: 3, w: 6, h: 3 }]); });
 await act(async () => { host.querySelector<HTMLButtonElement>('[aria-label="Other menu"]')!.click(); });
 await act(async () => { [...document.querySelectorAll<HTMLButtonElement>('[role="menuitem"]')].find(el => el.textContent === "Duplicate")!.click(); });
 await settle();
 expect(fetchMock).toHaveBeenCalledOnce();
 const [, init] = fetchMock.mock.calls[0];
 const body = JSON.parse(String(init!.body));
 expect(init!.method).toBe("PUT");
 expect(body.base_version).toBe(2);
 expect(body.spec.panels).toHaveLength(3);
 expect(body.spec.panels[0]).toMatchObject({ id: "note", grid: { x: 0, y: 0, w: 8, h: 3 } });
 expect(body.spec.panels[1]).toMatchObject({ id: "other", grid: { x: 0, y: 3, w: 6, h: 3 } });
 expect(body.spec.panels[2]).toMatchObject({ title: "Other (copy)", content: "Hello" });
 expect(body.spec.panels[2].grid).toBeUndefined();
 expect(body.spec.panels[2].id).toMatch(/^[a-z][a-z0-9_]{0,39}$/);
 expect(two.panels.some(p => p.id === body.spec.panels[2].id)).toBe(false);
});
