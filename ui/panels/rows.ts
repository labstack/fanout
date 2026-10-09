import type { Cell, Frame, Panel, PanelResult, Selection } from "./types";

export type RowModel = { columns: string[]; rows: Record<string, Cell>[]; selection(row: Record<string, Cell>): Selection };

export function frameRows(frame: Frame): Record<string, Cell>[] {
  return Array.from({ length: frame.rows }, (_, row) => Object.fromEntries(frame.columns.map((column, index) => {
    const value = frame.values[index]?.[row] ?? null;
    return [column.name, typeof value === "number" && !Number.isFinite(value) ? null : value];
  })));
}

/** Only context dimensions can be folded; messages, IDs and severity remain. */
export function logConstants(panel: Panel, frame?: Frame): { name: string; value: string }[] {
  if (panel.viz !== "logs" || !frame || frame.rows < 2) return [];
  return frame.columns.flatMap((c,i) => {
    if (c.role !== "dimension" || c.type !== "string" || ["body","body_template","severity","status"].includes(c.name) || /(^id$|_id$)/.test(c.name)) return [];
    const value = frame.values[i][0];
    return typeof value === "string" && value !== "" && frame.values[i].slice(0,frame.rows).every(v => v === value) ? [{name:c.name,value}] : [];
  });
}

export function rowModel(panel: Panel, result: PanelResult): RowModel {
  const frame = result.frame ?? { columns: [], values: [], rows: 0 };
  return { columns: frame.columns.map(column => column.name), rows: frameRows(frame), selection: row => {
    const dimensions: Record<string, string> = {};
    for (const by of panel.query?.by ?? []) {
      const column = by.startsWith("attributes[") || by.startsWith("resource[") ? by.slice(by.indexOf("'") + 1, by.lastIndexOf("'")) : by;
      if (row[column] !== undefined && row[column] !== null) dimensions[by] = String(row[column]);
    }
    return {
      time: typeof row.time === "number" ? row.time : typeof row.start === "number" ? row.start : undefined,
      dimensions, trace_id: typeof row.trace_id === "string" ? row.trace_id : undefined,
      namespace: typeof row.namespace === "string" ? row.namespace : undefined,
    };
  } };
}
