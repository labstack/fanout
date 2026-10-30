import { useEffect, useRef } from "react";
import { Compartment, EditorState, Prec } from "@codemirror/state";
import { EditorView, drawSelection, keymap } from "@codemirror/view";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { autocompletion, type CompletionContext, type CompletionResult } from "@codemirror/autocomplete";
import { setDiagnostics, type Diagnostic } from "@codemirror/lint";
import type { Problem } from "../api-error";
import type { Completion } from "../explore/api";
import { sqlTheme } from "../sql-editor/theme";
import { pointOffset, unitSpan } from "./spans";

const noProblems: readonly Problem[] = [];
// The shared SQL theme sizes a multi-line editor; FanQL is one line.
const oneLine = EditorView.theme({
  ".cm-scroller": { minHeight: "0", maxHeight: "none", overflowX: "auto", overflowY: "hidden", fontSize: "14px" },
  ".cm-content": { padding: "8px 4px" },
  ".cm-line": { whiteSpace: "pre" },
});

export type FanqlEditorProps = {
  value: string; onChange(value: string): void; onRun(): void;
  problems?: readonly Problem[];
  complete?: (text: string, cursor: number, signal: AbortSignal) => Promise<Completion>;
  readOnly?: boolean; label?: string; describedBy?: string; placeholder?: string;
};

export function fanqlDiagnostics(text: string, problems: readonly Problem[]): Diagnostic[] {
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
      try {
        const result = await complete(text, pointOffset(text, context.pos), abort.signal);
        if (abort.signal.aborted || result.items.length === 0) return null;
        const { from, to } = unitSpan(text, result.replace);
        return { from, to, filter: false, options: result.items.map(item => ({ label: item.label, apply: item.insert, detail: item.detail, type: item.kind })) };
      } catch { return null; }
    };
    const instance = new EditorView({ parent: host.current!, state: EditorState.create({ doc: latest.current.value, extensions: [
      history(), drawSelection(), sqlTheme, oneLine,
      // One line: newlines from typing or pasting become spaces.
      EditorState.transactionFilter.of(tr => {
        if (!tr.docChanged || !/[\r\n]/.test(tr.newDoc.toString())) return tr;
        const changes: { from: number; to: number; insert: string }[] = [];
        tr.changes.iterChanges((from, to, _f, _t, inserted) => changes.push({ from, to, insert: inserted.toString().replace(/\r?\n|\r/g, " ") }));
        return [{ changes, selection: tr.selection, scrollIntoView: tr.scrollIntoView }];
      }),
      editable.of([]), attributes.of([]),
      autocompletion({ override: [source], activateOnTypingDelay: 120 }),
      Prec.highest(keymap.of([{ key: "Mod-Enter", run: () => { latest.current.onRun(); return true; }, stopPropagation: true }])),
      // autocompletion() binds its own keys at high precedence, so an open
      // list accepts on Enter; otherwise Enter runs the query.
      keymap.of([{ key: "Enter", run: () => { latest.current.onRun(); return true; } }]),
      keymap.of([...defaultKeymap.filter(binding => binding.key !== "Enter"), ...historyKeymap]),
      EditorView.updateListener.of(update => { if (update.docChanged) latest.current.onChange(update.state.doc.toString()); }),
    ] }) });
    view.current = instance;
    return () => { pending.current?.abort(); view.current = null; instance.destroy(); };
  }, []);
  useEffect(() => {
    const instance = view.current!;
    if (instance.state.doc.toString() !== value) instance.dispatch({ changes: { from: 0, to: instance.state.doc.length, insert: value } });
  }, [value]);
  useEffect(() => { view.current!.dispatch({ effects: compartments.current.editable.reconfigure([EditorState.readOnly.of(readOnly), EditorView.editable.of(!readOnly)]) }); }, [readOnly]);
  useEffect(() => { view.current!.dispatch({ effects: compartments.current.attributes.reconfigure(EditorView.contentAttributes.of({ role: "textbox", "aria-label": label, "aria-multiline": "false", "aria-describedby": describedBy ?? "", "aria-invalid": problems.length ? "true" : "false", spellcheck: "false" })) }); }, [label, describedBy, problems.length]);
  useEffect(() => { const instance = view.current!; instance.dispatch(setDiagnostics(instance.state, fanqlDiagnostics(instance.state.doc.toString(), problems))); }, [value, problems]);
  return <div ref={host} data-fanql-editor />;
}
