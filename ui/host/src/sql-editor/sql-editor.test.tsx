import { act } from "react";
import { createRoot } from "react-dom/client";
import { EditorState } from "@codemirror/state";
import { EditorView } from "@codemirror/view";
import { CompletionContext } from "@codemirror/autocomplete";
import { schemaCompletionSource, sql } from "@codemirror/lang-sql";
import { diagnosticCount } from "@codemirror/lint";
import { afterEach, expect, it, vi } from "vitest";
import { SqlEditor } from "./sql-editor";
import { completionSchema, duckDB } from "./sql-language";
import { sqlDiagnostics } from "./diagnostics";
import type { TelemetrySchema } from "./schema";

const schema: TelemetrySchema = {
  signals: {
    spans: { columns: [{ name: "service", type: "string", description: "Service name" }, { name: "start_time", type: "time" }, { name: "duration_ms", type: "number" }] },
    logs: { columns: [{ name: "body", type: "string" }] }, metrics: { columns: [{ name: "value", type: "number" }] },
  },
  sql_relations: {
    resource_inventory: { columns: [{ name: "resource", type: "map", sql_type: "VARIANT" }] },
    service_rollup: { columns: [{ name: "p95_ms", type: "number", sql_type: "DOUBLE" }] },
    edge_rollup: { columns: [{ name: "caller", type: "string", sql_type: "VARCHAR" }] },
  },
};

it("completes approved relations and alias-qualified columns from the schema fixture", async () => {
  const config = { dialect: duckDB, schema: completionSchema(schema) };
  const complete = async (doc: string, pos = doc.length) => {
    const state = EditorState.create({ doc, extensions: [sql(config)] });
    return (await schemaCompletionSource(config)(new CompletionContext(state, pos, true)))!.options;
  };
  expect((await complete("SELECT * FROM ")).map(option => option.label).sort()).toEqual(Object.keys({ ...schema.signals, ...schema.sql_relations }).sort());
  expect(await complete("SELECT s. FROM spans s", 9)).toEqual(expect.arrayContaining([
    expect.objectContaining({ label: "duration_ms", detail: "number" }), expect.objectContaining({ label: "service", info: "Service name" }),
  ]));
  expect(await complete("SELECT resource_inventory.")).toContainEqual(expect.objectContaining({ label: "resource", detail: "VARIANT" }));
  expect(await complete("SELECT edge_rollup.")).toContainEqual(expect.objectContaining({ label: "caller", detail: "VARCHAR" }));
});

it("maps explicit source ranges and clamps them to the statement", () => {
  const doc = EditorState.create({ doc: "SELECT α\nFROM spans" }).doc;
  const [diagnostic] = sqlDiagnostics(doc, [{ path: "sql", message: "Unknown relation", hint: "use logs", range: { start: { line: 1, character: 5 }, end: { line: 8, character: 300 } } }]);
  expect(diagnostic).toEqual({ from: 14, to: doc.length, severity: "error", message: "Unknown relation (use logs)" });
  expect(sqlDiagnostics(doc, [{ path: "sql", message: "Bad range", range: { start: { line: -2, character: -4 }, end: { line: 0, character: 6 } } }])[0]).toMatchObject({ from: 0, to: 6 });
});

it("marks the whole statement when the server supplies no authored SQL position", () => {
  const doc = EditorState.create({ doc: "SELECT count(*)\nFROM spans WHERE $__window(start_time)" }).doc;
  expect(sqlDiagnostics(doc, [{ path: "panels[0].sql", message: "Parser error at LINE 3" }])[0]).toMatchObject({ from: 0, to: doc.length });
  expect(sqlDiagnostics(EditorState.create().doc, [{ path: "sql", message: "SQL required" }])[0]).toMatchObject({ from: 0, to: 0 });
});

const cleanups: (() => void)[] = [];
afterEach(async () => { await act(async () => cleanups.splice(0).forEach(cleanup => cleanup())); vi.unstubAllGlobals(); document.body.innerHTML = ""; });
async function renderEditor() {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host);
  const run = vi.fn(), escape = vi.fn(), change = vi.fn();
  const props = { value: "SELECT 1", onChange: change, onRun: run, onEscape: escape, schema };
  const render = async (extra = {}) => { await act(async () => root.render(<SqlEditor {...props} {...extra} />)); };
  cleanups.push(() => root.unmount());
  await render();
  const editor = host.querySelector<HTMLElement>('[role="textbox"]')!;
  const view = EditorView.findFromDOM(editor)!;
  const key = async (key: string, extra = {}) => {
    const event = new KeyboardEvent("keydown", { key, bubbles: true, cancelable: true, ...extra });
    await act(async () => { editor.focus(); editor.dispatchEvent(event); });
    return event;
  };
  return { editor, view, run, escape, change, render, key };
}

it("runs on Mod-Enter, leaves on Escape, and lets Tab navigate without editing", async () => {
  const { key, run, escape, view, editor } = await renderEditor();
  const modifier = /Mac/.test(navigator.platform) ? { metaKey: true } : { ctrlKey: true };
  expect((await key("Enter", modifier)).defaultPrevented).toBe(true); expect(run).toHaveBeenCalledOnce();
  const parent = vi.fn(); document.addEventListener("keydown", parent);
  expect((await key("Escape")).defaultPrevented).toBe(true);
  expect(escape).toHaveBeenCalledOnce(); expect(document.activeElement).not.toBe(editor); expect(parent).not.toHaveBeenCalled();
  document.removeEventListener("keydown", parent);
  expect((await key("Tab")).defaultPrevented).toBe(false); expect(view.state.doc.toString()).toBe("SELECT 1");
});

it("preserves the view across controlled updates and updates server diagnostics and accessibility", async () => {
  const { view, editor, render, change, key, run } = await renderEditor();
  await act(async () => view.dispatch({ changes: { from: 7, to: 8, insert: "2" } }));
  expect(change).toHaveBeenLastCalledWith("SELECT 2");
  await render({ value: "SELECT 2", problems: [{ path: "sql", message: "Invalid SQL" }] });
  expect(EditorView.findFromDOM(editor)).toBe(view); expect(diagnosticCount(view.state)).toBe(1);
  expect(editor.getAttribute("aria-label")).toBe("Panel SQL"); expect(editor.getAttribute("aria-invalid")).toBe("true");
  await render({ value: "SELECT 3", problems: [], disabled: true });
  expect(diagnosticCount(view.state)).toBe(0); expect(view.state.doc.toString()).toBe("SELECT 3");
  await key("Enter", /Mac/.test(navigator.platform) ? { metaKey: true } : { ctrlKey: true }); expect(run).not.toHaveBeenCalled();
});
