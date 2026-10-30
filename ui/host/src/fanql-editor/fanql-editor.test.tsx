import { act } from "react";
import { createRoot } from "react-dom/client";
import { EditorView } from "@codemirror/view";
import { forEachDiagnostic, type Diagnostic } from "@codemirror/lint";
import { startCompletion, currentCompletions, acceptCompletion } from "@codemirror/autocomplete";
import { afterEach, describe, expect, it, vi } from "vitest";
import { FanqlEditor, type FanqlEditorProps } from "./fanql-editor";
import { pointOffset, unitOffset } from "./spans";

describe("spans", () => {
  it("converts code points to UTF-16 units and back", () => {
    const text = "logs where body:\"😀x\"";
    expect(unitOffset(text, 17)).toBe(17);
    expect(unitOffset(text, 18)).toBe(19);
    expect(pointOffset(text, 19)).toBe(18);
    expect(unitOffset("ab", 9)).toBe(2);
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
  const render = async (more: Partial<FanqlEditorProps> = {}) => { await act(async () => root.render(<FanqlEditor {...props} {...more} />)); };
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
    const { view, change } = await renderEditor({ value, problems: [{ path: "text", message: "unknown field servce", hint: "did you mean service?", span: { start: 12, end: 18 }, fix_its: [{ label: "Use service", span: { start: 12, end: 18 }, text: "service" }] }] });
    const [diagnostic] = diagnostics(view);
    expect(diagnostic).toMatchObject({ from: 12, to: 18, severity: "error", message: "unknown field servce (did you mean service?)" });
    expect(diagnostic.actions?.[0].name).toBe("Use service");
    await act(async () => diagnostic.actions![0].apply(view, 12, 18));
    expect(change).toHaveBeenLastCalledWith("spans where service:a");
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

  it("refuses edits when read-only", async () => {
    const { view } = await renderEditor({ readOnly: true });
    expect(view.state.readOnly).toBe(true);
  });
});
