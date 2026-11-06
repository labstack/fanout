import { act, useState } from "react";
import { createRoot } from "react-dom/client";
import { EditorView } from "@codemirror/view";
import { forEachDiagnostic, type Diagnostic } from "@codemirror/lint";
import { startCompletion, currentCompletions, acceptCompletion, closeCompletion } from "@codemirror/autocomplete";
import { MantineProvider } from "@mantine/core";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FanqlEditor, type FanqlEditorProps } from "./fanql-editor";
import { pointOffset, unitSpan } from "./spans";

describe("spans", () => {
  it("converts code points to UTF-16 units and back", () => {
    const text = "logs where body:\"😀x\"";
    expect(unitSpan(text, { start: 17, end: 18 })).toEqual({ from: 17, to: 19 });
    expect(pointOffset(text, 19)).toBe(18);
    expect(unitSpan("ab", { start: 1, end: 9 })).toEqual({ from: 1, to: 2 });
  });
});

const cleanups: (() => void)[] = [];
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(cleanup => cleanup())); vi.unstubAllGlobals(); document.body.innerHTML = ""; });

async function renderEditor(extra: Partial<FanqlEditorProps> = {}) {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host);
  const run = vi.fn(), change = vi.fn();
  const props: FanqlEditorProps = { value: "spans | count()", onChange: change, onRun: run, ...extra };
  const render = async (more: Partial<FanqlEditorProps> = {}) => { await act(async () => root.render(<MantineProvider><FanqlEditor {...props} {...more} /></MantineProvider>)); };
  cleanups.push(() => root.unmount());
  await render();
  const editor = host.querySelector<HTMLElement>('[role="textbox"]')!;
  const view = EditorView.findFromDOM(editor)!;
  const key = async (key: string, init: KeyboardEventInit = {}) => {
    const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...init });
    await act(async () => { editor.focus(); editor.dispatchEvent(event); });
    return event;
  };
  return { editor, view, run, change, render, key };
}

const diagnostics = (view: EditorView) => { const out: Diagnostic[] = []; forEachDiagnostic(view.state, (d, from, to) => out.push({ ...d, from, to })); return out; };

describe("FanqlEditor", () => {
  it("runs on Enter and Mod-Enter without inserting a newline", async () => {
    const { key, run, view } = await renderEditor();
    expect((await key("Enter")).defaultPrevented).toBe(true);
    const modifier = /Mac/.test(navigator.platform) ? { metaKey: true } : { ctrlKey: true };
    await key("Enter", modifier);
    expect(run).toHaveBeenCalledTimes(2);
    expect(view.state.doc.toString()).toBe("spans | count()");
  });

  it("keeps pasted text on one line", async () => {
    const { view, change } = await renderEditor();
    await act(async () => view.dispatch({ changes: { from: view.state.doc.length, insert: "\nby service" } }));
    expect(view.state.doc.toString()).toBe("spans | count() by service");
    expect(change).toHaveBeenLastCalledWith("spans | count() by service");
  });

  it("marks a problem at its span and applies its fix-it", async () => {
    const value = "spans where servce:a";
    const { view, change } = await renderEditor({ value, problems: [{ path: "text", message: "unknown field servce", hint: "did you mean service?", span: { start: 12, end: 18 }, fix_its: [{ label: "service", span: { start: 12, end: 18 }, text: "service" }] }] });
    const [diagnostic] = diagnostics(view);
    expect(diagnostic).toMatchObject({ from: 12, to: 18, severity: "error", message: "unknown field servce (did you mean service?)" });
    expect(diagnostic.actions?.[0].name).toBe("Replace with service");
    await act(async () => diagnostic.actions![0].apply(view, 12, 18));
    expect(change).toHaveBeenLastCalledWith("spans where service:a", { problems: [], notices: [] });
  });

  it("lists each fix-it of a problem as a button beside the alert that edits the text and returns focus", async () => {
    const value = "spans | cuont() by host.name";
    const at = { start: 8, end: 13 };
    const { view, change, editor } = await renderEditor({ value, problems: [{ path: "text", message: "unknown function cuont", span: at, fix_its: [{ label: "count", span: at, text: "count" }, { label: "count_if", span: at, text: "count_if" }] }] });
    expect(editor.getAttribute("aria-invalid")).toBe("true");
    const alert = document.querySelector<HTMLElement>('[data-fanql-problems] [role="alert"]')!;
    // The alert announces the message alone; the buttons are not read with it.
    expect(alert.textContent).toBe("unknown function cuont");
    expect(alert.querySelector("button")).toBeNull();
    const buttons = [...document.querySelectorAll<HTMLButtonElement>("[data-fanql-problems] button")];
    expect(buttons.map(b => b.textContent)).toEqual(["Replace with count", "Replace with count_if"]);
    expect(buttons.map(b => document.getElementById(b.getAttribute("aria-describedby")!))).toEqual([alert, alert]);
    await act(async () => buttons[0].click());
    expect(change).toHaveBeenLastCalledWith("spans | count() by host.name", { problems: [], notices: [] });
    expect(view.hasFocus).toBe(true);
    expect(view.state.selection.main.head).toBe(13);
  });

  it("applies a fix-it after characters outside the basic plane, whose spans count code points", async () => {
    // As the server reports it for this text: cuont is code points 30 to 35.
    const value = 'spans where service:"café😀" | cuont()';
    const { change } = await renderEditor({ value, problems: [{ path: "text", message: "unknown function cuont", span: { start: 30, end: 37 }, fix_its: [{ label: "count", span: { start: 30, end: 35 }, text: "count" }] }] });
    await act(async () => document.querySelector<HTMLButtonElement>("[data-fanql-problems] button")!.click());
    expect(change).toHaveBeenLastCalledWith('spans where service:"café😀" | count()', { problems: [], notices: [] });
  });

  it("shows notices apart from problems, quietly, with their fix-its", async () => {
    const value = "spans | count() by host.name";
    const at = { start: 19, end: 28 };
    const notices = [{ path: "text", message: "host.name is usually a resource attribute", span: at, fix_its: [{ label: "resource.host.name", span: at, text: "resource.host.name" }] }];
    const { view, change, editor } = await renderEditor({ value, notices });
    expect(document.querySelector('[role="alert"]')).toBeNull();
    expect(editor.getAttribute("aria-invalid")).toBe("false");
    expect(document.querySelector("[data-fanql-notices]")?.textContent).toContain("host.name is usually a resource attribute");
    expect(diagnostics(view)[0]).toMatchObject({ from: 19, to: 28, severity: "info" });
    await act(async () => document.querySelector<HTMLButtonElement>("[data-fanql-notices] button")!.click());
    expect(change).toHaveBeenLastCalledWith("spans | count() by resource.host.name", { problems: [], notices: [] });
  });

  it("keeps the fix-its a fix-it does not touch, moved by its length change", async () => {
    // Two typos and two notices; the first fix grows the text by three.
    const value = "spans | cuont(), p59(duration) by host.name";
    const cuont = { start: 8, end: 13 }, p59 = { start: 17, end: 20 }, host = { start: 34, end: 43 };
    const problems = [
      { path: "text", message: "unknown function cuont", span: cuont, fix_its: [{ label: "count_if", span: cuont, text: "count_if" }] },
      { path: "text", message: "unknown function p59", span: p59, fix_its: [{ label: "p95", span: p59, text: "p95" }] },
    ];
    const notices = [
      { path: "text", message: "host.name is usually a resource attribute", span: host, fix_its: [{ label: "resource.host.name", span: host, text: "resource.host.name" }] },
      { path: "text", message: "Grouped by host.name" },
    ];
    const { change } = await renderEditor({ value, problems, notices });
    await act(async () => document.querySelector<HTMLButtonElement>("[data-fanql-problems] button")!.click());
    const moved = (span: { start: number; end: number }) => ({ start: span.start + 3, end: span.end + 3 });
    expect(change).toHaveBeenLastCalledWith("spans | count_if(), p59(duration) by host.name", {
      problems: [{ ...problems[1], span: moved(p59), fix_its: [{ label: "p95", span: moved(p59), text: "p95" }] }],
      notices: [{ ...notices[0], span: moved(host), fix_its: [{ label: "resource.host.name", span: moved(host), text: "resource.host.name" }] }, notices[1]],
    });
  });

  it("drops whole-query problems on a fix-it, which may have resolved them, but keeps whole-query notices", async () => {
    const at = { start: 8, end: 13 };
    const problems = [{ path: "text", message: "unknown function cuont", span: at, fix_its: [{ label: "count", span: at, text: "count" }] }, { path: "query", message: "the query needs a measure" }];
    const notices = [{ path: "text", message: "Grouped by service" }];
    const { change } = await renderEditor({ value: "spans | cuont() by service", problems, notices });
    await act(async () => document.querySelector<HTMLButtonElement>("[data-fanql-problems] button")!.click());
    expect(change).toHaveBeenLastCalledWith("spans | count() by service", { problems: [], notices });
  });

  it("drops the items a fix-it touches and keeps those before it in place", async () => {
    const value = "spans | count() by host.name";
    const by = { start: 16, end: 28 }, host = { start: 19, end: 28 };
    const notices = [
      { path: "text", message: "ungrouped first", span: { start: 0, end: 5 }, fix_its: [] },
      { path: "text", message: "grouping", span: by },
      { path: "text", message: "host.name is usually a resource attribute", span: host, fix_its: [{ label: "resource.host.name", span: host, text: "resource.host.name" }] },
    ];
    const { change } = await renderEditor({ value, notices });
    await act(async () => document.querySelector<HTMLButtonElement>("[data-fanql-notices] button")!.click());
    expect(change).toHaveBeenLastCalledWith("spans | count() by resource.host.name", { problems: [], notices: [notices[0]] });
  });

  it("offers no fix-it buttons when read-only", async () => {
    await renderEditor({ readOnly: true, problems: [{ path: "text", message: "m", span: { start: 0, end: 5 }, fix_its: [{ label: "logs", span: { start: 0, end: 5 }, text: "logs" }] }] });
    expect(document.querySelector("[data-fanql-problems] button")).toBeNull();
  });

  it("marks the whole text when a problem has no span", async () => {
    const { view } = await renderEditor({ problems: [{ path: "text", message: "bad" }] });
    expect(diagnostics(view)[0]).toMatchObject({ from: 0, to: "spans | count()".length });
  });

  it("completes from the server at the cursor and replaces the given span", async () => {
    const complete = vi.fn().mockResolvedValue({ replace: { start: 0, end: 2 }, items: [{ label: "spans", insert: "spans ", kind: "keyword", detail: "source" }], schema: { window: "1h", sampled: false, warm: true, recent: true } });
    const { view } = await renderEditor({ value: "sp", complete });
    await act(async () => { view.dispatch({ selection: { anchor: 2 } }); startCompletion(view); await new Promise(resolve => setTimeout(resolve, 200)); });
    expect(complete).toHaveBeenCalledWith("sp", 2, expect.any(AbortSignal));
    expect(currentCompletions(view.state).map(option => option.label)).toEqual(["spans"]);
    await act(async () => { acceptCompletion(view); });
    expect(view.state.doc.toString()).toBe("spans ");
  });

  it("discloses recent-only suggestions and an unavailable schema after two failures", async () => {
    const response = (schema: object) => ({ replace: { start: 0, end: 2 }, items: [{ label: "spans", insert: "spans ", kind: "keyword", detail: "" }], schema: { window: "24h", sampled: true, warm: true, recent: false, ...schema } });
    const complete = vi.fn().mockResolvedValueOnce(response({ recent: true, notice: "keys seen in the last 24h (recent), not in the selected interval" }))
      .mockResolvedValueOnce(response({ warm: false, error: true })).mockResolvedValueOnce(response({ warm: false, error: true }));
    const { view } = await renderEditor({ value: "sp", complete });
    const ask = async () => act(async () => { view.dispatch({ selection: { anchor: 2 } }); startCompletion(view); await new Promise(resolve => setTimeout(resolve, 200)); });
    await ask();
    expect(document.querySelector("[data-completion-note]")?.textContent).toBe("Suggestions: keys seen in the last 24h (recent), not in the selected interval");
    await act(async () => { closeCompletion(view); });
    // The note belongs to the list; a closed list leaves no stale note behind.
    expect(document.querySelector("[data-completion-note]")).toBeNull();
    await ask();
    expect(document.querySelector("[data-completion-note]")).toBeNull();
    await act(async () => { closeCompletion(view); });
    await ask();
    expect(document.querySelector("[data-completion-note]")?.textContent).toMatch(/schema could not be read/);
  });

  it("keeps fast typing when renders lag behind the editor", async () => {
    vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
    const errors = vi.spyOn(console, "error");
    function Controlled() {
      const [text, setText] = useState("");
      return <FanqlEditor value={text} onChange={setText} onRun={() => undefined} />;
    }
    const host = document.createElement("div"); document.body.append(host);
    const root = createRoot(host);
    cleanups.push(() => root.unmount());
    await act(async () => root.render(<MantineProvider><Controlled /></MantineProvider>));
    const view = EditorView.findFromDOM(host.querySelector<HTMLElement>('[role="textbox"]')!)!;
    vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", false);
    const text = "spans where service:checkout | count() by http_route";
    // Keystrokes land between React's scheduled renders, as with fast typing.
    for (const char of text) { view.dispatch({ changes: { from: view.state.doc.length, insert: char }, userEvent: "input.type" }); await new Promise(resolve => setTimeout(resolve, 0)); }
    await new Promise(resolve => setTimeout(resolve, 100));
    expect(view.state.doc.toString()).toBe(text);
    expect(errors).not.toHaveBeenCalled();
    errors.mockRestore();
  });

  it("accepts a suggestion with Tab and leaves Tab alone without a list", async () => {
    const complete = vi.fn().mockResolvedValue({ replace: { start: 0, end: 2 }, items: [{ label: "spans", insert: "spans ", kind: "keyword", detail: "source" }], schema: { window: "1h", sampled: false, warm: true, recent: true } });
    const { view, key } = await renderEditor({ value: "sp", complete });
    expect((await key("Tab")).defaultPrevented).toBe(false);
    await act(async () => { view.dispatch({ selection: { anchor: 2 } }); startCompletion(view); await new Promise(resolve => setTimeout(resolve, 200)); });
    expect((await key("Tab")).defaultPrevented).toBe(true);
    expect(view.state.doc.toString()).toBe("spans ");
  });

  it("runs on Enter while a list that opened by itself shows, and accepts only after the arrows moved", async () => {
    const items = [{ label: "service:cart", insert: "service:cart ", kind: "value", detail: "" }, { label: "service:cartservice", insert: "service:cartservice ", kind: "value", detail: "" }];
    const complete = vi.fn().mockResolvedValue({ replace: { start: 0, end: 12 }, items, schema: { window: "1h", sampled: false, warm: true, recent: true } });
    const { view, key, run } = await renderEditor({ value: "service:cart", complete });
    const list = async () => act(async () => { view.dispatch({ selection: { anchor: 12 } }); startCompletion(view); await new Promise(resolve => setTimeout(resolve, 200)); });
    await list();
    expect(currentCompletions(view.state)).toHaveLength(2);
    expect((await key("Enter")).defaultPrevented).toBe(true);
    expect(run).toHaveBeenCalledTimes(1);
    expect(view.state.doc.toString()).toBe("service:cart");
    expect(currentCompletions(view.state)).toHaveLength(0);
    await list();
    await key("ArrowDown");
    await key("Enter");
    expect(run).toHaveBeenCalledTimes(1);
    expect(view.state.doc.toString()).toBe("service:cartservice ");
  });

  it("wraps long text on screen and shows the placeholder when empty", async () => {
    const { view, editor } = await renderEditor({ value: "", placeholder: "spans | count() by service" });
    expect(view.lineWrapping).toBe(true);
    expect(editor.parentElement?.textContent).toContain("spans | count() by service");
  });

  it("refuses edits when read-only", async () => {
    const { view } = await renderEditor({ readOnly: true });
    expect(view.state.readOnly).toBe(true);
  });
});
