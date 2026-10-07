import { Anchor, Badge, Box, Table, Text, UnstyledButton, useComputedColorScheme } from "@mantine/core";
import { CaretDown, CaretUp } from "@phosphor-icons/react";
import { createSortedRowModel, rowSortingFeature, sortFn_alphanumeric, sortFn_basic, tableFeatures, useTable, type ColumnDef, type SortingState } from "@tanstack/react-table";
import { useEffect, useMemo, useRef, useState, type ReactNode } from "react";
import { logConstants, rowModel } from "../../../../panels/rows";
import type { Selection, Cell, Column, Panel, PanelResult, Unit } from "../../../../panels/types";
import { statusFor } from "../../../../panels/thresholds";
import { formatLabel, formatTimestamp, formatValue } from "../../../../panels/units";

import { Sparkline } from "./stat";
import { columnDisplay, type ColumnFormat } from "../../../../panels/column-formats";
import { makeDrill } from "../drill-state";
import { RowText } from "./cell-text";
import { statusInk, tint } from "../../../../panels/style";
import { chartThemeFor } from "../../../../panels/compile";

const features = tableFeatures({ rowSortingFeature, sortedRowModel: createSortedRowModel(), sortFns: { basic: sortFn_basic, alphanumeric: sortFn_alphanumeric } });

type Row = Cell[];

export type TableCellProps = { column: Column; value: Cell; row: Cell[]; rowIndex: number; columnIndex: number };

export function TableViz({ panel, result, onSelect, onPoint, onVariable, renderCell }: { panel: Panel; result: PanelResult; height: number; onSelect?: (value: string) => void; onPoint?: (selection: Selection) => void; onVariable?: (name: string, value: string) => void; renderCell?: (props: TableCellProps) => ReactNode }) {
  const dark=useComputedColorScheme("light")==="dark";
  const pointCallback = useRef(onPoint);
  const variableCallback = useRef(onVariable);
  pointCallback.current = onPoint;
  variableCallback.current = onVariable;
  const frame = result.frame!;
  const hidden = useMemo(() => new Set(logConstants(panel,frame).map(c=>c.name)),[panel,frame]);
  const [expanded,setExpanded] = useState<Set<number>>(new Set());
  useEffect(()=>setExpanded(new Set()),[frame]);
  const expandable = (column: Column) => ["body","body_template"].includes(column.name) || panel.options?.columns?.some(c=>c.field===column.name&&c.format==="log_template");
  const canExpand = frame.columns.some(expandable);
  const toggle = (index:number) => setExpanded(old=>{const next=new Set(old);if(next.has(index)) next.delete(index);else next.add(index);return next;});
  const model = useMemo(() => rowModel(panel, result), [panel, result]);
  const rows = useMemo<Row[]>(() => Array.from({ length: frame.rows }, (_, r) => frame.columns.map((_, c) => (typeof frame.values[c]?.[r] === "number" && !Number.isFinite(frame.values[c][r]) ? null : frame.values[c]?.[r] ?? null))), [frame]);
  const firstMeasure = frame.columns.findIndex((c) => c.role === "measure");
  const maxima = useMemo(() => frame.columns.map((c, i) => (c.role === "measure" ? Math.max(0, ...frame.values[i].filter((v): v is number => typeof v === "number" && Number.isFinite(v))) : 0)), [frame]);
  const columns = useMemo<ColumnDef<typeof features, Row>[]>(() => frame.columns.map<ColumnDef<typeof features, Row>>((column, index) => ({
    id: column.name,
    header: column.name,
    accessorFn: (row) => row[index],
    cell: (info) => {
      const value = info.getValue() as Cell;
      const custom = renderCell?.({ column, value, row: info.row.original, rowIndex: info.row.index, columnIndex: index });
      if (custom !== undefined) return custom;
      const format = panel.options?.columns?.find(c => c.field === column.name);
      if (format) {
        if (format.format === "sparkline" && !panel.query && column.type !== "json") return <Text size="xs" c={dark ? "warn.5" : "warn.8"} role="status">Sparkline requires an array column</Text>;
        const selection = { ...model.selection(model.rows[info.row.index]), trace_id: value === null ? undefined : String(value) };
        const target = format.format === "trace_link" ? makeDrill(panel, result, selection) : undefined;
        const url = target ? new URL(window.location.href) : undefined;
        if (url && target) url.searchParams.set("drill", JSON.stringify(target));
        return <FormattedCell format={{ ...format, unit: format.unit ?? (column.unit as Unit | undefined) ?? panel.unit }} value={value} max={maxima[index]} panel={panel} better={result.better}
          trend={panel.query ? frame.trends?.[column.name]?.[info.row.index] ?? [] : undefined} traceHref={url?.toString()}
          onTrace={() => pointCallback.current?.(selection)} onService={(name, value) => variableCallback.current?.(name, value)} />;
      }
      if (column.type === "number" && typeof value === "number") {
        const unit = column.unit ?? panel.unit;
        const status = index === firstMeasure ? statusFor(value, panel.thresholds, panel.better ?? result.better) : null;
        return <Box style={{ display: "flex", alignItems: "center", justifyContent: "flex-end", gap: 8 }}>
          <Text fz={12} ff="monospace" style={{color:status === "warn" || status === "bad" ? statusInk(status,dark) : undefined}}>{formatValue(unit, value)}</Text>
          {index === firstMeasure && maxima[index] > 0 && <Box w={56} h={6} bg="var(--mantine-color-default-border)" style={{ borderRadius: 3, overflow: "hidden", flex: "none" }}><Box h="100%" w={`${(value / maxima[index]) * 100}%`} bg="var(--mantine-primary-color-filled)" /></Box>}
        </Box>;
      }
      if (column.type === "time" && typeof value === "number") return <Text size="sm" ff="monospace" style={{ whiteSpace: "nowrap" }} title={new Date(value).toISOString()}>{formatTimestamp(value)}</Text>;
      const text = value === null ? "—" : String(value);
      if (column.name === "trace_id") return <Text fz={12} ff="monospace" title={text} data-trace-id={text}>{text.slice(0,16)}</Text>;
      return <Text fz={12} className="dashboard-dimension-nowrap" title={text} ff={column.type === "json" || /(_id|^id)$/.test(column.name) ? "monospace" : undefined}>{text}</Text>;
    },
    sortFn: column.role === "measure" ? "basic" : "alphanumeric",
  })).filter(c=>!hidden.has(c.id!)).sort((a,b)=> {
    if(panel.viz!=="log_patterns") return 0;
    const order=["severity","body_template","service","count","trend"];
    return order.indexOf(a.id!)-order.indexOf(b.id!);
  }), [frame, panel, panel.options?.columns, result, firstMeasure, maxima, renderCell, model,hidden,dark]);
  const [sorting, setSorting] = useState<SortingState>([]);
  const table = useTable({ features, data: rows, columns, state: { sorting }, onSortingChange: setSorting });
  const firstDimension = panel.query?.by?.length ? frame.columns.findIndex(c => c.role === "dimension") : panel.query && frame.columns.some(c => c.name === "service") ? frame.columns.findIndex(c => c.name === "service") : frame.columns.findIndex(c => c.role === "dimension");
  const selectionFor = (index: number): Selection => {
    const row = model.rows[index];
    const selection = model.selection(row);
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
  const logWidths:Record<string,number>={time:146,severity:90,service:120,trace_id:150,namespace:100};
  const fixedWidths=columns.flatMap(c=>logWidths[c.id!]? [logWidths[c.id!]]:[]);
  const minWidth=panel.viz === "logs" && columns.some(c=>c.id==="body") ? fixedWidths.reduce((sum,n)=>sum+n,0)+Math.max(180,...fixedWidths) : panel.viz === "log_patterns" ? 480 : undefined;
  return <Box>
    <Table className="dashboard-table" stickyHeader highlightOnHover fz={12} verticalSpacing={6} style={{minWidth}}>
      <colgroup>{columns.map(c=><col key={c.id} data-field={c.id} style={{width:panel.viz === "logs" ? ({time:"146px",severity:"90px",service:"120px",trace_id:"150px",namespace:"100px"} as Record<string,string>)[c.id!] : panel.viz === "log_patterns" ? ({severity:"90px",service:"90px",count:"64px",trend:"100px"} as Record<string,string>)[c.id!] : undefined}} />)}</colgroup>
      <Table.Thead>
        {table.getHeaderGroups().map((group) => <Table.Tr key={group.id}>
          {group.headers.map((header) => {
            const measure = frame.columns.find(c=>c.name===header.column.id)?.type === "number";
            return <Table.Th aria-sort={header.column.getIsSorted() === "asc" ? "ascending" : header.column.getIsSorted() === "desc" ? "descending" : "none"} key={header.id} ta={measure ? "right" : undefined}>
              <UnstyledButton onClick={header.column.getToggleSortingHandler()} fz="xs" c="dimmed" ff="monospace" fw={500}>
                <table.FlexRender header={header} />{header.column.getIsSorted() === "asc" ? <CaretUp size={10} /> : header.column.getIsSorted() === "desc" ? <CaretDown size={10} /> : null}
              </UnstyledButton>
            </Table.Th>;
          })}
        </Table.Tr>)}
      </Table.Thead>
      <Table.Tbody>
        {table.getRowModel().rows.map((row) => <Table.Tr key={row.id} data-row-expanded={expanded.has(row.index)} aria-expanded={canExpand ? expanded.has(row.index) : undefined} tabIndex={rowInteractive(row.index) || canExpand ? 0 : undefined} style={{ cursor: rowInteractive(row.index) || canExpand ? "pointer" : undefined }}
          onClick={event => { if ((event.target as Element).closest("a,button")) return; if(canExpand && (panel.viz === "logs" || !rowInteractive(row.index))) toggle(row.index); else activate(row.index, row.original); }}
          onKeyDown={event => { if ((!rowInteractive(row.index)&&!canExpand) || (event.target as Element).closest("a,button")) return; if (event.key === "Enter" || event.key === " ") { event.preventDefault(); if(canExpand && (panel.viz === "logs" || !rowInteractive(row.index))) toggle(row.index); else activate(row.index, row.original); } }}>
          {row.getAllCells().map(cell => {
            const column=frame.columns.find(c=>c.name===cell.column.id)!;
            return <Table.Td key={cell.id} ta={column.type === "number" ? "right" : undefined}>
              {expandable(column) ? <div data-row-text style={{display:expanded.has(row.index)?"block":"-webkit-box",WebkitLineClamp:expanded.has(row.index)?undefined:2,WebkitBoxOrient:"vertical",maxHeight:expanded.has(row.index)?undefined:36,overflow:"hidden",textOverflow:"ellipsis",whiteSpace:"normal",overflowWrap:"anywhere",lineHeight:"18px"}}><table.FlexRender cell={cell}/></div> : <table.FlexRender cell={cell}/>}
            </Table.Td>;
          })}
        </Table.Tr>)}
      </Table.Tbody>
    </Table>
    {!panel.query&&(panel.options?.columns??[]).some(c=>c.format==="sparkline"&&frame.columns.some((column,i)=>column.name===c.field&&(column.type!=="json"||frame.values[i].some(value=>Boolean(columnDisplay(c,value,0).unsupported)))))&&<Text size="xs" c={dark ? "warn.5" : "warn.8"} role="status">Sparkline requires an array column</Text>}
    {(panel.options?.columns??[]).some(c=>!frame.columns.some(column=>column.name===c.field))&&<Text size="xs" c={dark ? "warn.5" : "warn.8"} role="status">Column formats unavailable: {(panel.options?.columns??[]).filter(c=>!frame.columns.some(column=>column.name===c.field)).map(c=>c.field).join(", ")}</Text>}

    {frame.truncated && <Text size="xs" c="dimmed" mt={4}>Showing {frame.rows} rows.</Text>}
  </Box>;
}

function FormattedCell({ format, value, max, panel, better, trend, traceHref, onTrace, onService }: {
  format: ColumnFormat; value: Cell; max: number; panel: Panel; better?: "lower" | "higher"; trend?: (number | null)[];
  traceHref?: string; onTrace: () => void; onService: (name: string, value: string) => void;
}): ReactNode {
  const dark=useComputedColorScheme("light")==="dark";
  const display = columnDisplay(format, value, max, panel, better, trend);
  switch (display.kind) {
    case "unit": return <Text size="sm" ff="monospace">{display.text}</Text>;
    case "bar": return <Box style={{display:"flex",alignItems:"center",justifyContent:"flex-end",gap:8}}>
      <Text fz={12} ff="monospace" style={{whiteSpace:"nowrap"}}>{typeof value==="number" ? formatLabel(format.unit,value) : display.text}</Text>
      <Box w={64} h={6} bg="var(--mantine-color-default-border)" style={{borderRadius:3,overflow:"hidden",flex:"none"}}><Box h={6} w={`${(display.fraction ?? 0) * 100}%`} bg="var(--mantine-primary-color-filled)" /></Box>
    </Box>;
    case "status": {
      const bad = display.status === "bad" || display.text === "bad" || display.text === "unhealthy" || display.text.includes("ERROR");
      const warn = display.status === "warn" || display.text === "warn" || display.text === "degraded" || display.text === "WARN";
      const theme=chartThemeFor(dark);
      return <Badge variant="light" color={bad ? "bad" : warn ? "warn" : "gray"} style={{color:theme.text,background:tint(bad?theme.status.bad:warn?theme.status.warn:theme.muted,.14)}}><span style={{color:bad||warn?statusInk(bad?"bad":"warn",dark):theme.text}}>{bad ? "◆ " : warn ? "■ " : display.status === null || display.text === "—" || display.text === "unknown" ? "○ " : "● "}</span>{display.text}</Badge>;
    }
    case "sparkline": return display.unsupported
      ? <Text size="xs" c={dark ? "warn.5" : "warn.8"} role="status">{display.unsupported}</Text>
      : <Box style={{ display: "flex", alignItems: "center", gap: 8 }}>
        {(typeof value === "number" || value === null) && <Text size="sm" ff="monospace" style={{ flex: "none" }}>{display.text}</Text>}
        {display.points && display.points.filter(p => p !== null).length >= 2 ? <Sparkline points={display.points} /> : <Text c="dimmed">No trend</Text>}
      </Box>;
    case "trace_link": return traceHref ? <Anchor ff="monospace" fz={12} title={display.text} href={traceHref} onClick={event => {
      if (event.button === 0 && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
        event.preventDefault(); event.stopPropagation(); onTrace();
      }
    }}>{display.text.slice(0,16)}</Anchor> : <Text fz={12} ff="monospace" title={display.text}>{display.text.slice(0,16)}</Text>;
    case "service_link": return <Anchor component="button" type="button" onClick={event => {
      event.stopPropagation();
      if (display.variable && value !== null) onService(display.variable, display.text);
    }}>{display.text}</Anchor>;
    case "log_template": return <RowText text={display.text} template />;
  }
}
