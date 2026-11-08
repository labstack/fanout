import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, within } from "@testing-library/dom";
import { EditorView } from "@codemirror/view";
import { forEachDiagnostic } from "@codemirror/lint";
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
    if (url === "/api/telemetry/schema") return json({ signals: {}, sql_relations: {}, services: [{ value: "checkout" }, { value: "frontend" }] });
    if (url.startsWith("/api/traces/")) return json({ summary: "", provenance: { window: "1h", generated_at: "", complete: true }, data: { trace_id: "abc", duration_ms: 5, has_error: false, services: ["checkout"], spans: [{ span_id: "s1", service: "checkout", operation: "GET /", kind: "server", start: "2026-10-30T10:00:00Z", duration_ms: 5, status: "ok" }], logs: [], span_count: 1, service_count: 1, truncated: false } });
    if (url === "/api/fanql/complete") return json({ replace: { start: 0, end: 0 }, items: [], schema: { window: "1h", sampled: false, warm: true, recent: true } });
    throw new Error(`Unexpected request: ${url}`);
  });
});
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(cleanup => cleanup())); vi.unstubAllGlobals(); document.body.innerHTML = ""; });

async function render(preset: Preset, initial: Partial<ExploreSearch> = {}, onExplore?: (q: string) => void) {
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host), client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  let search: ExploreSearch = { q: "", range: "1h", tab: "query", ...initial };
  const onSearch = vi.fn((next: ExploreSearch, _replace?: boolean) => { search = next; void draw(); });
  const draw = async () => act(async () => root.render(<MantineProvider><QueryClientProvider client={client}><SearchPage preset={preset} search={search} onSearch={onSearch} onSaved={vi.fn()} onExplore={onExplore} /></QueryClientProvider></MantineProvider>));
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
    expect(bodies("/api/fanql/parse")[0]).toEqual({ text: "", window: "1h", source: "logs", viz: "logs" });
    expect(bodies("/api/queries/run")[0].dashboard.panels[0]).toMatchObject({ viz: "logs", query: { from: "logs", sort: "time" } });
  });

  it("shows invalid panel problems at the search editor without a failed result", async () => {
    const fetch = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (input, init) => String(input) === "/api/queries/run"
      ? json({ results: [{ id: "search", status: "invalid", elapsed_ms: 0, problems: [{ path: "panels[0].query.where[0]", message: "unknown field http_route" }] }] })
      : fetch(input, init));
    const { ui, wait } = await render(logsPreset, { q: "http_route:a" });
    await wait(() => expect(ui.getByRole("alert").textContent).toBe("unknown field http_route"));
    const editor = ui.getByRole("textbox", { name: "Search logs" });
    expect(editor.getAttribute("aria-invalid")).toBe("true");
    expect(document.querySelector("[data-panel-error]")).toBeNull();
  });

  it("parses typed filters against the page's source", async () => {
    const { ui, wait } = await render(tracesPreset);
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    const view = EditorView.findFromDOM(ui.getByRole("textbox", { name: "Search traces" }))!;
    await act(async () => view.dispatch({ changes: { from: 0, insert: "service:checkout" } }));
    await act(async () => ui.getByRole("button", { name: "Search" }).click());
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(2));
    expect(bodies("/api/fanql/parse")[1]).toEqual({ text: "service:checkout", window: "1h", source: "spans", viz: "traces" });
    expect(bodies("/api/queries/run")[1].dashboard.panels[0]).toMatchObject({ viz: "traces", query: { from: "spans", where: ["service = 'checkout'"], sort: "duration_ms" } });
  });

  it("reruns when the order changes", async () => {
    const { ui, wait, search } = await render(tracesPreset);
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    // Errors only is a field, so the order offers start time instead of a second error filter.
    expect(ui.queryByRole("radio", { name: "Errors first" })).toBeNull();
    await act(async () => ui.getByRole("radio", { name: "Oldest" }).click());
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(2));
    expect(search().sort).toBe("+start");
    expect(bodies("/api/queries/run")[1].dashboard.panels[0].query.sort).toBe("+start");
  });

  it("adds a typed search to history and replaces the entry for reruns", async () => {
    const { ui, wait, onSearch } = await render(logsPreset);
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    expect(onSearch.mock.calls.at(-1)?.[1]).toBe(true);
    const view = EditorView.findFromDOM(ui.getByRole("textbox", { name: "Search logs" }))!;
    await act(async () => view.dispatch({ changes: { from: 0, insert: "timeout" } }));
    await act(async () => ui.getByRole("button", { name: "Search" }).click());
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(2));
    expect(onSearch.mock.calls.at(-1)).toEqual([expect.objectContaining({ q: "timeout" }), false]);
    await act(async () => ui.getByRole("button", { name: "Search" }).click());
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(3));
    expect(onSearch.mock.calls.at(-1)?.[1]).toBe(true);
  });

  it("says when services did not load and retries", async () => {
    let fail = true;
    const base = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (input, init) => String(input) === "/api/telemetry/schema" && fail ? json({ code: "timeout", message: "Gateway timeout" }, 504) : base(input, init));
    const { ui, wait } = await render(logsPreset);
    await wait(() => expect(ui.getByText(/Services did not load/)).toBeTruthy());
    fail = false;
    await act(async () => ui.getByRole("button", { name: "Retry" }).click());
    await wait(() => expect(ui.queryByText(/Services did not load/)).toBeNull());
  });

  it("words a search the server was too busy to start for this page", async () => {
    const base = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (input, init) => String(input) === "/api/queries/run" ? json({ results: [{ id: "search", status: "error", reason: "busy", retryable: true, elapsed_ms: 0, error: "Not run: other queries kept the server busy. Refresh to try again." }] }) : base(input, init));
    const { wait } = await render(tracesPreset);
    await wait(() => expect(document.body.textContent).toContain("Not run: other queries kept the server busy. Run the search again."));
    expect(document.body.textContent).not.toContain("Refresh to try again");
  });

  it("explains that body:x is exact while bare words match any part", async () => {
    const { ui } = await render(logsPreset);
    expect(ui.getByText(/Bare words match anywhere in the body; body:x matches the whole body/)).toBeTruthy();
  });

  it("shows the first rows and more on request", async () => {
    const base = fetchMock.getMockImplementation()!;
    const many = { columns: [{ name: "time", type: "time", role: "time" }, { name: "body", type: "string", role: "dimension" }], values: [Array.from({ length: 250 }, (_, i) => 1_800_000_000_000 - i), Array.from({ length: 250 }, (_, i) => `line ${i}`)], rows: 250 };
    fetchMock.mockImplementation(async (input, init) => String(input) === "/api/queries/run" ? json({ results: [{ id: "search", status: "ok", elapsed_ms: 1, frame: many }] }) : base(input, init));
    const { ui, wait } = await render(logsPreset);
    await wait(() => expect(document.querySelectorAll("[data-search-page] tbody tr")).toHaveLength(100));
    expect(ui.getByText("Showing 100 of 250 rows")).toBeTruthy();
    await act(async () => ui.getByRole("button", { name: "Show 100 more" }).click());
    expect(document.querySelectorAll("[data-search-page] tbody tr")).toHaveLength(200);
    await act(async () => ui.getByRole("button", { name: "Show 50 more" }).click());
    expect(document.querySelectorAll("[data-search-page] tbody tr")).toHaveLength(250);
    expect(ui.queryByText(/Showing/)).toBeNull();
  });

  it("opens a log's attributes, resource and trace under its row", async () => {
    const base = fetchMock.getMockImplementation()!;
    const logs = { columns: [{ name: "time", type: "time", role: "time" }, { name: "service", type: "string", role: "dimension" }, { name: "body", type: "string", role: "dimension" }, { name: "trace_id", type: "string", role: "dimension" }, { name: "namespace", type: "string", role: "dimension" }], values: [[1_800_000_000_000], ["checkout"], ["it's done"], ["abc"], ["shop"]], rows: 1 };
    const details = { columns: [{ name: "attributes", type: "json" }, { name: "resource", type: "json" }, { name: "span", type: "string" }, { name: "scope", type: "string" }], values: [['{"order.id":"42"}'], ['{"host.name":"pod-1"}'], ["s1"], ["checkout-lib"]], rows: 1 };
    fetchMock.mockImplementation(async (input, init) => String(input) === "/api/queries/run"
      ? json({ results: [JSON.parse(String(init?.body)).panels[0] === "log" ? { id: "log", status: "ok", elapsed_ms: 1, frame: details } : { id: "search", status: "ok", elapsed_ms: 1, from_ms: 1_799_999_000_000, to_ms: 1_800_000_100_000, frame: logs }] }) : base(input, init));
    const { ui, wait, search } = await render(logsPreset);
    await wait(() => expect(document.querySelectorAll("[data-search-page] tbody tr")).toHaveLength(1));
    const row = document.querySelector<HTMLElement>("[data-search-page] tbody tr")!;
    expect(within(row).getByRole("button", {name: "it's done"})).toBeTruthy();
    await act(async () => row.click());
    expect(within(row).getByRole("button", {name: "it's done"})).toBeTruthy();
    expect(row.querySelector("button[aria-expanded]")?.getAttribute("aria-expanded")).toBe("true");
    await wait(() => expect(document.querySelector("[data-log-details]")?.textContent).toContain("pod-1"));
    const sql: string = bodies("/api/queries/run").at(-1).dashboard.panels[0].sql;
    expect(sql).toContain("coalesce(body, '') = 'it''s done'");
    expect(sql).toContain("coalesce(namespace, '') = 'shop'");
    expect(bodies("/api/queries/run").at(-1).time).toMatchObject({ from: new Date(1_799_999_999_999).toISOString(), to: new Date(1_800_000_000_001).toISOString() });
    const panel = within(document.querySelector<HTMLElement>("[data-log-details]")!);
    expect(panel.getByText("order.id")).toBeTruthy(); expect(panel.getByText("s1")).toBeTruthy();
    await act(async () => panel.getByRole("button", { name: "abc" }).click());
    await wait(() => expect(JSON.parse(search().drill!)).toMatchObject({ trace_id: "abc", namespace: "shop" }));
  });

  it("excludes an operation from the traces with one click", async () => {
    const base = fetchMock.getMockImplementation()!;
    const traces = { columns: [{ name: "trace_id", type: "string", role: "dimension" }, { name: "service", type: "string", role: "dimension" }, { name: "operation", type: "string", role: "dimension" }, { name: "duration_ms", type: "number", role: "measure", unit: "ms" }], values: [["abc"], ["flagd"], ['flagd.Service/"EventStream"'], [600000]], rows: 1 };
    fetchMock.mockImplementation(async (input, init) => String(input) === "/api/queries/run" ? json({ results: [{ id: "search", status: "ok", elapsed_ms: 1, frame: traces }] }) : base(input, init));
    const { ui, wait, search } = await render(tracesPreset, { q: "a or b" });
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    await act(async () => ui.getByRole("button", { name: 'Exclude operation flagd.Service/"EventStream"' }).click());
    await wait(() => expect(bodies("/api/fanql/parse").at(-1).text).toBe('(a or b) -operation:"flagd.Service/""EventStream"""'));
    await wait(() => expect(search().q).toBe('(a or b) -operation:"flagd.Service/""EventStream"""'));
    expect(ui.getByRole("button", { name: "Exclude service flagd" })).toBeTruthy();
  });

  it("opens the searched spans by service in Explore without status or excluded terms", async () => {
    const onExplore = vi.fn();
    const where = ["(service = 'a' OR service = 'b')", "(service IS NULL OR service != 'cart')", "status = 'STATUS_CODE_ERROR'", "duration_ms > 500"];
    parse = async body => json({ query: { from: body.source, where }, canonical: body.text, viz: { compatible: [], edits: [] }, notices: [] });
    const base = fetchMock.getMockImplementation()!;
    fetchMock.mockImplementation(async (input, init) => String(input) === "/api/fanql/format"
      ? json({ text: "", exactness: "exact", editable: true, requires: [], notices: [], where: ["service:a or service:b", "-service:cart", "status:error", "duration>500ms"], measures: [] })
      : base(input, init));
    const { ui, wait } = await render(tracesPreset, { q: "(service:a or service:b) -service:cart", picks: { errors: "1", slower: "500" } }, onExplore);
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    await act(async () => ui.getByRole("button", { name: "Span error rates by service" }).click());
    await wait(() => expect(onExplore).toHaveBeenCalledWith("spans where (service:a or service:b) duration>500ms | count(), error_rate(), p95(duration) by service sort error_rate"));
    expect(bodies("/api/fanql/format")[0]).toEqual({ query: { from: "spans", where }, viz: "traces" });
  });

  it("opens every span by service when nothing filters the traces", async () => {
    const onExplore = vi.fn();
    const { ui, wait } = await render(tracesPreset, {}, onExplore);
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    await act(async () => ui.getByRole("button", { name: "Span error rates by service" }).click());
    await wait(() => expect(onExplore).toHaveBeenCalledWith("spans | count(), error_rate(), p95(duration) by service sort error_rate"));
    expect(bodies("/api/fanql/format")).toHaveLength(0);
  });

  it("opens the trace drawer named in the URL", async () => {
    const drill = JSON.stringify({ panel_id: "search", kind: "traces", from: "2026-10-30T10:00:00Z", to: "2026-10-30T10:01:00Z", window_from: "2026-10-30T10:00:00Z", window_to: "2026-10-30T10:01:00Z", dimensions: {}, trace_id: "abc" });
    const { wait } = await render(tracesPreset, { drill });
    await wait(() => expect(fetchMock.mock.calls.some(([input]) => String(input).startsWith("/api/traces/abc?"))).toBe(true));
    await wait(() => expect(document.body.querySelector('[role="dialog"]')).not.toBeNull());
  });

  it("follows Back to earlier filters", async () => {
    const { wait, onSearch, search, ui } = await render(logsPreset, { q: "service:b" });
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    await act(async () => onSearch({ ...search(), q: "service:a" }));
    await wait(() => expect(bodies("/api/fanql/parse").at(-1)).toEqual({ text: "service:a", window: "1h", source: "logs", viz: "logs" }));
    expect(EditorView.findFromDOM(ui.getByRole("textbox", { name: "Search logs" }))!.state.doc.toString()).toBe("service:a");
  });

  it("adds the filter fields after the typed filters", async () => {
    const { ui, wait, search } = await render(tracesPreset, { q: "operation:GET" });
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    await act(async () => ui.getByRole("switch", { name: "Errors only" }).click());
    await wait(() => expect(bodies("/api/fanql/parse").at(-1)).toEqual({ text: "(operation:GET) status:error", window: "1h", source: "spans", viz: "traces" }));
    expect(search().picks).toEqual({ errors: "1" });
    const runs = bodies("/api/queries/run").length;
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "Slower than (ms)" }), { target: { value: "500" } }); });
    // Typing alone does not search; leaving the field commits it.
    expect(bodies("/api/queries/run")).toHaveLength(runs);
    await act(async () => { fireEvent.keyDown(ui.getByRole("textbox", { name: "Slower than (ms)" }), { key: "Enter" }); });
    await wait(() => expect(bodies("/api/fanql/parse").at(-1).text).toBe("(operation:GET) status:error duration>500ms"));
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "Slower than (ms)" }), { target: { value: "800" } }); });
    await act(async () => { fireEvent.focusOut(ui.getByRole("textbox", { name: "Slower than (ms)" })); });
    await wait(() => expect(bodies("/api/fanql/parse").at(-1).text).toBe("(operation:GET) status:error duration>800ms"));
  });

  it("filters logs by service and severity from the URL", async () => {
    const { ui, wait } = await render(logsPreset, { picks: { service: "billing", severity: "warn" } });
    await wait(() => expect(bodies("/api/fanql/parse")[0]).toEqual({ text: "service:\"billing\" severity>=warn", window: "1h", source: "logs", viz: "logs" }));
    // A service missing from the recent schema still shows as chosen.
    await wait(() => expect(ui.getByLabelText<HTMLInputElement>("Service", { selector: "input" }).value).toBe("billing"));
  });

  it("maps notice fix-its back to the editor and drops those on page terms", async () => {
    parse = async body => json({ query: { from: "logs" }, canonical: body.text, viz: { compatible: [], edits: [] }, notices: [
      { path: "text", message: "host.name is usually a resource attribute", span: { start: 1, end: 10 }, fix_its: [{ label: "resource.host.name", span: { start: 1, end: 10 }, text: "resource.host.name" }] },
      { path: "text", message: "about the page's term", span: { start: 20, end: 30 }, fix_its: [{ label: "x", span: { start: 20, end: 30 }, text: "x" }] },
    ] });
    const { ui, wait } = await render(logsPreset, { q: "host.name:a", picks: { severity: "warn" } });
    await wait(() => expect(ui.getByText("host.name is usually a resource attribute")).toBeTruthy());
    expect(ui.getAllByRole("button", { name: /^Replace with/ }).map(b => b.textContent)).toEqual(["Replace with resource.host.name"]);
    await act(async () => ui.getByRole("button", { name: "Replace with resource.host.name" }).click());
    expect(ui.getByRole("textbox", { name: "Search logs" }).textContent).toBe("resource.host.name:a");
  });

  it("drops notices when the filters are edited or changed while the parse was out", async () => {
    const notice = { path: "text", message: "host.name is usually a resource attribute", span: { start: 0, end: 9 } };
    let answer: (() => void) | undefined;
    parse = body => body.text === "host.name:b" ? new Promise(resolve => { answer = () => resolve(json({ query: { from: "logs" }, canonical: body.text, viz: { compatible: [], edits: [] }, notices: [notice] })); })
      : Promise.resolve(json({ query: { from: "logs" }, canonical: body.text, viz: { compatible: [], edits: [] }, notices: [notice] }));
    const { ui, wait } = await render(logsPreset, { q: "host.name:a" });
    await wait(() => expect(ui.getByText("host.name is usually a resource attribute")).toBeTruthy());
    const view = EditorView.findFromDOM(ui.getByRole("textbox", { name: "Search logs" }))!;
    await act(async () => view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: "host.name:b" } }));
    expect(document.querySelector("[data-fanql-notices]")).toBeNull();
    await act(async () => ui.getByRole("button", { name: "Search" }).click());
    await act(async () => view.dispatch({ changes: { from: view.state.doc.length, insert: " x" } }));
    await act(async () => answer!());
    await wait(() => expect(bodies("/api/queries/run").length).toBeGreaterThan(1));
    expect(document.querySelector("[data-fanql-notices]")).toBeNull();
  });

  it("groups typed filters before the fields and maps problem spans back to the editor", async () => {
    parse = async body => body.text.startsWith("(") && body.text.includes("where")
      ? json({ code: "invalid_fanql", message: "The query has problems.", problems: [{ path: "text", message: "the page supplies where", span: { start: 1, end: 6 }, fix_its: [{ label: "Remove where", span: { start: 1, end: 7 }, text: "" }] }, { path: "text", message: "about the field", span: { start: 20, end: 30 } }] }, 400)
      : json({ query: { from: "logs" }, canonical: "logs", viz: { compatible: [], edits: [] }, notices: [] });
    const { ui, wait } = await render(logsPreset, { q: "service:a or service:b", picks: { severity: "warn" } });
    await wait(() => expect(bodies("/api/fanql/parse")[0].text).toBe("(service:a or service:b) severity>=warn"));
    const view = EditorView.findFromDOM(ui.getByRole("textbox", { name: "Search logs" }))!;
    await act(async () => view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: "where x" } }));
    await act(async () => ui.getByRole("button", { name: "Search" }).click());
    await wait(() => expect(ui.getAllByRole("alert")[0].textContent).toBe("the page supplies where"));
    const marks: [number, number][] = [];
    forEachDiagnostic(view.state, (_d, from, to) => marks.push([from, to]));
    expect(marks).toEqual([[0, 5], [0, 7]]);
  });

  it("shows the problem when a where is typed", async () => {
    parse = async body => body.text ? json({ code: "invalid_fanql", message: "The query has problems.", problems: [{ path: "text", message: "the page supplies where", span: { start: 0, end: 5 } }] }, 400) : json({ query: { from: "logs" }, canonical: "logs", viz: { compatible: [], edits: [] }, notices: [] });
    const { ui, wait } = await render(logsPreset, { q: "where service:a" });
    await wait(() => expect(ui.getByRole("alert").textContent).toBe("the page supplies where"));
  });
});
