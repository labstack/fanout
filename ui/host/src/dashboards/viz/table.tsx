import { Box, Table, Text, UnstyledButton } from "@mantine/core";
import { CaretDown, CaretUp } from "@phosphor-icons/react";
import { createSortedRowModel, rowSortingFeature, sortFn_alphanumeric, sortFn_basic, tableFeatures, useTable, type ColumnDef, type SortingState } from "@tanstack/react-table";
import { useMemo, useState } from "react";
import type { Cell, Panel, PanelResult } from "../../../../panels/types";
import { statusFor } from "../../../../panels/thresholds";
import { formatValue } from "../../../../panels/units";

const features = tableFeatures({ rowSortingFeature, sortedRowModel: createSortedRowModel(), sortFns: { basic: sortFn_basic, alphanumeric: sortFn_alphanumeric } });

type Row = Cell[];

export function TableViz({ panel, result, onSelect }: { panel: Panel; result: PanelResult; height: number; onSelect?: (value: string) => void }) {
  const frame = result.frame!;
  const rows = useMemo<Row[]>(() => Array.from({ length: frame.rows }, (_, r) => frame.columns.map((_, c) => frame.values[c][r])), [frame]);
  const firstMeasure = frame.columns.findIndex((c) => c.role === "measure");
  const maxima = useMemo(() => frame.columns.map((c, i) => (c.role === "measure" ? Math.max(0, ...frame.values[i].filter((v): v is number => typeof v === "number")) : 0)), [frame]);
  const columns = useMemo<ColumnDef<typeof features, Row>[]>(() => frame.columns.map((column, index) => ({
    id: column.name,
    header: column.name,
    accessorFn: (row) => row[index],
    cell: (info) => {
      const value = info.getValue() as Cell;
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
  })), [frame, panel, result.better, firstMeasure, maxima]);
  const [sorting, setSorting] = useState<SortingState>([]);
  const table = useTable({ features, data: rows, columns, state: { sorting }, onSortingChange: setSorting });
  const firstDimension = frame.columns.findIndex((c) => c.role === "dimension");
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
        {table.getRowModel().rows.map((row) => <Table.Tr key={row.id} tabIndex={onSelect && firstDimension >= 0 ? 0 : undefined} onKeyDown={(event) => {
          if (onSelect && firstDimension >= 0 && (event.key === "Enter" || event.key === " ")) {
            event.preventDefault();
            onSelect(String(row.original[firstDimension] ?? ""));
          }
        }} style={{ cursor: onSelect ? "pointer" : undefined }} onClick={onSelect && firstDimension >= 0 ? () => onSelect(String(row.original[firstDimension] ?? "")) : undefined}>
          {row.getAllCells().map((cell) => <Table.Td key={cell.id}><table.FlexRender cell={cell} /></Table.Td>)}
        </Table.Tr>)}
      </Table.Tbody>
    </Table>
    {frame.truncated && <Text size="xs" c="dimmed" mt={4}>Showing the first {frame.rows} rows.</Text>}
  </Box>;
}
