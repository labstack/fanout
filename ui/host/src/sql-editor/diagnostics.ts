import type { Diagnostic } from "@codemirror/lint";
import type { Text } from "@codemirror/state";
import type { Problem } from "../api-error";

/** Explicit source ranges are zero-based, with an exclusive end. */
export type SqlProblem = Problem & { range?: { start: { line: number; character: number }; end: { line: number; character: number } } };

function offset(doc: Text, position: { line: number; character: number }): number {
  const line = doc.line(Math.max(1, Math.min(doc.lines, position.line + 1)));
  return line.from + Math.max(0, Math.min(line.length, position.character));
}

export function sqlDiagnostics(doc: Text, problems: readonly SqlProblem[]): Diagnostic[] {
  // Current panel.Problem has no source position. Do not infer offsets from
  // engine error text: macro expansion/canonicalization changes that SQL.
  return problems.map(problem => {
    const from = problem.range ? offset(doc, problem.range.start) : 0;
    const to = problem.range ? Math.max(from, offset(doc, problem.range.end)) : doc.length;
    return { from, to, severity: "error", message: problem.message + (problem.hint ? ` (${problem.hint})` : "") };
  });
}
