import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { fireEvent, within } from "@testing-library/dom";
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
let format: (body: { query: unknown }) => Promise<Response>;
let schemaFailure: boolean;

beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  vi.stubGlobal("fetch", fetchMock);
  vi.stubGlobal("ResizeObserver", class { observe() {} unobserve() {} disconnect() {} });
  fetchMock.mockReset();
  schemaFailure = false;
  parse = async body => json(parsed(body.text));
  runQuery = async () => json({ results: [{ id: "explore", status: "ok", elapsed_ms: 3, frame }] });
  format = async () => json({ text: "spans | p95(duration) by service", exactness: "exact", editable: true, requires: [], notices: [], where: [], measures: [] });
  fetchMock.mockImplementation(async (input, init) => {
    const url = String(input);
    if (url === "/api/fanql/parse") return parse(JSON.parse(String(init?.body)));
    if (url === "/api/fanql/complete") return json({ replace: { start: 0, end: 0 }, items: [], schema: { window: "1h", sampled: false, warm: true, recent: true } });
    if (url === "/api/queries/run") return runQuery();
    if (url === "/api/telemetry/schema") return schemaFailure ? json({ code: "schema_unavailable", message: "schema unavailable" }, 500) : json({ signals: { spans: { columns: [{ name: "service", type: "string" }, { name: "duration_ms", type: "number" }] } }, sql_relations: {}, measures: ["count", "p95"], services: [{ value: "checkout" }] });
    if (url === "/api/fanql/format") return format(JSON.parse(String(init?.body)));
    throw new Error(`Unexpected request: ${url}`);
  });
});
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(cleanup => cleanup())); vi.unstubAllGlobals(); document.body.innerHTML = ""; });

async function render(initial: Partial<ExploreSearch> = {}) {
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host), client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  let search: ExploreSearch = { q: "", range: "1h", tab: "query", ...initial };
  const onSearch = vi.fn((next: ExploreSearch, _replace?: boolean) => { search = next; void draw(); });
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
  it("shows an example in the empty editor and disables Run on empty text", async () => {
    const { ui } = await render();
    expect(ui.getByText("Type a query above and press Enter.")).toBeTruthy();
    expect(document.querySelector(".cm-placeholder")?.textContent).toBe("spans | count() by service");
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
    // Explore panels drill like dashboard panels of the same source.
    expect(body.dashboard.panels).toEqual([{ id: "explore", title: "spans | count() by service", viz: "table", query: { from: "spans", measures: ["count()"], by: ["service"] }, drill: "traces" }]);
    expect(body.time).toEqual({ range: "1h", refresh: "off" });
    // A run of a new query is a history entry of its own.
    expect(onSearch).toHaveBeenCalledWith(expect.objectContaining({ q: "spans | count() by service", viz: "table" }), false);
    expect(ui.getByText("Grouped by service")).toBeTruthy();
  });

  it("adds a history entry per new query and shows an entry Back returns to", async () => {
    const { ui, type, click, wait, onSearch, search } = await render();
    await type("spans | count()");
    await click("Run");
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    await type("spans | count() by service");
    await click("Run");
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(2));
    await click("Run");
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(3));
    expect(onSearch.mock.calls.map(([next, replace]) => [next.q, replace])).toEqual([["spans | count()", false], ["spans | count() by service", false], ["spans | count() by service", true]]);
    // The router hands back the earlier entry.
    await act(async () => onSearch({ ...search(), q: "spans | count()" }));
    await wait(() => expect(bodies("/api/fanql/parse").at(-1)).toEqual({ text: "spans | count()" }));
    expect(ui.getByRole("textbox", { name: "FanQL query" }).textContent).toBe("spans | count()");
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(4));
  });

  it("gives logs panels the logs drill and offers no drill on metrics", async () => {
    parse = async body => json({ ...parsed(body.text), query: body.text.startsWith("logs") ? { from: "logs", measures: ["count()"], by: ["service"] } : { from: "metrics", measures: ["avg(value)"], by: ["service"] } });
    const { type, click, wait } = await render();
    await type("logs | count() by service");
    await click("Run");
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    expect(bodies("/api/queries/run")[0].dashboard.panels[0].drill).toBe("logs");
    await type("metrics | avg(value) by service");
    await click("Run");
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(2));
    expect(bodies("/api/queries/run")[1].dashboard.panels[0]).not.toHaveProperty("drill");
  });

  it("explains the SQL tab and labels the FanQL result there", async () => {
    const { ui, type, click, wait } = await render();
    await type("spans | count() by service");
    await click("Run");
    await wait(() => expect(ui.getByRole("status").textContent).toContain("Ran as"));
    await act(async () => ui.getByRole("tab", { name: "SQL" }).click());
    await wait(() => expect(document.querySelector("[data-sql-hint]")?.textContent).toMatch(/the FanQL query is not converted.*\$__window\(time_column\)/));
    expect(ui.getByRole("status").textContent).toBe("Result of the last FanQL run");
  });

  it("fits the empty state to the tab", async () => {
    const { ui } = await render({ tab: "builder" });
    await vi.waitFor(() => expect(ui.getByText("Build a query above and press Run.")).toBeTruthy());
    expect(ui.queryByText("Type a query above and press Enter.")).toBeNull();
  });

  it("keeps the save button focusable and tells why it cannot save yet", async () => {
    const { ui } = await render();
    const save = ui.getByRole<HTMLButtonElement>("button", { name: "Save to dashboard" });
    expect(save.disabled).toBe(false);
    expect(save.getAttribute("aria-disabled")).toBe("true");
    expect(document.getElementById(save.getAttribute("aria-describedby")!)?.textContent).toBe("Run a query that returns rows first");
    await act(async () => save.click());
    expect(ui.queryByRole("dialog")).toBeNull();
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
    const save = () => ui.getByRole("button", { name: "Save to dashboard" });
    expect(save().getAttribute("aria-disabled")).toBe("true");
    await type("spans | count() by service");
    await click("Run");
    await wait(() => expect(save().getAttribute("aria-disabled")).toBe("false"));
    await type("spans | count() by operation");
    expect(ui.getByRole("status").textContent).toBe("Result of the last run · query has changed");
    expect(save().getAttribute("aria-disabled")).toBe("true");
  });

  it("says the run failed when a changed query is rejected", async () => {
    const { ui, type, click, wait } = await render();
    const save = () => ui.getByRole("button", { name: "Save to dashboard" });
    const reason = () => document.getElementById(save().getAttribute("aria-describedby")!)?.textContent;
    await type("spans | count() by service");
    await click("Run");
    await wait(() => expect(save().getAttribute("aria-disabled")).toBe("false"));
    parse = async () => json({ code: "invalid_fanql", message: "The query has problems.", problems: [{ path: "text", message: "unknown field nosuchcol" }] }, 400);
    await type("spans | count() by nosuchcol");
    await click("Run");
    await wait(() => expect(ui.getByRole("alert").textContent).toBe("unknown field nosuchcol"));
    expect(save().getAttribute("aria-disabled")).toBe("true");
    expect(reason()).toBe("The last run failed; saving needs a run that works");
    await type("spans | count() by operation");
    expect(reason()).toBe("Run the query as it is now first");
  });

  it("refuses to save a run without rows", async () => {
    runQuery = async () => json({ results: [{ id: "explore", status: "empty", elapsed_ms: 3, frame: { ...frame, values: [], rows: 0 } }] });
    const { ui, type, click, wait } = await render();
    await type("spans | count()");
    await click("Run");
    await wait(() => expect(ui.getByRole("status")).toBeTruthy());
    expect(ui.getByRole("button", { name: "Save to dashboard" }).getAttribute("aria-disabled")).toBe("true");
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
    await wait(() => expect(ui.getByRole("button", { name: "Save to dashboard" }).getAttribute("aria-disabled")).toBe("false"));
    runQuery = async () => json({ code: "unavailable", message: "engine busy" }, 503);
    await act(async () => onSearch({ ...search(), range: "24h" }));
    await wait(() => expect(ui.getByText("engine busy")).toBeTruthy());
    expect(ui.getByRole("status").textContent).toBe("Result of the last run · query has changed");
    expect(ui.getByRole("button", { name: "Save to dashboard" }).getAttribute("aria-disabled")).toBe("true");
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

  it("opens the Builder from the parsed text and prints builder edits back as text", async () => {
    parse = async body => json({ ...parsed(body.text), query: { from: "spans", measures: ["count()"], by: ["service"] } });
    const { ui, type, wait, search } = await render();
    await type("spans | count() by service");
    await act(async () => ui.getByRole("tab", { name: "Builder" }).click());
    await wait(() => expect(search().tab).toBe("builder"));
    await wait(() => expect(ui.getByLabelText("Measure 1 function", { selector: "input" })).toBeTruthy());
    const fn = ui.getByLabelText("Measure 1 function", { selector: "input" });
    await act(async () => { fireEvent.focus(fn); fireEvent.click(fn); fireEvent.keyDown(fn, { key: "ArrowDown" }); });
    const list = () => within(document.getElementById(fn.getAttribute("aria-controls") ?? "")!);
    await wait(() => list().getByRole("option", { name: "p95", hidden: true }));
    await act(async () => { fireEvent.click(list().getByRole("option", { name: "p95", hidden: true })); });
    await wait(() => expect(bodies("/api/fanql/format").at(-1)).toEqual({ query: { from: "spans", measures: ["p95(duration_ms)"], by: ["service"] } }));
    await wait(() => expect(ui.getByText("spans | p95(duration) by service")).toBeTruthy());
    await act(async () => ui.getByRole("tab", { name: "Query" }).click());
    await wait(() => expect(ui.getByRole("textbox", { name: "FanQL query" }).textContent).toBe("spans | p95(duration) by service"));
  });

  it("stays on the Query tab when the text has problems", async () => {
    parse = async () => json({ code: "invalid_fanql", message: "The query has problems.", problems: [{ path: "text", message: "unexpected |" }] }, 400);
    const { ui, type, wait, search } = await render();
    await type("spans ||");
    await act(async () => ui.getByRole("tab", { name: "Builder" }).click());
    await wait(() => expect(ui.getByRole("alert").textContent).toBe("unexpected |"));
    expect(search().tab).toBe("query");
  });

  it("runs from the Builder through the formatted text", async () => {
    const { ui, wait, click } = await render({ tab: "builder" });
    await wait(() => expect(ui.getByLabelText("Measure 1 function", { selector: "input" })).toBeTruthy());
    await wait(() => expect(bodies("/api/fanql/format")).toHaveLength(1));
    await click("Run");
    await wait(() => expect(bodies("/api/fanql/parse").at(-1)).toEqual({ text: "spans | p95(duration) by service" }));
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
  });

  it("keeps a query the text cannot hold in the Builder and runs it directly", async () => {
    format = async () => json({ text: "spans where …", exactness: "exact", editable: false, reason: "it has more than 64 filters", requires: [], notices: [], where: [], measures: [] });
    const { ui, wait, click } = await render({ tab: "builder" });
    await wait(() => expect(bodies("/api/fanql/format")).toHaveLength(1));
    await click("Run");
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    expect(bodies("/api/fanql/parse")).toHaveLength(0);
    expect(bodies("/api/queries/run")[0].dashboard.panels[0].query).toEqual({ from: "spans", measures: ["count()"], by: ["service"] });
    await act(async () => ui.getByRole("tab", { name: "Query" }).click());
    await wait(() => expect(ui.getByText(/This query is edited in the Builder: it has more than 64 filters/)).toBeTruthy());
    expect(EditorView.findFromDOM(ui.getByRole("textbox", { name: "FanQL query" }))!.state.readOnly).toBe(true);
  });

  it("disables Run while the builder query has problems", async () => {
    format = async () => json({ code: "invalid_spec", message: "The query spec is invalid.", problems: [{ path: "measures[0]", message: "p95 needs a numeric field" }] }, 400);
    const { ui, wait } = await render({ tab: "builder" });
    await wait(() => expect(ui.getByRole("alert").textContent).toBe("p95 needs a numeric field"));
    expect(ui.getByRole<HTMLButtonElement>("button", { name: "Run" }).disabled).toBe(true);
  });

  it("does not let a pending builder print overwrite typed text", async () => {
    let answer: (response: Response) => void = () => {};
    const { ui, wait, type } = await render({ tab: "builder" });
    await wait(() => expect(bodies("/api/fanql/format")).toHaveLength(1));
    format = () => new Promise(resolve => { answer = resolve; });
    await act(async () => ui.getByRole("button", { name: "Add measure" }).click());
    await wait(() => expect(bodies("/api/fanql/format")).toHaveLength(2));
    await act(async () => ui.getByRole("tab", { name: "Query" }).click());
    await type("logs | count()");
    await act(async () => answer(json({ text: "spans | count(), count() by service", exactness: "exact", editable: true, requires: [], notices: [], where: [], measures: [] })));
    await act(async () => {});
    expect(EditorView.findFromDOM(ui.getByRole("textbox", { name: "FanQL query" }))!.state.doc.toString()).toBe("logs | count()");
  });

  it("reopens the Builder for an oversized query without reparsing it", async () => {
    format = async () => json({ text: "spans where …", exactness: "exact", editable: false, reason: "too long", requires: [], notices: [], where: [], measures: [] });
    const { ui, wait, search } = await render({ tab: "builder" });
    await wait(() => expect(bodies("/api/fanql/format")).toHaveLength(1));
    await act(async () => ui.getByRole("tab", { name: "Query" }).click());
    await act(async () => ui.getByRole("tab", { name: "Builder" }).click());
    await wait(() => expect(search().tab).toBe("builder"));
    expect(bodies("/api/fanql/parse")).toHaveLength(0);
  });

  it("does not pull the user back to the Builder after they leave during a run", async () => {
    const { ui, wait, click, search } = await render({ tab: "builder" });
    await wait(() => expect(bodies("/api/fanql/format")).toHaveLength(1));
    let answer: (response: Response) => void = () => {};
    format = () => new Promise(resolve => { answer = resolve; });
    await click("Run");
    await act(async () => ui.getByRole("tab", { name: "SQL" }).click());
    await act(async () => answer(json({ text: "spans | count() by service", exactness: "exact", editable: true, requires: [], notices: [], where: [], measures: [] })));
    await act(async () => {});
    expect(search().tab).toBe("sql");
    expect(bodies("/api/queries/run")).toHaveLength(0);
  });

  it("runs SQL as a table", async () => {
    const { ui, type, wait } = await render({ tab: "sql", sql: "SELECT 1" });
    await wait(() => expect(bodies("/api/queries/run")[0].dashboard.panels[0]).toEqual({ id: "explore", title: "SQL", viz: "table", sql: "SELECT 1" }));
    expect(ui.getByRole("tab", { name: "SQL" }).getAttribute("aria-selected")).toBe("true");
    await type("SELECT 2");
    expect(ui.getByRole("status").textContent).toBe("Result of the last run · query has changed");
  });

  it("shows a running state, not the empty prompt, while a link's first run is in flight", async () => {
    let answer: (response: Response) => void = () => {};
    runQuery = () => new Promise(resolve => { answer = resolve; });
    const { ui, wait } = await render({ q: "spans | count() by service" });
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    expect(ui.getByText("Running the query…")).toBeTruthy();
    expect(ui.queryByText("Type a query above and press Enter.")).toBeNull();
    await act(async () => answer(json({ results: [{ id: "explore", status: "ok", elapsed_ms: 3, frame }] })));
    await wait(() => expect(ui.queryByText("Running the query…")).toBeNull());
  });

  it("opens a Builder link without adding a history entry for the tab it is already on", async () => {
    const { ui, wait, onSearch } = await render({ tab: "builder", q: "spans | count() by service" });
    await wait(() => expect(ui.getByLabelText("Measure 1 function", { selector: "input" })).toBeTruthy());
    await wait(() => expect(bodies("/api/queries/run")).toHaveLength(1));
    expect(onSearch.mock.calls.filter(([next, replace]) => next.tab === "builder" && !replace)).toEqual([]);
  });

  it("shows why a linked query cannot open in the Builder and offers the Query tab", async () => {
    parse = async () => json({ code: "invalid_fanql", message: "The query has problems.", problems: [{ path: "text", message: "a time series groups by one field" }] }, 400);
    const { ui, wait, search } = await render({ tab: "builder", q: "spans | count() by service, operation every 5m" });
    await wait(() => expect(document.querySelector("[data-builder-blocked]")?.textContent).toContain("a time series groups by one field"));
    expect(ui.queryByText("Loading fields…")).toBeNull();
    await act(async () => ui.getByRole("button", { name: "Fix it in the Query tab" }).click());
    await wait(() => expect(search().tab).toBe("query"));
  });

  it("shows a linked query as FanQL in the Builder, labels its chips, and keeps the typed text", async () => {
    const query = { from: "spans", measures: ["count()"], where: ["contains(lower(operation), lower('se'))"] };
    parse = async body => json({ ...parsed(body.text), query });
    format = async () => json({ text: "spans where se | count()", exactness: "exact", editable: true, requires: [], notices: [], where: ["se"], measures: ["count()"] });
    const { ui, wait } = await render({ tab: "builder", q: "spans se | count()" });
    await wait(() => expect(document.querySelector("[data-builder-text]")?.textContent).toBe("As FanQL: spans where se | count()"));
    expect(ui.getByText("se")).toBeTruthy();
    expect(ui.queryByText(/contains\(lower/)).toBeNull();
    await act(async () => ui.getByRole("tab", { name: "Query" }).click());
    await wait(() => expect(ui.getByRole("textbox", { name: "FanQL query" }).textContent).toBe("spans se | count()"));
  });

  it("starts an empty Builder from an example without writing it into the query text", async () => {
    const { ui, wait } = await render({ tab: "builder" });
    await wait(() => expect(bodies("/api/fanql/format")).toHaveLength(1));
    await act(async () => ui.getByRole("tab", { name: "SQL" }).click());
    await wait(() => expect(document.querySelector("[data-sql-hint]")).toBeTruthy());
    expect(document.querySelector("[data-sql-hint]")?.textContent).not.toContain("not converted");
    await act(async () => ui.getByRole("tab", { name: "Query" }).click());
    expect(EditorView.findFromDOM(ui.getByRole("textbox", { name: "FanQL query" }))!.state.doc.toString()).toBe("");
  });

  it("holds Run while a Builder filter value cannot be used", async () => {
    parse = async body => json({ ...parsed(body.text), query: { from: "spans", measures: ["count()"], where: ["duration_ms > 100"] } });
    const { ui, wait } = await render({ tab: "builder", q: "spans duration>100ms | count()" });
    await wait(() => expect(ui.getByRole("textbox", { name: "Filter 1 value" })).toBeTruthy());
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "Filter 1 value" }), { target: { value: "100 ms please" } }); });
    await wait(() => expect(ui.getByRole<HTMLButtonElement>("button", { name: "Run" }).disabled).toBe(true));
    await act(async () => { fireEvent.change(ui.getByRole("textbox", { name: "Filter 1 value" }), { target: { value: "100ms" } }); });
    await new Promise(r => setTimeout(r, 400)); await act(async () => {}); const input = ui.getByRole<HTMLInputElement>("textbox", { name: "Filter 1 value" }); const btn = ui.getByRole<HTMLButtonElement>("button", { name: "Run" }); console.log("DBG", document.querySelector("[data-builder-problems]")?.textContent, document.querySelector("[data-builder-text]")?.textContent, input.value, input.getAttribute("aria-invalid"), btn.outerHTML.slice(0, 300), JSON.stringify(bodies("/api/fanql/format")));
    await wait(() => expect(ui.getByRole<HTMLButtonElement>("button", { name: "Run" }).disabled).toBe(false));
  });

  it("keeps the Builder usable with the query's own fields when the schema fails, and retries", async () => {
    schemaFailure = true;
    parse = async body => json({ ...parsed(body.text), query: { from: "spans", measures: ["p95(duration_ms)"], by: ["service"] } });
    const { ui, wait } = await render({ tab: "builder", q: "spans | p95(duration) by service" });
    await wait(() => expect(ui.getByText("Fields could not be loaded")).toBeTruthy());
    await wait(() => expect(ui.getByLabelText("Measure 1 field", { selector: "input" })).toHaveProperty("value", "duration_ms"));
    const before = fetchMock.mock.calls.filter(([input]) => String(input) === "/api/telemetry/schema").length;
    await act(async () => ui.getByRole("button", { name: "Retry" }).click());
    await wait(() => expect(fetchMock.mock.calls.filter(([input]) => String(input) === "/api/telemetry/schema").length).toBe(before + 1));
  });
});
