import { Button, Group, Stack, Text } from "@mantine/core";
import { useEffect, useRef, useState } from "react";
import { Annotation, Compartment, EditorState, Prec } from "@codemirror/state";
import { EditorView, drawSelection, keymap, placeholder, tooltips } from "@codemirror/view";
import { defaultKeymap, history, historyKeymap } from "@codemirror/commands";
import { acceptCompletion, autocompletion, closeCompletion, completionStatus, type CompletionContext, type CompletionResult } from "@codemirror/autocomplete";
import { setDiagnostics, type Diagnostic } from "@codemirror/lint";
import type { FixIt, Problem } from "../api-error";
import type { Completion, Notice } from "../explore/api";
import { sqlTheme } from "../sql-editor/theme";
import { pointOffset, unitSpan } from "./spans";

const noProblems: readonly Problem[] = [];
const noNotices: readonly Notice[] = [];
// The shared SQL theme sizes a multi-line editor; FanQL is one logical line that wraps on screen.
const oneLine = EditorView.theme({
  ".cm-scroller": { minHeight: "0", maxHeight: "none", overflow: "hidden", fontSize: "14px" },
  ".cm-content": { padding: "8px 4px" },
  // The list stays clear of the screen edge, and on a phone its descriptions wrap instead of being cut.
  ".cm-tooltip.cm-tooltip-autocomplete > ul": { maxWidth: "min(700px, calc(100vw - 40px))" },
  // A wrapped description hangs under the label, past the type icon.
  "@media (max-width: 600px)": {
    ".cm-tooltip.cm-tooltip-autocomplete > ul > li": { whiteSpace: "normal", paddingLeft: "calc(3px + 1.4em)", textIndent: "-1.4em" },
    ".cm-tooltip.cm-tooltip-autocomplete > ul > li .cm-completionIcon": { fontSize: "inherit", width: "1em", paddingRight: "0.4em", textIndent: "0" },
  },
});
/** Marks a transaction that applies the value prop, so it is not reported back as an edit. */
const fromProp = Annotation.define<boolean>();

export type FanqlEditorProps = {
  value: string; onChange(value: string): void; onRun(): void;
  problems?: readonly Problem[];
  /** Guidance that never blocks a run (section 0.12). */
  notices?: readonly Notice[];
  complete?: (text: string, cursor: number, signal: AbortSignal) => Promise<Completion>;
  readOnly?: boolean; label?: string; describedBy?: string; placeholder?: string;
};

const describe = (item: Problem | Notice) => item.message + ("hint" in item && item.hint ? ` (${item.hint})` : "");

/** Applies a fix-it to the text it was computed for and returns the cursor to the editor, after the replacement. */
function applyFix(view: EditorView, fix: FixIt) {
  const { from, to } = unitSpan(view.state.doc.toString(), fix.span);
  view.dispatch({ changes: { from, to, insert: fix.text }, selection: { anchor: from + fix.text.length } });
  view.focus();
}

function fanqlDiagnostics(text: string, problems: readonly Problem[], notices: readonly Notice[]): Diagnostic[] {
  const mark = (item: Problem | Notice, severity: "error" | "info"): Diagnostic => {
    const { from, to } = item.span ? unitSpan(text, item.span) : { from: 0, to: text.length };
    return { from, to, severity, message: describe(item), actions: (item.fix_its ?? []).map(fix => ({ name: `Replace with ${fix.label || fix.text}`, apply: (view: EditorView) => applyFix(view, fix) })) };
  };
  // A notice without a span says something about the whole query; it is listed, not marked.
  return [...problems.map(problem => mark(problem, "error")), ...notices.filter(notice => notice.span).map(notice => mark(notice, "info"))];
}

/** One-line FanQL input. The server parses; the editor only edits and reports. */
export function FanqlEditor(props: FanqlEditorProps) {
  const { value, problems = noProblems, notices = noNotices, readOnly = false, label = "FanQL query", describedBy } = props;
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
  // Whether the user moved through the open list with the arrow keys.
  const navigated = useRef(false);
  // Text reported through onChange that has not rendered back yet. A render
  // that lags behind typing echoes one of these and must not rewind the editor.
  const echoes = useRef<string[]>([]);
  // The text each list describes; a run can replace one list and keep the other.
  // Once the text is edited their spans no longer index it, so they are no
  // longer marked and their fix-its are withdrawn.
  const [basis, setBasis] = useState({ problems, notices, problemText: value, noticeText: value });
  if (basis.problems !== problems || basis.notices !== notices) setBasis({ problems, notices, problemText: basis.problems !== problems ? value : basis.problemText, noticeText: basis.notices !== notices ? value : basis.noticeText });
  const current = { problems: basis.problemText === value ? problems : noProblems, notices: basis.noticeText === value ? notices : noNotices };
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
      // Ahead of autocompletion(), whose own keys share the highest precedence.
      // Tab accepts an open suggestion; with no list it keeps the page's focus order.
      // Enter accepts only a suggestion the user moved to with the arrow keys; a
      // list that opened by itself does not stop Enter from running the query.
      Prec.highest(keymap.of([
        { key: "Mod-Enter", run: () => { latest.current.onRun(); return true; }, stopPropagation: true },
        { key: "Tab", run: acceptCompletion },
        { key: "Enter", run: view => {
          if (navigated.current && completionStatus(view.state) === "active") return acceptCompletion(view);
          closeCompletion(view); latest.current.onRun(); return true;
        } },
        ...["ArrowDown", "ArrowUp", "PageDown", "PageUp"].map(key => ({ key, run: (view: EditorView) => { if (completionStatus(view.state) === "active") navigated.current = true; return false; } })),
      ])),
      autocompletion({ override: [source], activateOnTypingDelay: 120 }),
      // Tooltips keep a margin from the window's sides, as the page content does.
      tooltips({ tooltipSpace: view => { const win = view.dom.ownerDocument.defaultView ?? window; return { top: 0, left: 16, bottom: win.innerHeight, right: win.innerWidth - 16 }; } }),
      keymap.of([...defaultKeymap.filter(binding => binding.key !== "Enter"), ...historyKeymap]),
      EditorView.updateListener.of(update => {
        // Typing changes the list, so a choice made with the arrows no longer stands.
        if (update.docChanged || completionStatus(update.state) === null) navigated.current = false;
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
  useEffect(() => { const instance = view.current!; instance.dispatch(setDiagnostics(instance.state, fanqlDiagnostics(instance.state.doc.toString(), current.problems, current.notices))); }, [current.problems, current.notices]);
  const fixes = (item: Problem | Notice, fresh: boolean) => fresh && !readOnly && item.fix_its?.map((fix, index) =>
    <Button key={index} size="compact-xs" variant="default" fw={400} onClick={() => applyFix(view.current!, fix)} data-fix-it><span>Replace with <code>{fix.label || fix.text}</code></span></Button>);
  return <div data-fanql-editor>
    <div ref={host} />
    {schemaNote && listOpen && <Text size="xs" c="dimmed" mt={4} data-completion-note>Suggestions: {schemaNote}</Text>}
    {problems.length > 0 && <Stack gap={4} mt="xs" role="alert" data-fanql-problems>{problems.map((problem, index) =>
      <Group key={index} gap={6} wrap="wrap"><Text size="sm" c="bad">{describe(problem)}</Text>{fixes(problem, current.problems === problems)}</Group>)}</Stack>}
    {notices.length > 0 && <Stack gap={4} mt="xs" data-fanql-notices>{notices.map((notice, index) =>
      <Group key={index} gap={6} wrap="wrap"><Text size="xs" c="dimmed">{notice.message}</Text>{fixes(notice, current.notices === notices)}</Group>)}</Stack>}
  </div>;
}
