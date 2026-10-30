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

beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  fetchMock.mockReset();
  patch = async () => json({ id: "d1", version: 8, spec });
  fetchMock.mockImplementation(async (input, init) => {
    const url = String(input);
    if (url === "/api/fanql/format") return json({ text: "spans | count() by service", exactness: "exact", editable: true, requires: [], notices: [] });
    if (url === "/api/fanql/parse") return json({ query: { from: "spans", measures: ["count()"], by: ["service"], where: ["service = $service"] }, canonical: "spans where service:$service | count() by service", viz: { compatible: [{ viz: "table", requires: [] }], edits: [] }, notices: [] });
    if (url === "/api/queries/run") return json({ results: [{ id: "explore", status: "ok", elapsed_ms: 2, frame }] });
    if (url === "/api/dashboards/d1" && init?.method === "PATCH") return patch();
    throw new Error(`Unexpected request: ${init?.method ?? "GET"} ${url}`);
  });
});
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(cleanup => cleanup())); vi.unstubAllGlobals(); document.body.innerHTML = ""; });

async function render({ add = false } = {}) {
  const panel = add ? undefined : spec.panels[0];
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host), client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const onClose = vi.fn();
  await act(async () => root.render(<MantineProvider><QueryClientProvider client={client}><PanelEditor dashboardId="d1" version={7} spec={spec} time={{ range: "3h" }} vars={{ service: "checkout" }} panel={panel} onClose={onClose} /></QueryClientProvider></MantineProvider>));
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
    expect(bodies("/api/dashboards/d1", "PATCH")[0]).toEqual({ operations: [{ op: "update_panel", id: "panel", set: { query: { from: "spans", measures: ["count()"], by: ["service"], where: ["service = $service"] }, viz: "table" } }], base_version: 7, message: "Edited Requests" });
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

  it("explains a version conflict and stays open", async () => {
    patch = async () => json({ code: "dashboard_version_conflict", message: "conflict" }, 409);
    const { ui, onClose, wait } = await render();
    await wait(() => expect(ui.getByRole<HTMLButtonElement>("button", { name: "Save panel" }).disabled).toBe(false));
    await act(async () => ui.getByRole("button", { name: "Save panel" }).click());
    await wait(() => expect(ui.getByText(/Someone saved this dashboard since you opened it/)).toBeTruthy());
    expect(onClose).not.toHaveBeenCalled();
  });
});
