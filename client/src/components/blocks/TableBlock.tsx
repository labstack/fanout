import {
  useReactTable,
  getCoreRowModel,
  flexRender,
  createColumnHelper,
} from "@tanstack/react-table";
import { useMemo } from "react";
import type { TableBlockData, TableColumn } from "@/lib/types";
import { replaceEmojis } from "@/lib/emoji-icons";

const alignClass: Record<NonNullable<TableColumn["align"]>, string> = {
  left: "text-left",
  right: "text-right",
  center: "text-center",
};

type Row = Record<string, unknown>;

const columnHelper = createColumnHelper<Row>();

export function TableBlock({ data }: { data: TableBlockData }) {
  const columns = useMemo(
    () =>
      data.columns.map((col) =>
        columnHelper.accessor((row) => row[col.key], {
          id: col.key,
          header: () => col.label,
          cell: (info) => {
            const v = info.getValue();
            if (v == null) return "";
            const s = String(v);
            return <>{replaceEmojis(s)}</>;
          },
          meta: { align: col.align ?? "left" },
        }),
      ),
    [data.columns],
  );

  const table = useReactTable({
    data: data.rows,
    columns,
    getCoreRowModel: getCoreRowModel(),
  });

  return (
    <div className="overflow-x-auto rounded-lg border border-border">
      <table className="w-full text-sm">
        <thead>
          {table.getHeaderGroups().map((hg) => (
            <tr key={hg.id} className="border-b border-border bg-muted/50">
              {hg.headers.map((header) => {
                const align =
                  (header.column.columnDef.meta as { align?: string })?.align ??
                  "left";
                return (
                  <th
                    key={header.id}
                    className={`px-4 py-2.5 font-medium text-muted-foreground ${alignClass[align as keyof typeof alignClass]}`}
                  >
                    {header.isPlaceholder
                      ? null
                      : flexRender(
                          header.column.columnDef.header,
                          header.getContext(),
                        )}
                  </th>
                );
              })}
            </tr>
          ))}
        </thead>
        <tbody>
          {table.getRowModel().rows.map((row) => (
            <tr
              key={row.id}
              className="border-b border-border last:border-b-0 transition-colors hover:bg-muted/50"
            >
              {row.getVisibleCells().map((cell) => {
                const align =
                  (cell.column.columnDef.meta as { align?: string })?.align ??
                  "left";
                return (
                  <td
                    key={cell.id}
                    className={`px-4 py-2 ${alignClass[align as keyof typeof alignClass]}`}
                  >
                    {flexRender(cell.column.columnDef.cell, cell.getContext())}
                  </td>
                );
              })}
            </tr>
          ))}
        </tbody>
      </table>
    </div>
  );
}
