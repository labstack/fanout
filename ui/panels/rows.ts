import type { Cell, Frame, Panel, PanelResult, Selection } from "./types";

export type RowModel = { columns: string[]; rows: Record<string, Cell>[]; selection(row: Record<string, Cell>): Selection };

export function frameRows(frame: Frame): Record<string, Cell>[] {
  return Array.from({ length: frame.rows }, (_, row) => Object.fromEntries(frame.columns.map((column, index) => {
    const value = frame.values[index]?.[row] ?? null;
    return [column.name, typeof value === "number" && !Number.isFinite(value) ? null : value];
  })));
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
