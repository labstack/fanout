import { Box, Table, Text, UnstyledButton } from "@mantine/core";
import { CaretDown, CaretUp } from "@phosphor-icons/react";
import { createSortedRowModel, rowSortingFeature, sortFn_alphanumeric, sortFn_basic, tableFeatures, useTable, type ColumnDef, type SortingState } from "@tanstack/react-table";
import { useMemo, useState, type ReactNode } from "react";
import { rowModel } from "../../../../panels/rows";
import type { Selection, Cell, Column, Panel, PanelResult } from "../../../../panels/types";
import { statusFor } from "../../../../panels/thresholds";
import { formatValue } from "../../../../panels/units";

const features = tableFeatures({ rowSortingFeature, sortedRowModel: createSortedRowModel(), sortFns: { basic: sortFn_basic, alphanumeric: sortFn_alphanumeric } });

type Row = Cell[];

export type TableCellProps = { column: Column; value: Cell; row: Cell[]; rowIndex: number; columnIndex: number };

export function TableViz({ panel, result, onSelect, onPoint, renderCell }: { panel: Panel; result: PanelResult; height: number; onSelect?: (value: string) => void; onPoint?: (selection: Selection) => void; renderCell?: (props: TableCellProps) => ReactNode }) {
  const frame = result.frame!;
  const model = useMemo(() => rowModel(panel, result), [panel, result]);
  const rows = useMemo<Row[]>(() => Array.from({ length: frame.rows }, (_, r) => frame.columns.map((_, c) => (typeof frame.values[c]?.[r] === "number" && !Number.isFinite(frame.values[c][r]) ? null : frame.values[c]?.[r] ?? null))), [frame]);
  const firstMeasure = frame.columns.findIndex((c) => c.role === "measure");
  const maxima = useMemo(() => frame.columns.map((c, i) => (c.role === "measure" ? Math.max(0, ...frame.values[i].filter((v): v is number => typeof v === "number" && Number.isFinite(v))) : 0)), [frame]);
  const columns = useMemo<ColumnDef<typeof features, Row>[]>(() => frame.columns.map((column, index) => ({
    id: column.name,
    header: column.name,
    accessorFn: (row) => row[index],
    cell: (info) => {
      const value = info.getValue() as Cell;
      const custom = renderCell?.({ column, value, row: info.row.original, rowIndex: info.row.index, columnIndex: index });
      if (custom !== undefined) return custom;
      if (column.role === "measure" && typeof value === "number") {
        const unit = column.unit ?? panel.unit;
        const status = index === firstMeasure ? statusFor(value, panel.thresholds, panel.better ?? result.better) : null;
        return <Box style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", gap: 8 }}>
          <Text size="sm" ff="monospace" c={status === "warn" || status === "bad" ? status : undefined}>{formatValue(unit, value)}</Text>
          {index === firstMeasure && maxima[index] > 0 && <Box w={56} h={6} bg="var(--mantine-color-default-border)" style={{ borderRadius: 3, overflow: "hidden", flex: "none" }}><Box h="100%" w={`${(value / maxima[index]) * 100}%`} bg="var(--mantine-primary-color-filled)" /></Box>}
        </Box>;
      }
      if (column.type === "time" && typeof value === "number") return <Text size="sm" ff="monospace">{new Date(value).toLocaleString()}</Text>;
      const text = value === null ? "—" : String(value);
      return <Text size="sm" className="dashboard-dimension-nowrap" title={text} ff={column.type === "json" || /(_id|^id)$/.test(column.name) ? "monospace" : undefined}>{text}</Text>;
    },
    sortFn: column.role === "measure" ? "basic" : "alphanumeric",
  })), [frame, panel, result.better, firstMeasure, maxima, renderCell]);
  const [sorting, setSorting] = useState<SortingState>([]);
  const table = useTable({ features, data: rows, columns, state: { sorting }, onSortingChange: setSorting });
  const firstDimension = panel.query?.by?.length ? frame.columns.findIndex(c => c.role === "dimension") : panel.query && frame.columns.some(c => c.name === "service") ? frame.columns.findIndex(c => c.name === "service") : frame.columns.findIndex(c => c.role === "dimension");
  const selectionFor = (index: number): Selection => {
    const row = model.rows[index];
    const selection = model.selection(row);
    // Task 8 leaves formatted trace columns as text; the row still drills.
    if (!selection.trace_id) {
      const link = panel.options?.columns?.find(column => column.format === "trace_link" && typeof row[column.field] === "string" && row[column.field] !== "");
      if (link) selection.trace_id = String(row[link.field]);
    }
    return selection;
  };
  const rowInteractive = (index: number) => Boolean(onSelect || onPoint && (panel.click || panel.drill || selectionFor(index).trace_id));
  const activate = (index: number, original: Cell[]) => {
    if (!rowInteractive(index)) return;
    onPoint?.(selectionFor(index));
    const value = firstDimension >= 0 ? String(original[firstDimension] ?? "") : undefined;
    if (value !== undefined && value !== "Other") onSelect?.(value);
  };
  return <Box>
    <Table className="dashboard-table" stickyHeader highlightOnHover={Boolean(onSelect)} fz="sm" verticalSpacing={6}>
      <Table.Thead>
        {table.getHeaderGroups().map((group) => <Table.Tr key={group.id}>
          {group.headers.map((header) => {
            const measure = frame.columns[header.index]?.role === "measure";
            return <Table.Th aria-sort={header.column.getIsSorted() === "asc" ? "ascending" : header.column.getIsSorted() === "desc" ? "descending" : "none"} key={header.id} ta={measure ? "right" : undefined}>
              <UnstyledButton onClick={header.column.getToggleSortingHandler()} fz="xs" c="dimmed" ff="monospace" fw={500}>
                <table.FlexRender header={header} />{header.column.getIsSorted() === "asc" ? <CaretUp size={10} /> : header.column.getIsSorted() === "desc" ? <CaretDown size={10} /> : null}
              </UnstyledButton>
            </Table.Th>;
          })}
        </Table.Tr>)}
      </Table.Thead>
      <Table.Tbody>
        {table.getRowModel().rows.map((row) => <Table.Tr key={row.id} tabIndex={rowInteractive(row.index) ? 0 : undefined} style={{ cursor: rowInteractive(row.index) ? "pointer" : undefined }}
          onClick={event => { if ((event.target as Element).closest("a,button")) return; activate(row.index, row.original); }}
          onKeyDown={event => { if (!rowInteractive(row.index) || (event.target as Element).closest("a,button")) return; if (event.key === "Enter" || event.key === " ") { event.preventDefault(); activate(row.index, row.original); } }}>
          {row.getAllCells().map(cell => <Table.Td key={cell.id}><table.FlexRender cell={cell} /></Table.Td>)}
        </Table.Tr>)}
      </Table.Tbody>
    </Table>
    {frame.truncated && <Text size="xs" c="dimmed" mt={4}>Showing the first {frame.rows} rows.</Text>}
  </Box>;
}
