import { useEffect, useRef } from "react";
import { Compartment, EditorState, Prec } from "@codemirror/state";
import { EditorView, drawSelection, highlightActiveLine, keymap, lineNumbers } from "@codemirror/view";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { autocompletion, completionKeymap } from "@codemirror/autocomplete";
import { bracketMatching, indentOnInput, syntaxHighlighting } from "@codemirror/language";
import { sql } from "@codemirror/lang-sql";
import { setDiagnostics } from "@codemirror/lint";
import { completionSchema, duckDB, macroCompletions } from "./sql-language";
import { sqlDiagnostics, type SqlProblem } from "./diagnostics";
import type { TelemetrySchema } from "./schema";
import { sqlHighlight, sqlTheme } from "./theme";

const noProblems: readonly SqlProblem[] = [];
export type SqlEditorProps = {
  value: string; onChange(value: string): void; onRun(): void; onEscape(): void;
  schema?: TelemetrySchema; problems?: readonly SqlProblem[]; label?: string; describedBy?: string; disabled?: boolean;
};

/** No app or dashboard state: reusable by any SQL query surface. */
export function SqlEditor(props: SqlEditorProps) {
  const { value, schema, problems = noProblems, label = "Panel SQL", describedBy, disabled = false } = props;
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const latest = useRef(props); latest.current = props;
  const compartments = useRef({ language: new Compartment(), editable: new Compartment(), attributes: new Compartment() });
  useEffect(() => {
    const { language, editable, attributes } = compartments.current;
    const instance = new EditorView({ parent: host.current!, state: EditorState.create({ doc: latest.current.value, extensions: [
      lineNumbers(), history(), drawSelection(), highlightActiveLine(), bracketMatching(), indentOnInput(),
      sqlTheme, syntaxHighlighting(sqlHighlight), EditorView.lineWrapping,
      language.of(sql({ dialect: duckDB })), duckDB.language.data.of({ autocomplete: macroCompletions }), editable.of([]), attributes.of([]),
      Prec.highest(keymap.of([
        { key: "Mod-Enter", run: () => { if (!latest.current.disabled) latest.current.onRun(); return true; }, stopPropagation: true },
        { key: "Escape", run: () => { instance.contentDOM.blur(); latest.current.onEscape(); return true; }, stopPropagation: true },
      ])),
      // Tab remains native focus navigation; no indentation key binding.
      autocompletion(), keymap.of([...completionKeymap.filter(binding => binding.key !== "Escape"), ...defaultKeymap, ...historyKeymap]),
      EditorView.updateListener.of(update => { if (update.docChanged) latest.current.onChange(update.state.doc.toString()); }),
    ] }) });
    view.current = instance;
    return () => { view.current = null; instance.destroy(); };
  }, []);
  useEffect(() => {
    const instance = view.current!;
    if (instance.state.doc.toString() !== value) instance.dispatch({ changes: { from: 0, to: instance.state.doc.length, insert: value } });
  }, [value]);
  useEffect(() => { view.current!.dispatch({ effects: compartments.current.language.reconfigure(sql({ dialect: duckDB, schema: schema ? completionSchema(schema) : undefined })) }); }, [schema]);
  useEffect(() => { view.current!.dispatch({ effects: compartments.current.editable.reconfigure([EditorState.readOnly.of(disabled), EditorView.editable.of(!disabled)]) }); }, [disabled]);
  useEffect(() => { view.current!.dispatch({ effects: compartments.current.attributes.reconfigure(EditorView.contentAttributes.of({ role: "textbox", "aria-label": label, "aria-multiline": "true", "aria-describedby": describedBy ?? "", "aria-invalid": problems.length ? "true" : "false", spellcheck: "false" })) }); }, [label, describedBy, problems.length]);
  useEffect(() => { const instance = view.current!; instance.dispatch(setDiagnostics(instance.state, sqlDiagnostics(instance.state.doc, problems))); }, [value, problems]);
  return <div ref={host} data-sql-editor />;
}
