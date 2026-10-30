import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { within } from "@testing-library/dom";
import { EditorView } from "@codemirror/view";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";
import { ExplorePage } from "./page";
import type { ExploreSearch } from "./search";

const frame = { columns: [{ name: "service", type: "string" as const, role: "dimension" as const }, { name: "count", type: "number" as const, role: "measure" as const }], values: [["checkout"], [42]], rows: 1 };
const parsed = (text: string, canonical = text) => ({
  query: { from: "spans", measures: ["count()"], by: ["service"] }, canonical,
  viz: { compatible: [{ viz: "table", requires: [] }, { viz: "bar", requires: [] }, { viz: "state_timeline", requires: ["thresholds"] }], edits: [{ viz: "timeseries", text: `${text} every auto` }] },
  notices: [{ path: "text", message: "Grouped by service" }],
});
const cleanups: (() => void)[] = [];
const fetchMock = vi.fn<typeof fetch>();
const json = (body: unknown, status = 200) => Response.json(body, { status });
const bodies = (url: string) => fetchMock.mock.calls.filter(([input]) => String(input) === url).map(([, init]) => JSON.parse(String(init?.body)));
let parse: (body: { text: string; viz?: string }) => Promise<Response>;
let runQuery: () => Promise<Response>;

beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  fetchMock.mockReset();
  parse = async body => json(parsed(body.text));
  runQuery = async () => json({ results: [{ id: "explore", status: "ok", elapsed_ms: 3, frame }] });
  fetchMock.mockImplementation(async (input, init) => {
    const url = String(input);
    if (url === "/api/fanql/parse") return parse(JSON.parse(String(init?.body)));
    if (url === "/api/fanql/complete") return json({ replace: { start: 0, end: 0 }, items: [], schema: { window: "1h", sampled: false, warm: true, recent: true } });
    if (url === "/api/queries/run") return runQuery();
    if (url === "/api/telemetry/schema") return json({ signals: {}, sql_relations: {} });
    throw new Error(`Unexpected request: ${url}`);
  });
});
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(cleanup => cleanup())); vi.unstubAllGlobals(); document.body.innerHTML = ""; });

async function render(initial: Partial<ExploreSearch> = {}) {
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host), client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  let search: ExploreSearch = { q: "", range: "1h", tab: "query", ...initial };
  const onSearch = vi.fn((next: ExploreSearch) => { search = next; void draw(); });
  const onSaved = vi.fn<(id: string) => void>();
  const draw = async () => act(async () => root.render(<MantineProvider><QueryClientProvider client={client}><ExplorePage search={search} onSearch={onSearch} onSaved={onSaved} /></QueryClientProvider></MantineProvider>));
  cleanups.push(() => { root.unmount(); client.clear(); });
  await draw();
  const ui = within(host);
  const editor = () => EditorView.findFromDOM(ui.getByRole("textbox", { name: /FanQL query|Explore SQL/ }))!;
  const type = async (text: string) => act(async () => { const view = editor(); view.dispatch({ changes: { from: 0, to: view.state.doc.length, insert: text } }); });
  const click = async (name: string | RegExp) => act(async () => ui.getByRole("button", { name }).click());
  const wait = async (assertion: () => void) => vi.waitFor(async () => { await act(async () => {}); assertion(); }, { timeout: 3000, interval: 10 });
  return { ui, onSearch, onSaved, type, click, wait, search: () => search };
}

describe("ExplorePage", () => {
  it("shows an example before the first run and disables Run on empty text", async () => {
    const { ui } = await render();
    expect(ui.getByText(/Type a query, for example/)).toBeTruthy();
    expect(ui.getByRole<HTMLButtonElement>("button", { name: "Run" }).disabled).toBe(true);
    expect(fetchMock).not.toHaveBeenCalledWith("/api/fanql/parse", expect.anything());
  });

  it("parses, runs one panel with the compiled query and shows the canonical text", async () => {
    parse = async body => json(parsed(body.text, "spans | count() by service"));
    const { ui, type, click, wait, onSearch } = await render();
    await type("spans | count() by service");
    await click("Run");
    await wait(() => expect(ui.getByRole("status").textContent).toContain("Ran as spans | count() by service"));
    const [body] = bodies("/api/queries/run");
    expect(body.panels).toEqual(["explore"]);
    expect(body.dashboard.panels).toEqual([{ id: "explore", title: "spans | count() by service", viz: "table", query: { from: "spans", measures: ["count()"], by: ["service"] } }]);
    expect(body.time).toEqual({ range: "1h", refresh: "off" });
    expect(onSearch).toHaveBeenCalledWith(expect.objectContaining({ q: "spans | count() by service", viz: "table" }), true);
    expect(ui.getByText("Grouped by service")).toBeTruthy();
  });

  it("shows problems and does not run a query that has them", async () => {
    parse = async () => json({ code: "invalid_fanql", message: "The query has problems.", problems: [{ path: "text", message: "unknown field servce", hint: "did you mean service?", span: { start: 12, end: 18 } }] }, 400);
    const { ui, type, click, wait } = await render();
    await type("spans where servce:a");
    await click("Run");
    await wait(() => expect(ui.getByRole("alert").textContent).toBe("unknown field servce (did you mean service?)"));
    expect(bodies("/api/queries/run")).toHaveLength(0);
  });

  it("renders an execution error in the panel card", async () => {
    runQuery = async () => json({ results: [{ id: "explore", status: "error", error: "Query timed out", elapsed_ms: 3 }] });
    const { ui, type, click, wait } = await render();
    await type("spans | count()");
    await click("Run");
    await wait(() => expect(ui.getByText(/Query timed out/)).toBeTruthy());
  });

  it("offers only compatible vizzes, disables those needing more settings, and applies an edit", async () => {
    const { ui, type, click, wait } = await render();
    await type("spans | count() by service");
    await click("Run");
    await wait(() => expect(ui.getByRole("radiogroup", { name: "Visualization" })).toBeTruthy());
    const options = within(ui.getByRole("radiogroup", { name: "Visualization" })).getAllByRole<HTMLInputElement>("radio");
    expect(options.map(option => [option.value, option.disabled])).toEqual([["table", false], ["bar", false], ["state_timeline", true]]);
    await click("Time series…");
    await wait(() => expect(bodies("/api/fanql/parse").at(-1)).toEqual({ text: "spans | count() by service every auto" }));
  });

  it("marks the result stale after an edit and only saves a fresh run with rows", async () => {
    const { ui, type, click, wait } = await render();
    const save = () => ui.getByRole<HTMLButtonElement>("button", { name: "Save to dashboard" });
    expect(save().disabled).toBe(true);
    await type("spans | count() by service");
    await click("Run");
    await wait(() => expect(save().disabled).toBe(false));
    await type("spans | count() by operation");
    expect(ui.getByRole("status").textContent).toBe("Result of the last run · query has changed");
    expect(save().disabled).toBe(true);
  });

  it("refuses to save a run without rows", async () => {
    runQuery = async () => json({ results: [{ id: "explore", status: "empty", elapsed_ms: 3, frame: { ...frame, values: [], rows: 0 } }] });
    const { ui, type, click, wait } = await render();
    await type("spans | count()");
    await click("Run");
    await wait(() => expect(ui.getByRole("status")).toBeTruthy());
    expect(ui.getByRole<HTMLButtonElement>("button", { name: "Save to dashboard" }).disabled).toBe(true);
  });

  it("aborts the previous run when another starts", async () => {
    let first: AbortSignal | undefined;
    parse = async body => json(parsed(body.text));
    let calls = 0;
    runQuery = () => new Promise(resolve => {
      calls++;
      if (calls === 1) { first = fetchMock.mock.calls.filter(([input]) => String(input) === "/api/queries/run")[0][1]?.signal ?? undefined; return; }
      resolve(json({ results: [{ id: "explore", status: "ok", elapsed_ms: 3, frame }] }));
    });
    const { ui, type, click, wait } = await render();
    await type("spans | count()");
    await click("Run");
    await wait(() => expect(calls).toBe(1));
    // Run shows a spinner meanwhile; Enter in the editor still starts a new run.
    const editor = ui.getByRole("textbox", { name: "FanQL query" });
    await act(async () => { editor.focus(); editor.dispatchEvent(new KeyboardEvent("keydown", { key: "Enter", bubbles: true, cancelable: true })); });
    await wait(() => expect(first?.aborted).toBe(true));
  });

  it("runs a shared link on open", async () => {
    const { wait } = await render({ q: "spans | count() by service", viz: "table" });
    await wait(() => expect(bodies("/api/fanql/parse")).toEqual([{ text: "spans | count() by service" }]));
    await wait(() => expect(bodies("/api/queries/run")[0].dashboard.panels[0].viz).toBe("table"));
  });

  it("keeps long queries runnable by shortening the panel title", async () => {
    const long = "spans where " + Array.from({ length: 12 }, (_, i) => `service:s${i}`).join(" or ") + " | count()";
    parse = async body => json(parsed(body.text, long));
    const { type, click, wait } = await render();
    await type(long);
    await click("Run");
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    expect(Array.from(bodies("/api/queries/run")[0].dashboard.panels[0].title as string)).toHaveLength(80);
  });

  it("does not pin a new query to the previous viz", async () => {
    const { type, click, wait } = await render({ q: "logs", viz: "logs" });
    await wait(() => expect(bodies("/api/fanql/parse")).toEqual([{ text: "logs" }]));
    await type("logs | count() by service");
    await click("Run");
    await wait(() => expect(bodies("/api/fanql/parse").at(-1)).toEqual({ text: "logs | count() by service" }));
  });

  it("reruns the edited text, not the last result, when the window changes", async () => {
    const { type, click, wait, onSearch, search } = await render();
    await type("spans | count()");
    await click("Run");
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    await type("spans | count() by service");
    await act(async () => onSearch({ ...search(), range: "24h" }));
    await wait(() => expect(bodies("/api/fanql/parse").at(-1)).toEqual({ text: "spans | count() by service" }));
    await wait(() => expect(bodies("/api/queries/run").at(-1).time).toEqual({ range: "24h", refresh: "off" }));
  });

  it("marks a result stale when the window changes and the rerun fails", async () => {
    const { ui, type, click, wait, onSearch, search } = await render();
    await type("spans | count() by service");
    await click("Run");
    await wait(() => expect(ui.getByRole<HTMLButtonElement>("button", { name: "Save to dashboard" }).disabled).toBe(false));
    runQuery = async () => json({ code: "unavailable", message: "engine busy" }, 503);
    await act(async () => onSearch({ ...search(), range: "24h" }));
    await wait(() => expect(ui.getByText("engine busy")).toBeTruthy());
    expect(ui.getByRole("status").textContent).toBe("Result of the last run · query has changed");
    expect(ui.getByRole<HTMLButtonElement>("button", { name: "Save to dashboard" }).disabled).toBe(true);
  });

  it("drops problems that arrive after the text was edited", async () => {
    let answer: (response: Response) => void = () => {};
    parse = () => new Promise(resolve => { answer = resolve; });
    const { ui, type, click, wait } = await render();
    await type("spans where servce:a");
    await click("Run");
    await wait(() => expect(bodies("/api/fanql/parse")).toHaveLength(1));
    await type("spans where service:a");
    await act(async () => answer(json({ code: "invalid_fanql", message: "The query has problems.", problems: [{ path: "text", message: "unknown field servce", span: { start: 12, end: 18 } }] }, 400)));
    await act(async () => {});
    expect(ui.queryByRole("alert")).toBeNull();
  });

  it("hides viz edits once the query has changed", async () => {
    const { ui, type, click, wait } = await render();
    await type("spans | count() by service");
    await click("Run");
    await wait(() => expect(ui.getByRole("button", { name: "Time series…" })).toBeTruthy());
    await type("spans where service:a | count() by service");
    expect(ui.queryByRole("button", { name: "Time series…" })).toBeNull();
  });

  it("runs SQL as a table", async () => {
    const { ui, type, wait } = await render({ tab: "sql", sql: "SELECT 1" });
    await wait(() => expect(bodies("/api/queries/run")[0].dashboard.panels[0]).toEqual({ id: "explore", title: "SQL", viz: "table", sql: "SELECT 1" }));
    expect(ui.getByRole("tab", { name: "SQL" }).getAttribute("aria-selected")).toBe("true");
    await type("SELECT 2");
    expect(ui.getByRole("status").textContent).toBe("Result of the last run · query has changed");
  });
});
