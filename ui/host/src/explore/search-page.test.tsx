import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { within } from "@testing-library/dom";
import { EditorView } from "@codemirror/view";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import type { ExploreSearch } from "./search";
import { SearchPage, logsPreset, tracesPreset, type Preset } from "./search-page";

const frame = { columns: [{ name: "body", type: "string" as const, role: "dimension" as const }], values: [["timeout"]], rows: 1 };
const cleanups: (() => void)[] = [];
const fetchMock = vi.fn<typeof fetch>();
const json = (body: unknown, status = 200) => Response.json(body, { status });
const bodies = (url: string) => fetchMock.mock.calls.filter(([input]) => String(input) === url).map(([, init]) => JSON.parse(String(init?.body)));
let parse: (body: { text: string; source: string }) => Promise<Response>;

beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  fetchMock.mockReset();
  parse = async body => json({ query: { from: body.source, ...(body.text ? { where: ["service = 'checkout'"] } : {}) }, canonical: body.source, viz: { compatible: [], edits: [] }, notices: [] });
  fetchMock.mockImplementation(async (input, init) => {
    const url = String(input);
    if (url === "/api/fanql/parse") return parse(JSON.parse(String(init?.body)));
    if (url === "/api/queries/run") return json({ results: [{ id: "search", status: "ok", elapsed_ms: 3, frame }] });
    if (url === "/api/fanql/complete") return json({ replace: { start: 0, end: 0 }, items: [], schema: { window: "1h", sampled: false, warm: true, recent: true } });
    throw new Error(`Unexpected request: ${url}`);
  });
});
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(cleanup => cleanup())); vi.unstubAllGlobals(); document.body.innerHTML = ""; });

async function render(preset: Preset, initial: Partial<ExploreSearch> = {}) {
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host), client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  let search: ExploreSearch = { q: "", range: "1h", tab: "query", ...initial };
  const onSearch = vi.fn((next: ExploreSearch) => { search = next; void draw(); });
  const draw = async () => act(async () => root.render(<MantineProvider><QueryClientProvider client={client}><SearchPage preset={preset} search={search} onSearch={onSearch} onSaved={vi.fn()} /></QueryClientProvider></MantineProvider>));
  cleanups.push(() => { root.unmount(); client.clear(); });
  await draw();
  const ui = within(host);
  const wait = async (assertion: () => void) => vi.waitFor(async () => { await act(async () => {}); assertion(); }, { timeout: 3000, interval: 10 });
  return { ui, wait, onSearch, search: () => search };
}

describe("SearchPage", () => {
  it("opens with the newest logs and runs the logs viz sorted by time", async () => {
    const { wait } = await render(logsPreset);
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    expect(bodies("/api/fanql/parse")[0]).toEqual({ text: "", source: "logs", viz: "logs" });
    expect(bodies("/api/queries/run")[0].dashboard.panels[0]).toMatchObject({ viz: "logs", query: { from: "logs", sort: "time" } });
  });

  it("parses typed filters against the page's source", async () => {
    const { ui, wait } = await render(tracesPreset);
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    const view = EditorView.findFromDOM(ui.getByRole("textbox", { name: "Search traces" }))!;
    await act(async () => view.dispatch({ changes: { from: 0, insert: "service:checkout" } }));
    await act(async () => ui.getByRole("button", { name: "Search" }).click());
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(2));
    expect(bodies("/api/fanql/parse")[1]).toEqual({ text: "service:checkout", source: "spans", viz: "traces" });
    expect(bodies("/api/queries/run")[1].dashboard.panels[0]).toMatchObject({ viz: "traces", query: { from: "spans", where: ["service = 'checkout'"], sort: "duration_ms" } });
  });

  it("reruns when the order changes", async () => {
    const { ui, wait, search } = await render(tracesPreset);
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    await act(async () => ui.getByRole("radio", { name: "Errors first" }).click());
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(2));
    expect(search().sort).toBe("errors");
    expect(bodies("/api/queries/run")[1].dashboard.panels[0].query.sort).toBe("errors");
  });

  it("shows the problem when a where is typed", async () => {
    parse = async body => body.text ? json({ code: "invalid_fanql", message: "The query has problems.", problems: [{ path: "text", message: "the page supplies where", span: { start: 0, end: 5 } }] }, 400) : json({ query: { from: "logs" }, canonical: "logs", viz: { compatible: [], edits: [] }, notices: [] });
    const { ui, wait } = await render(logsPreset, { q: "where service:a" });
    await wait(() => expect(ui.getByRole("alert").textContent).toBe("the page supplies where"));
  });
});
