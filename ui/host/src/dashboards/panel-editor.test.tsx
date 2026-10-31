import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { within } from "@testing-library/dom";
import { EditorView } from "@codemirror/view";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { DashboardSpec } from "../../../panels/types";
import PanelEditor from "./panel-editor";

const spec: DashboardSpec = { version: 1, name: "Checkout", time: { range: "3h" }, variables: [{ name: "service", kind: "custom", options: ["checkout"], default: "checkout" }], panels: [
  { id: "panel", title: "Requests", viz: "table", query: { from: "spans", measures: ["count()"], by: ["service"] } },
] };
const frame = { columns: [{ name: "service", type: "string" as const, role: "dimension" as const }, { name: "count", type: "number" as const, role: "measure" as const }], values: [["checkout"], [4]], rows: 1 };
const cleanups: (() => void)[] = [];
const fetchMock = vi.fn<typeof fetch>();
const json = (body: unknown, status = 200) => Response.json(body, { status });
const bodies = (url: string, method = "POST") => fetchMock.mock.calls.filter(([input, init]) => String(input) === url && (init?.method ?? "GET") === method).map(([, init]) => JSON.parse(String(init?.body)));
let patch: () => Promise<Response>;
let ranFrame: typeof frame;
let compatible: { viz: string; requires: string[] }[];

beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  fetchMock.mockReset();
  patch = async () => json({ id: "d1", version: 8, spec });
  compatible = [{ viz: "table", requires: [] }];
  ranFrame = frame;
  fetchMock.mockImplementation(async (input, init) => {
    const url = String(input);
    if (url === "/api/fanql/format") return json({ text: "spans | count() by service", exactness: "exact", editable: true, requires: [], notices: [] });
    if (url === "/api/fanql/parse") return json({ query: { from: "spans", measures: ["count()"], by: ["service"], where: ["service = $service"] }, canonical: "spans where service:$service | count() by service", viz: { compatible, edits: [] }, notices: [] });
    if (url === "/api/queries/run") return json({ results: [{ id: "explore", status: "ok", elapsed_ms: 2, frame: ranFrame }] });
    if (url === "/api/dashboards/d1" && (init?.method === "PATCH" || init?.method === "PUT")) return patch();
    throw new Error(`Unexpected request: ${init?.method ?? "GET"} ${url}`);
  });
});
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(cleanup => cleanup())); vi.unstubAllGlobals(); document.body.innerHTML = ""; });

async function render({ add = false, panel: edited = spec.panels[0], layoutChanged = false } = {}) {
  const panel = add ? undefined : edited;
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host), client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const onClose = vi.fn();
  await act(async () => root.render(<MantineProvider><QueryClientProvider client={client}><PanelEditor dashboardId="d1" version={7} spec={spec} time={{ range: "3h" }} vars={{ service: "checkout" }} panel={panel} layoutChanged={layoutChanged} onClose={onClose} /></QueryClientProvider></MantineProvider>));
  cleanups.push(() => { root.unmount(); client.clear(); });
  const ui = within(document.body);
  const wait = async (assertion: () => void) => vi.waitFor(async () => { await act(async () => {}); assertion(); }, { timeout: 3000, interval: 10 });
  return { ui, onClose, wait };
}

describe("PanelEditor", () => {
  it("opens a structured panel as FanQL, runs with the dashboard's variables and saves its query", async () => {
    const { ui, onClose, wait } = await render();
    await wait(() => expect(EditorView.findFromDOM(ui.getByRole("textbox", { name: "FanQL query" }))!.state.doc.toString()).toBe("spans | count() by service"));
    // An existing panel runs as soon as it opens.
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    expect(bodies("/api/fanql/parse")[0].variables).toEqual(spec.variables);
    expect(bodies("/api/queries/run")[0]).toMatchObject({ vars: { service: "checkout" }, dashboard: { variables: spec.variables } });
    await wait(() => expect(ui.getByRole<HTMLButtonElement>("button", { name: "Save panel" }).disabled).toBe(false));
    await act(async () => ui.getByRole("button", { name: "Save panel" }).click());
    await wait(() => expect(onClose).toHaveBeenCalled());
    expect(bodies("/api/dashboards/d1", "PATCH")[0]).toEqual({ operations: [{ op: "update_panel", id: "panel", set: { query: { from: "spans", measures: ["count()"], by: ["service"], where: ["service = $service"] } } }], base_version: 7, message: "Edited Requests" });
  });

  it("adds a new panel with a free id", async () => {
    const { ui, wait } = await render({ add: true });
    await wait(() => expect(ui.getByRole("textbox", { name: "FanQL query" })).toBeTruthy());
    const view = EditorView.findFromDOM(ui.getByRole("textbox", { name: "FanQL query" }))!;
    await act(async () => view.dispatch({ changes: { from: 0, insert: "spans | count() by service" } }));
    await act(async () => ui.getByRole("button", { name: "Run" }).click());
    await wait(() => expect(ui.getByRole<HTMLButtonElement>("button", { name: "Add panel" }).disabled).toBe(false));
    await act(async () => ui.getByRole("button", { name: "Add panel" }).click());
    await wait(() => expect(bodies("/api/dashboards/d1", "PATCH")).toHaveLength(1));
    expect(bodies("/api/dashboards/d1", "PATCH")[0].operations[0]).toMatchObject({ op: "add_panel", panel: { id: "panel_2", viz: "table" } });
  });

  it("drops settings tied to the old viz or columns when the query changes them", async () => {
    const stat = { id: "panel", title: "Requests", viz: "stat" as const, reduce: "last" as const, unit: "count" as const, options: { columns: [{ field: "count", format: "bar" as const }] }, click: { set_variable: "service" }, query: { from: "spans" as const, measures: ["count()"] } };
    const { ui, wait } = await render({ panel: stat });
    await wait(() => expect(ui.getByRole<HTMLButtonElement>("button", { name: "Save panel" }).disabled).toBe(false));
    const ran = bodies("/api/queries/run")[0].dashboard.panels[0];
    expect(ran).toMatchObject({ viz: "table", unit: "count" });
    expect(ran.reduce).toBeUndefined();
    expect(ran.options).toBeUndefined();
    await act(async () => ui.getByRole("button", { name: "Save panel" }).click());
    await wait(() => expect(bodies("/api/dashboards/d1", "PATCH")).toHaveLength(1));
    expect(bodies("/api/dashboards/d1", "PATCH")[0].operations[0].set).toEqual({ viz: "table", reduce: null, options: null, click: null, query: { from: "spans", measures: ["count()"], by: ["service"], where: ["service = $service"] } });
  });

  it("keeps a gauge a gauge when the panel has its minimum and maximum", async () => {
    compatible = [{ viz: "stat", requires: [] }, { viz: "gauge", requires: ["min_max"] }];
    const gauge = { id: "panel", title: "Errors", viz: "gauge" as const, min: 0, max: 100, query: { from: "spans" as const, measures: ["error_rate()"] } };
    const { wait } = await render({ panel: gauge });
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    expect(bodies("/api/queries/run")[0].dashboard.panels[0]).toMatchObject({ viz: "gauge", min: 0, max: 100 });
  });

  it("saves unsaved layout changes with the panel", async () => {
    const { ui, wait } = await render({ layoutChanged: true });
    await wait(() => expect(ui.getByRole<HTMLButtonElement>("button", { name: "Save panel" }).disabled).toBe(false));
    await act(async () => ui.getByRole("button", { name: "Save panel" }).click());
    await wait(() => expect(bodies("/api/dashboards/d1", "PUT")).toHaveLength(1));
    expect(bodies("/api/dashboards/d1", "PUT")[0]).toMatchObject({ base_version: 7, message: "Edited Requests and saved layout", spec: { panels: [{ id: "panel", title: "Requests", query: { where: ["service = $service"] } }] } });
  });

  it("edits a SQL panel in the SQL tab and keeps its viz", async () => {
    const sqlPanel = { id: "panel", title: "SQL requests", viz: "stat" as const, unit: "count" as const, sql: "SELECT count(*) AS total FROM spans WHERE $__window(start_time)" };
    const { ui, wait } = await render({ panel: sqlPanel });
    await wait(() => expect(ui.getByRole("textbox", { name: "Panel SQL" })).toBeTruthy());
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    expect(bodies("/api/queries/run")[0].dashboard.panels[0]).toMatchObject({ viz: "stat", unit: "count", sql: sqlPanel.sql });
    const view = EditorView.findFromDOM(ui.getByRole("textbox", { name: "Panel SQL" }))!;
    await act(async () => view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: "SELECT count(*) + 1 AS total FROM spans WHERE $__window(start_time)" } }));
    await act(async () => ui.getByRole("button", { name: "Run" }).click());
    await wait(() => expect(ui.getByRole<HTMLButtonElement>("button", { name: "Save panel" }).disabled).toBe(false));
    await act(async () => ui.getByRole("button", { name: "Save panel" }).click());
    await wait(() => expect(bodies("/api/dashboards/d1", "PATCH")).toHaveLength(1));
    expect(bodies("/api/dashboards/d1", "PATCH")[0].operations[0]).toEqual({ op: "update_panel", id: "panel", set: { sql: "SELECT count(*) + 1 AS total FROM spans WHERE $__window(start_time)" } });
  });

  it("saves an edited panel that is empty right now, but adds no empty panel", async () => {
    ranFrame = { ...frame, values: [[], []], rows: 0 };
    const edited = await render();
    await edited.wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    await edited.wait(() => expect(edited.ui.getByRole<HTMLButtonElement>("button", { name: "Save panel" }).disabled).toBe(false));
    await act(async () => cleanups.splice(0).forEach(cleanup => cleanup()));
    document.body.innerHTML = "";
    const added = await render({ add: true });
    await added.wait(() => expect(added.ui.getByRole("textbox", { name: "FanQL query" })).toBeTruthy());
    const view = EditorView.findFromDOM(added.ui.getByRole("textbox", { name: "FanQL query" }))!;
    await act(async () => view.dispatch({ changes: { from: 0, insert: "spans | count() by service" } }));
    await act(async () => added.ui.getByRole("button", { name: "Run" }).click());
    await added.wait(() => expect(bodies("/api/queries/run")).toHaveLength(2));
    expect(added.ui.getByRole<HTMLButtonElement>("button", { name: "Add panel" }).disabled).toBe(true);
  });

  it("asks before discarding a draft", async () => {
    const { ui, onClose, wait } = await render({ add: true });
    await wait(() => expect(ui.getByRole("textbox", { name: "FanQL query" })).toBeTruthy());
    await act(async () => ui.getByRole("button", { name: "Cancel" }).click());
    expect(onClose).toHaveBeenCalledTimes(1);
    const view = EditorView.findFromDOM(ui.getByRole("textbox", { name: "FanQL query" }))!;
    await act(async () => view.dispatch({ changes: { from: 0, insert: "spans" } }));
    await act(async () => ui.getByRole("button", { name: "Cancel" }).click());
    await wait(() => expect(ui.getByText("Discard changes?")).toBeTruthy());
    expect(onClose).toHaveBeenCalledTimes(1);
    await act(async () => ui.getByRole("button", { name: "Keep editing" }).click());
    expect(ui.queryByText("Discard changes?")).toBeNull();
    await act(async () => ui.getByRole("button", { name: "Cancel" }).click());
    await act(async () => ui.getByRole("button", { name: "Discard changes" }).click());
    expect(onClose).toHaveBeenCalledTimes(2);
  });

  it("explains a version conflict and stays open", async () => {
    patch = async () => json({ code: "dashboard_version_conflict", message: "conflict" }, 409);
    const { ui, onClose, wait } = await render();
    await wait(() => expect(ui.getByRole<HTMLButtonElement>("button", { name: "Save panel" }).disabled).toBe(false));
    await act(async () => ui.getByRole("button", { name: "Save panel" }).click());
    await wait(() => expect(ui.getByText(/Someone saved this dashboard since you opened it/)).toBeTruthy());
    expect(onClose).not.toHaveBeenCalled();
  });
});
