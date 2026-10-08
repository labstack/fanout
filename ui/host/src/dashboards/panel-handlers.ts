import type { Panel, PanelResult, Selection, VarValue } from "../../../panels/types";
import { makeDrill } from "./drill-state";

/** A point handler only makes trace cells actionable when they contain ids. */
export function panelHandlers(panel: Panel, result: PanelResult | undefined,
  onVariable: (name: string, value: VarValue) => void, onPoint?: (selection: Selection) => void) {
  const hasTrace = (field: string) => result?.frame?.columns.some((column, index) =>
    column.name === field && result.frame!.values[index]?.some(v => typeof v === "string" && v !== ""));
  return {
    onSelect: panel.click && (panel.viz === "service_map" || !(panel.drill && onPoint))
      ? (value: string) => onVariable(panel.click!.set_variable, value) : undefined,
    onPoint: onPoint && (panel.viz === "service_map" || panel.click || panel.drill || hasTrace("trace_id") ||
      panel.options?.columns?.some(c => c.format === "trace_link" && hasTrace(c.field))) ? onPoint : undefined,
  };
}

export function drillSelection(panel: Panel, result: PanelResult | undefined, selection: Selection, vars: Record<string, VarValue>) {
  if (!result) return;
  const target = makeDrill(panel, result, selection);
  if (!target) return;
  const first = Object.values(selection.dimensions)[0];
  const variable = panel.click?.set_variable;
  return { target, vars: variable && first !== undefined ? { ...vars, [variable]: first } : vars };
}
