import { Text } from "@mantine/core";
import { useEffect, useRef, useState } from "react";
import { Annotation, Compartment, EditorState, Prec } from "@codemirror/state";
import { EditorView, drawSelection, keymap, placeholder } from "@codemirror/view";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { acceptCompletion, autocompletion, completionStatus, type CompletionContext, type CompletionResult } from "@codemirror/autocomplete";
import { setDiagnostics, type Diagnostic } from "@codemirror/lint";
import type { Problem } from "../api-error";
import type { Completion } from "../explore/api";
import { sqlTheme } from "../sql-editor/theme";
import { pointOffset, unitSpan } from "./spans";

const noProblems: readonly Problem[] = [];
// The shared SQL theme sizes a multi-line editor; FanQL is one logical line that wraps on screen.
const oneLine = EditorView.theme({
  ".cm-scroller": { minHeight: "0", maxHeight: "none", overflow: "hidden", fontSize: "14px" },
  ".cm-content": { padding: "8px 4px" },
});
/** Marks a transaction that applies the value prop, so it is not reported back as an edit. */
const fromProp = Annotation.define<boolean>();

export type FanqlEditorProps = {
  value: string; onChange(value: string): void; onRun(): void;
  problems?: readonly Problem[];
  complete?: (text: string, cursor: number, signal: AbortSignal) => Promise<Completion>;
  readOnly?: boolean; label?: string; describedBy?: string; placeholder?: string;
};

function fanqlDiagnostics(text: string, problems: readonly Problem[]): Diagnostic[] {
  return problems.map(problem => {
    const { from, to } = problem.span ? unitSpan(text, problem.span) : { from: 0, to: text.length };
    return {
      from, to, severity: "error", message: problem.message + (problem.hint ? ` (${problem.hint})` : ""),
      actions: (problem.fix_its ?? []).map(fix => ({
        name: fix.label || fix.text,
        apply(view: EditorView) {
          const doc = view.state.doc.toString();
          const target = unitSpan(doc, fix.span);
          view.dispatch({ changes: { from: target.from, to: target.to, insert: fix.text } });
        },
      })),
    };
  });
}

/** One-line FanQL input. The server parses; the editor only edits and reports. */
export function FanqlEditor(props: FanqlEditorProps) {
  const { value, problems = noProblems, readOnly = false, label = "FanQL query", describedBy } = props;
  const host = useRef<HTMLDivElement>(null);
  const view = useRef<EditorView | null>(null);
  const latest = useRef(props); latest.current = props;
  const pending = useRef<AbortController | undefined>(undefined);
  // What the suggestions are based on (section 0.14): the server's recency notice, or that the schema is unavailable.
  const [schemaNote, setSchemaNote] = useState<string>();
  const failures = useRef(0);
  // The note describes the open suggestion list, so it shows only with one.
  const [listOpen, setListOpen] = useState(false);
  const open = useRef(false);
  // Text reported through onChange that has not rendered back yet. A render
  // that lags behind typing echoes one of these and must not rewind the editor.
  const echoes = useRef<string[]>([]);
  const compartments = useRef({ editable: new Compartment(), attributes: new Compartment() });
  useEffect(() => {
    const { editable, attributes } = compartments.current;
    const source = async (context: CompletionContext): Promise<CompletionResult | null> => {
      const complete = latest.current.complete;
      if (!complete) return null;
      pending.current?.abort();
      const abort = new AbortController(); pending.current = abort;
      context.addEventListener("abort", () => abort.abort());
      const text = context.state.doc.toString();
      setSchemaNote(undefined);
      try {
        const result = await complete(text, pointOffset(text, context.pos), abort.signal);
        if (abort.signal.aborted) return null;
        failures.current = result.schema.error ? failures.current + 1 : 0;
        setSchemaNote(failures.current >= 2 ? "Field suggestions are unavailable: the telemetry schema could not be read." : result.schema.notice);
        if (result.items.length === 0) return null;
        const { from, to } = unitSpan(text, result.replace);
        return { from, to, filter: false, options: result.items.map(item => ({ label: item.label, apply: item.insert, detail: item.detail, type: item.kind })) };
      } catch { return null; }
    };
    const instance = new EditorView({ parent: host.current!, state: EditorState.create({ doc: latest.current.value, extensions: [
      history(), drawSelection(), sqlTheme, Prec.highest(oneLine), EditorView.lineWrapping,
      // One line: newlines from typing or pasting become spaces.
      EditorState.transactionFilter.of(tr => {
        if (!tr.docChanged || !/[\r\n]/.test(tr.newDoc.toString())) return tr;
        const changes: { from: number; to: number; insert: string }[] = [];
        tr.changes.iterChanges((from, to, _f, _t, inserted) => changes.push({ from, to, insert: inserted.toString().replace(/\r?\n|\r/g, " ") }));
        return [{ changes, selection: tr.selection, scrollIntoView: tr.scrollIntoView }];
      }),
      editable.of([]), attributes.of([]),
      autocompletion({ override: [source], activateOnTypingDelay: 120 }),
      // Tab accepts an open suggestion; with no list it keeps the page's focus order.
      Prec.highest(keymap.of([{ key: "Mod-Enter", run: () => { latest.current.onRun(); return true; }, stopPropagation: true }, { key: "Tab", run: acceptCompletion }])),
      // autocompletion() binds its own keys at high precedence, so an open
      // list accepts on Enter; otherwise Enter runs the query.
      keymap.of([{ key: "Enter", run: () => { latest.current.onRun(); return true; } }]),
      keymap.of([...defaultKeymap.filter(binding => binding.key !== "Enter"), ...historyKeymap]),
      EditorView.updateListener.of(update => {
        if (update.docChanged && !update.transactions.some(tr => tr.annotation(fromProp))) {
          const text = update.state.doc.toString();
          echoes.current.push(text);
          latest.current.onChange(text);
        }
        const listed = completionStatus(update.state) !== null;
        if (listed !== open.current) { open.current = listed; setListOpen(listed); }
      }),
    ] }) });
    view.current = instance;
    return () => { pending.current?.abort(); view.current = null; instance.destroy(); };
  }, []);
  useEffect(() => {
    const instance = view.current!;
    const echo = echoes.current.indexOf(value);
    if (echo >= 0) { echoes.current.splice(0, echo + 1); return; }
    echoes.current = [];
    if (instance.state.doc.toString() !== value) instance.dispatch({ changes: { from: 0, to: instance.state.doc.length, insert: value }, annotations: fromProp.of(true) });
  }, [value]);
  useEffect(() => { view.current!.dispatch({ effects: compartments.current.editable.reconfigure([EditorState.readOnly.of(readOnly), EditorView.editable.of(!readOnly)]) }); }, [readOnly]);
  useEffect(() => { view.current!.dispatch({ effects: compartments.current.attributes.reconfigure([
    EditorView.contentAttributes.of({ role: "textbox", "aria-label": label, "aria-multiline": "false", "aria-describedby": describedBy ?? "", "aria-invalid": problems.length ? "true" : "false", spellcheck: "false" }),
    props.placeholder ? placeholder(props.placeholder) : [],
  ]) }); }, [label, describedBy, problems.length, props.placeholder]);
  useEffect(() => { const instance = view.current!; instance.dispatch(setDiagnostics(instance.state, fanqlDiagnostics(instance.state.doc.toString(), problems))); }, [value, problems]);
  return <div data-fanql-editor>
    <div ref={host} />
    {schemaNote && listOpen && <Text size="xs" c="dimmed" mt={4} data-completion-note>Suggestions: {schemaNote}</Text>}
  </div>;
}
