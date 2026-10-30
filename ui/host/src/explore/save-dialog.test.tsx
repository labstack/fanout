import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, within } from "@testing-library/dom";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { Panel } from "../../../panels/types";
import { SaveDialog, defaultTitle, uniquePanelID } from "./save-dialog";

const panel: Panel = { id: "explore", title: "spans | count() by service", viz: "table", query: { from: "spans", measures: ["count()"], by: ["service"] } };
const summary = { id: "d1", name: "Checkout", description: "", is_default: false, version: 3, panel_count: 2, updated_at: "" };
const record = { id: "d1", name: "Checkout", description: "", is_default: false, version: 3, created_at: "", updated_at: "", spec: { version: 1, name: "Checkout", time: { range: "1h" }, panels: [{ id: "explore", title: "Old", viz: "stat" }, { id: "explore_2", title: "Older", viz: "stat" }] } };
const cleanups: (() => void)[] = [];
const fetchMock = vi.fn<typeof fetch>();
const json = (body: unknown, status = 200) => Response.json(body, { status });
const body = (url: string, method: string) => JSON.parse(String(fetchMock.mock.calls.find(([input, init]) => String(input) === url && init?.method === method)?.[1]?.body));
let dashboards: unknown[];
let patch: () => Promise<Response>;

beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  fetchMock.mockReset();
  dashboards = [summary];
  patch = async () => json({ ...record, version: 4 });
  fetchMock.mockImplementation(async (input, init) => {
    const url = String(input), method = init?.method ?? "GET";
    if (url === "/api/dashboards" && method === "GET") return json({ dashboards });
    if (url === "/api/dashboards" && method === "POST") return json({ ...record, id: "d9" }, 201);
    if (url === "/api/dashboards/d1" && method === "GET") return json(record);
    if (url === "/api/dashboards/d1" && method === "PATCH") return patch();
    throw new Error(`Unexpected request: ${method} ${url}`);
  });
});
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(cleanup => cleanup())); vi.unstubAllGlobals(); document.body.innerHTML = ""; });

async function render(time: { range?: string; from?: string; to?: string } = { range: "24h" }) {
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host), client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  const onSaved = vi.fn(), onClose = vi.fn();
  await act(async () => root.render(<MantineProvider><QueryClientProvider client={client}><SaveDialog panel={panel} time={time} onClose={onClose} onSaved={onSaved} /></QueryClientProvider></MantineProvider>));
  cleanups.push(() => { root.unmount(); client.clear(); });
  const ui = within(document.body);
  const wait = async (assertion: () => void) => vi.waitFor(async () => { await act(async () => {}); assertion(); }, { timeout: 3000, interval: 10 });
  const input = async (label: string, value: string) => act(async () => { fireEvent.change(ui.getByRole("textbox", { name: label }), { target: { value } }); });
  const click = async (name: string) => act(async () => ui.getByRole("button", { name }).click());
  return { ui, onSaved, wait, input, click };
}

async function choose(ui: ReturnType<typeof within>, name: string) {
  const select = ui.getByLabelText("Dashboard", { selector: "input" });
  await act(async () => { fireEvent.focus(select); fireEvent.click(select); fireEvent.keyDown(select, { key: "ArrowDown" }); });
  await vi.waitFor(() => ui.getByRole("option", { name, hidden: true }), { timeout: 2000 });
  await act(async () => { fireEvent.click(ui.getByRole("option", { name, hidden: true })); });
}

describe("save helpers", () => {
  it("picks a free panel id and shortens long titles", () => {
    expect(uniquePanelID("explore", [])).toBe("explore");
    expect(uniquePanelID("explore", ["explore", "explore_2"])).toBe("explore_3");
    expect(defaultTitle("x".repeat(100))).toHaveLength(80);
    expect(defaultTitle("short")).toBe("short");
    expect(Array.from(defaultTitle("😀".repeat(100)))).toHaveLength(80);
  });
});

describe("SaveDialog", () => {
  it("adds the panel to an existing dashboard at its current version with a free id", async () => {
    const { ui, onSaved, wait, click } = await render();
    await wait(() => expect(ui.getByLabelText("Dashboard", { selector: "input" })).toBeTruthy());
    await choose(ui, "Checkout");
    await click("Save");
    await wait(() => expect(onSaved).toHaveBeenCalledWith("d1"));
    expect(body("/api/dashboards/d1", "PATCH")).toEqual({ operations: [{ op: "add_panel", panel: { ...panel, id: "explore_3" } }], base_version: 3, message: "Added spans | count() by service from Explore" });
  });

  it("creates a new dashboard holding only this panel", async () => {
    const { ui, onSaved, wait, click, input } = await render();
    await wait(() => expect(ui.getByRole("radio", { name: "New dashboard" })).toBeTruthy());
    await act(async () => { fireEvent.click(ui.getByRole("radio", { name: "New dashboard" })); });
    expect(ui.getByRole<HTMLButtonElement>("button", { name: "Save" }).disabled).toBe(true);
    await input("Dashboard name", "Latency");
    await input("Panel title", "Requests by service");
    await click("Save");
    await wait(() => expect(onSaved).toHaveBeenCalledWith("d9"));
    expect(body("/api/dashboards", "POST")).toEqual({ spec: { version: 1, name: "Latency", time: { range: "24h" }, panels: [{ ...panel, title: "Requests by service" }] } });
  });

  it("keeps an absolute window on a new dashboard", async () => {
    dashboards = [];
    const { ui, onSaved, wait, click, input } = await render({ from: "2026-10-01T00:00:00Z", to: "2026-10-01T06:00:00Z" });
    await wait(() => expect(ui.getByRole("textbox", { name: "Dashboard name" })).toBeTruthy());
    await input("Dashboard name", "Incident");
    await click("Save");
    await wait(() => expect(onSaved).toHaveBeenCalledWith("d9"));
    expect(body("/api/dashboards", "POST").spec.time).toEqual({ from: "2026-10-01T00:00:00Z", to: "2026-10-01T06:00:00Z" });
  });

  it("asks for a name when there are no dashboards yet", async () => {
    dashboards = [];
    const { ui, wait } = await render();
    await wait(() => expect(ui.getByRole("textbox", { name: "Dashboard name" })).toBeTruthy());
    expect(ui.queryByRole("radio", { name: "Existing dashboard" })).toBeNull();
  });

  it("explains a version conflict and keeps the dialog open", async () => {
    patch = async () => json({ code: "dashboard_version_conflict", message: "conflict" }, 409);
    const { ui, onSaved, wait, click } = await render();
    await wait(() => expect(ui.getByLabelText("Dashboard", { selector: "input" })).toBeTruthy());
    await choose(ui, "Checkout");
    await click("Save");
    await wait(() => expect(ui.getByText(/Someone saved that dashboard a moment ago/)).toBeTruthy());
    expect(onSaved).not.toHaveBeenCalled();
  });
});
