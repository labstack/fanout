import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { within } from "@testing-library/dom";
import { EditorView } from "@codemirror/view";
import { diagnosticCount } from "@codemirror/lint";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import type { DashboardSpec, PanelResult } from "../../../panels/types";
import PanelSqlEditor from "./panel-sql-editor";

const original = "SELECT count(*) AS total FROM spans WHERE $__window(start_time)";
const edited = "SELECT count(*) + 1 AS total FROM spans WHERE $__window(start_time)";
const spec: DashboardSpec = { version: 1, name: "SQL dashboard", time: { range: "1h" }, variables: [{ name: "service", kind: "custom", options: ["checkout"], default: "checkout" }], panels: [
  { id: "notes", title: "Notes", viz: "text", content: "Keep this" },
  { id: "count", title: "Requests for $service", viz: "stat", sql: original, unit: "count", description: "Preserve all panel fields", grid: { x: 0, y: 3, w: 6, h: 3 } },
] };
const time = { from: "2026-10-01T12:00:00.000000001Z", to: "2026-10-01T13:00:00.000000001Z", compare: "previous_period" as const };
const vars = { service: "checkout" };
const frame = { columns: [{ name: "total", type: "number" as const, role: "measure" as const }], values: [[42]], rows: 1 };
const cleanups: (() => void)[] = [];
const fetchMock = vi.fn<typeof fetch>();
const json = (body: unknown, status = 200) => Response.json(body, { status });
let preview: () => Promise<Response>;
let save: () => Promise<Response>;
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  fetchMock.mockReset();
  preview = async () => json({ results: [{ id: "count", status: "ok", elapsed_ms: 3, frame }] });
  save = async () => json({ id: "d1", version: 8, spec: { ...spec, panels: spec.panels.map(p => p.id === "count" ? { ...p, sql: edited } : p) } });
  fetchMock.mockImplementation(async (input, init) => {
    if (String(input) === "/api/telemetry/schema") return json({ signals: { spans: { columns: [{ name: "start_time", type: "time" }] } }, sql_relations: {} });
    if (String(input) === "/api/queries/run") return preview();
    if (String(input) === "/api/dashboards/d1" && init?.method === "PATCH") return save();
    throw new Error(`Unexpected request: ${input}`);
  });
});
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(cleanup => cleanup())); vi.unstubAllGlobals(); document.body.innerHTML = ""; });

async function render() {
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host), client = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
  const onClose = vi.fn();
  const props = { dashboardId: "d1", version: 7, panel: spec.panels[1], spec, time, vars, onClose };
  const rerender = async (extra = {}) => act(async () => root.render(<MantineProvider><QueryClientProvider client={client}><PanelSqlEditor {...props} {...extra} /></QueryClientProvider></MantineProvider>));
  cleanups.push(() => { root.unmount(); client.clear(); });
  await rerender();
  const dialog = within(document.body).getByRole("dialog"), ui = within(dialog);
  const view = EditorView.findFromDOM(ui.getByRole("textbox", { name: "Panel SQL" }))!;
  const change = async (sql: string) => act(async () => view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: sql } }));
  const click = async (name: string) => act(async () => ui.getByRole("button", { name }).click());
  const wait = async (assertion: () => void) => vi.waitFor(async () => { await act(async () => {}); assertion(); }, { timeout: 3000, interval: 10 });
  return { ui, client, onClose, view, change, click, wait, rerender };
}

it("edits, runs through the existing query API, renders the result and saves only SQL at the captured version", async () => {
  const before = structuredClone(spec);
  const { ui, change, click, wait, client, onClose } = await render();
  expect((ui.getByRole("button", { name: "Save SQL" }) as HTMLButtonElement).disabled).toBe(true);
  await change(edited); await click("Run");
  await wait(() => expect(ui.getByText("42")).toBeDefined());
  const query = fetchMock.mock.calls.find(([url]) => String(url) === "/api/queries/run")!;
  expect(JSON.parse(String(query[1]?.body))).toEqual({ dashboard: { ...spec, panels: [spec.panels[0], { ...spec.panels[1], sql: edited }] }, panels: ["count"], time, vars, compare: true, widths: { count: 800 } });
  await click("Save SQL");
  await wait(() => expect(onClose).toHaveBeenCalledOnce());
  const patch = fetchMock.mock.calls.find(([, init]) => init?.method === "PATCH")!;
  expect(JSON.parse(String(patch[1]?.body))).toEqual({ operations: [{ op: "update_panel", id: "count", set: { sql: edited } }], base_version: 7, message: "Edited SQL for Requests for $service" });
  expect(client.getQueryData(["dashboard", "d1"])).toMatchObject({ version: 8 });
  expect(spec).toEqual(before);
});

it("keeps the dialog and draft when Escape leaves the SQL editor", async () => {
  const { ui, view, change, onClose } = await render();
  await change(edited);
  await act(async () => {
    view.focus();
    view.contentDOM.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
  });
  expect(onClose).not.toHaveBeenCalled();
  expect(within(document.body).getByRole("dialog").contains(ui.getByRole("textbox", { name: "Panel SQL" }))).toBe(true);
  expect(view.state.doc.toString()).toBe(edited);
  expect(document.activeElement).toBe(ui.getByRole("button", { name: "Run" }));
  expect(ui.queryByRole("alert")).toBeNull();
});

it.each(["Cancel", "Close SQL editor", "Escape", "overlay"])("confirms discarding a draft on %s without writing a version", async source => {
  const { ui, view, change, click, onClose } = await render();
  const close = async () => {
    if (source === "Escape") await act(async () => {
      const run = ui.getByRole("button", { name: "Run" });
      run.focus();
      run.dispatchEvent(new KeyboardEvent("keydown", { key: "Escape", bubbles: true, cancelable: true }));
    });
    else if (source === "overlay") await act(async () => (document.querySelector(".mantine-Modal-overlay") as HTMLElement).click());
    else await click(source);
  };
  await change(edited); await close();
  expect(onClose).not.toHaveBeenCalled();
  expect(ui.getByRole("alert").textContent).toContain("Discard SQL changes?");
  await click("Keep editing");
  expect(ui.queryByRole("alert")).toBeNull();
  expect(view.state.doc.toString()).toBe(edited);
  await close(); await click("Discard changes");
  expect(onClose).toHaveBeenCalledOnce(); expect(spec.panels[1].sql).toBe(original);
  expect(fetchMock.mock.calls.some(([, init]) => init?.method === "PATCH")).toBe(false);
});

it("closes an unchanged draft without a discard prompt", async () => {
  const { ui, click, onClose } = await render();
  await click("Cancel");
  expect(onClose).toHaveBeenCalledOnce();
  expect(ui.queryByRole("alert")).toBeNull();
});

it.each(["empty", "error"] as const)("uses the panel renderer's %s presentation", async status => {
  preview = async () => json({ results: [{ id: "count", status, elapsed_ms: 0, diagnosis: "No spans match this window.", error: "Unknown SQL field" } satisfies PanelResult] });
  const { ui, click, wait, view } = await render();
  await click("Run");
  await wait(() => expect(ui.getByText(status === "empty" ? "No spans match this window." : "Unknown SQL field")).toBeDefined());
  expect(diagnosticCount(view.state)).toBe(status === "error" ? 1 : 0);
});

it("shows server problems inline for this panel and clears them when editing", async () => {
  preview = async () => json({ code: "invalid_spec", message: "The dashboard spec is invalid.", problems: [{ path: "panels[1].sql", message: "Use $__window(start_time)", hint: "Filter time" }, { path: "panels[0].title", message: "Title required" }] }, 400);
  const { ui, click, change, wait, view } = await render();
  await click("Run");
  await wait(() => expect(diagnosticCount(view.state)).toBe(1));
  expect(ui.getByRole("alert").textContent).toContain("Title required");
  const problems = ui.getByRole("alert").querySelectorAll("li.panel-sql-problem");
  expect(Array.from(problems, problem => problem.textContent)).toEqual(["panels[1].sql: Use $__window(start_time) (Filter time)", "panels[0].title: Title required"]);
  expect(ui.getByRole("textbox").getAttribute("aria-invalid")).toBe("true");
  await change(edited); expect(diagnosticCount(view.state)).toBe(0); expect(ui.queryByRole("alert")).toBeNull();
});

it("retains the draft on version conflict and loads latest without overwriting it", async () => {
  save = async () => json({ code: "dashboard_version_conflict", message: "Conflict" }, 409);
  const { ui, change, click, wait, view, onClose, rerender } = await render();
  await change(edited);
  await rerender({ version: 8 });
  await click("Save SQL");
  await wait(() => expect(ui.getByRole("button", { name: "Load latest" })).toBeDefined());
  expect(view.state.doc.toString()).toBe(edited); expect(onClose).not.toHaveBeenCalled();
  await click("Run");
  await wait(() => expect(ui.getByText("42")).toBeDefined());
  expect((ui.getByRole("button", { name: "Save SQL" }) as HTMLButtonElement).disabled).toBe(true);
  await click("Load latest"); expect(onClose).not.toHaveBeenCalled();
  await click("Keep editing"); expect(view.state.doc.toString()).toBe(edited);
  await click("Load latest"); await click("Discard changes"); expect(onClose).toHaveBeenCalledOnce();
  expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PATCH")).toHaveLength(1);
});

it("prevents duplicate saves and retains server save problems in the editor", async () => {
  let finish!: (response: Response) => void;
  save = () => new Promise<Response>(resolve => { finish = resolve; });
  const { ui, change, click, wait, view, onClose } = await render();
  await change(edited);
  const button = ui.getByRole("button", { name: "Save SQL" });
  await act(async () => { button.click(); button.click(); });
  expect(fetchMock.mock.calls.filter(([, init]) => init?.method === "PATCH")).toHaveLength(1);
  await act(async () => finish(json({ code: "invalid_spec", message: "Invalid SQL", problems: [{ path: "panels[1].sql", message: "Unknown column" }] }, 400)));
  await wait(() => expect(diagnosticCount(view.state)).toBe(1));
  expect(onClose).not.toHaveBeenCalled();
  await change(original); expect((ui.getByRole("button", { name: "Save SQL" }) as HTMLButtonElement).disabled).toBe(true);
  await click("Cancel"); expect(onClose).toHaveBeenCalledOnce();
});

it("aborts an in-flight preview on unmount", async () => {
  let signal: AbortSignal | undefined;
  const pending = new Promise<Response>(() => {});
  fetchMock.mockImplementation(async (input, init) => {
    if (String(input) === "/api/telemetry/schema") return json({ signals: {}, sql_relations: {} });
    if (String(input) === "/api/queries/run") { signal = init?.signal ?? undefined; return pending; }
    return pending;
  });
  const { click } = await render();
  await click("Run"); expect(signal?.aborted).toBe(false);
  await act(async () => cleanups.splice(0).forEach(cleanup => cleanup()));
  expect(signal?.aborted).toBe(true);
});
