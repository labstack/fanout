import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, within } from "@testing-library/dom";
import { EditorView } from "@codemirror/view";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { NewDashboard } from "./new-dashboard";

const frame = { columns: [{ name: "service", type: "string" as const, role: "dimension" as const }, { name: "count", type: "number" as const, role: "measure" as const }], values: [["checkout"], [4]], rows: 1 };
const cleanups: (() => void)[] = [];
const fetchMock = vi.fn<typeof fetch>();
const json = (body: unknown, status = 200) => Response.json(body, { status });

beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  fetchMock.mockReset();
  fetchMock.mockImplementation(async (input, init) => {
    const url = String(input);
    if (url === "/api/fanql/parse") return json({ query: { from: "spans", measures: ["count()"], by: ["service"] }, canonical: "spans | count() by service", viz: { compatible: [{ viz: "table", requires: [] }], edits: [] }, notices: [] });
    if (url === "/api/queries/run") return json({ results: [{ id: "explore", status: "ok", elapsed_ms: 2, frame }] });
    if (url === "/api/dashboards" && init?.method === "POST") return json({ id: "d9", version: 1 }, 201);
    throw new Error(`Unexpected request: ${url}`);
  });
});
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(cleanup => cleanup())); vi.unstubAllGlobals(); document.body.innerHTML = ""; });

describe("NewDashboard", () => {
  it("creates a named dashboard with the first panel from a run", async () => {
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host), client = new QueryClient();
    const onCreated = vi.fn();
    await act(async () => root.render(<MantineProvider><QueryClientProvider client={client}><NewDashboard onClose={vi.fn()} onCreated={onCreated} /></QueryClientProvider></MantineProvider>));
    cleanups.push(() => { root.unmount(); client.clear(); });
    const ui = within(document.body);
    const wait = async (assertion: () => void) => vi.waitFor(async () => { await act(async () => {}); assertion(); }, { timeout: 3000, interval: 10 });
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "Dashboard name" }), { target: { value: "Checkout" } }); });
    const view = EditorView.findFromDOM(ui.getByRole("textbox", { name: "FanQL query" }))!;
    await act(async () => view.dispatch({ changes: { from: 0, insert: "spans | count() by service" } }));
    await act(async () => ui.getByRole("button", { name: "Run" }).click());
    await wait(() => expect(ui.getByRole("button", { name: "Create dashboard" }).getAttribute("aria-disabled")).toBe("false"));
    await act(async () => ui.getByRole("button", { name: "Create dashboard" }).click());
    await wait(() => expect(onCreated).toHaveBeenCalledWith("d9"));
    const post = fetchMock.mock.calls.find(([input, init]) => String(input) === "/api/dashboards" && init?.method === "POST")!;
    expect(JSON.parse(String(post[1]?.body))).toEqual({ spec: { version: 1, name: "Checkout", time: { range: "1h" }, panels: [{ id: "panel", title: "spans | count() by service", viz: "table", query: { from: "spans", measures: ["count()"], by: ["service"] }, drill: "traces" }] } });
  });

  it("cancels at once when nothing was entered, and asks before discarding a name or query", async () => {
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host), client = new QueryClient();
    const onClose = vi.fn();
    await act(async () => root.render(<MantineProvider><QueryClientProvider client={client}><NewDashboard onClose={onClose} onCreated={vi.fn()} /></QueryClientProvider></MantineProvider>));
    cleanups.push(() => { root.unmount(); client.clear(); });
    const ui = within(document.body);
    await act(async () => ui.getByRole("button", { name: "Cancel" }).click());
    expect(onClose).toHaveBeenCalledTimes(1);
    const field = ui.getByRole("textbox", { name: "Dashboard name" });
    await act(async () => { fireEvent.change(field, { target: { value: "Checkout" } }); });
    field.focus();
    await act(async () => { fireEvent.keyDown(field, { key: "Escape" }); });
    expect(ui.getByText("Discard changes?")).toBeTruthy();
    expect(onClose).toHaveBeenCalledTimes(1);
    await act(async () => ui.getByRole("button", { name: "Discard changes" }).click());
    expect(onClose).toHaveBeenCalledTimes(2);
  });
});
