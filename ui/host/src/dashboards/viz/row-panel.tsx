import { Anchor, Badge, Text } from "@mantine/core";
import { useCallback, useMemo, type CSSProperties } from "react";
import { makeDrill } from "../drill-state";
import { drillHref } from "../search";
import { rowModel } from "../../../../panels/rows";
import type { AnalysisProps } from "./analysis-chart";
import { TableViz, type TableCellProps } from "./table";
import { RowText } from "./cell-text";
import { seriesSlot } from "../../../../chart";
import { chartThemeFor } from "../../../../panels/compile";
import { statusInk, tint } from "../../../../panels/style";

const traceIdStyle: CSSProperties = { display: "block", width: "16ch", maxWidth: "100%", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" };
const traceStatuses: Record<string, string> = { STATUS_CODE_ERROR: "Error", STATUS_CODE_OK: "OK", STATUS_CODE_UNSET: "Unset" };

const serviceStyle: CSSProperties = { display: "block", maxWidth: "100%", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" };
function severityLabel(value: unknown): string {
  const raw = String(value ?? "").trim().toUpperCase().replace(/^SEVERITY_NUMBER_/, "");
  if (raw === "CRITICAL") return "FATAL";
  if (raw === "WARNING") return "WARN";
  const numeric = Number(raw);
  if (/^\d+$/.test(raw) && numeric >= 1 && numeric <= 24) return ["TRACE", "DEBUG", "INFO", "WARN", "ERROR", "FATAL"][Math.floor((numeric - 1) / 4)];
  return raw.match(/^(TRACE|DEBUG|INFO|WARN|ERROR|FATAL)[2-4]?$/)?.[1] ?? "—";
}

export function RowPanel(props: AnalysisProps) {
  const model = useMemo(() => rowModel(props.panel, props.result), [props.panel, props.result]);
  const patternEvents = model.rows.reduce((total, row) => total + (typeof row.count === "number" && Number.isFinite(row.count) && row.count >= 0 ? row.count : 0), 0);
  const cell = useCallback(({ column, value, rowIndex }: TableCellProps) => {
    const selection = model.selection(model.rows[rowIndex]);
    const name = column.name;
    if (name === "trace_id" && value) {
      const target = makeDrill(props.panel, props.result, selection);
      if (!target) return <Text size="sm" ff="monospace" style={traceIdStyle} title={String(value)} data-trace-id={String(value)} aria-label={`Trace ID ${value}`}>{String(value).slice(0,16)}</Text>;
      if (props.traceLinks === "button") return <Anchor component="button" type="button" style={traceIdStyle} title={String(value)} data-trace-id={String(value)} aria-label={`Trace ID ${value}`} ff="monospace" onClick={event => {
        event.stopPropagation(); props.onPoint?.(selection);
      }}>{String(value).slice(0,16)}</Anchor>;
      const href = drillHref(window.location.href, target);
      return <Anchor style={traceIdStyle} title={String(value)} data-trace-id={String(value)} aria-label={`Trace ID ${value}`} ff="monospace" href={href} onClick={event => {
        if (props.onPoint && event.button === 0 && !event.ctrlKey && !event.metaKey && !event.shiftKey && !event.altKey) {
          event.preventDefault(); event.stopPropagation(); props.onPoint(selection);
        }
      }}>{String(value).slice(0,16)}</Anchor>;
    }
    if (name === "service" && value) return props.onSelect
      ? <Anchor component="button" type="button" style={serviceStyle} title={String(value)} onClick={() => props.onSelect?.(String(value))}>{String(value)}</Anchor>
      : <Text fz={12} style={serviceStyle} title={String(value)}>{String(value)}</Text>;
    if (name === "health" || name === "severity" || name === "status") {
      const severity = name === "severity" ? severityLabel(value) : undefined;
      const bad = severity ? severity === "ERROR" || severity === "FATAL" : String(value).includes("ERROR") || value === "FATAL" || value === "CRITICAL" || value === "unhealthy";
      const warn = severity ? severity === "WARN" : value === "degraded" || String(value).startsWith("WARN");
      const label = severity ?? (name === "status" ? traceStatuses[String(value)] ?? String(value ?? "Unknown") : String(value ?? "Unknown"));
      const theme=chartThemeFor(props.dark);
      return <Badge title={String(value ?? "UNSPECIFIED")} variant="light" color={bad ? "bad" : warn ? "warn" : "gray"} style={{color:theme.text,background:tint(bad?theme.status.bad:warn?theme.status.warn:theme.muted,.14)}}>{name === "severity" && <span style={{color:bad||warn?statusInk(bad?"bad":"warn",props.dark):theme.text}}>{bad ? "◆" : warn ? "■" : "●"} </span>}{label}</Badge>;
    }
    if (name === "body" || name === "body_template") return <RowText text={String(value ?? "")} template={name === "body_template"} highlight={props.panel.options?.highlight} />;
    if (name === "trend") {
      let counts: (number | null)[] = [];
      try {
        const parsed: unknown = JSON.parse(String(value));
        if (Array.isArray(parsed)) counts = parsed.map(n => typeof n === "number" && Number.isFinite(n) && n >= 0 ? n : null);
      } catch { /* Invalid trends render an empty sparkline. */ }
      const max = counts.reduce<number>((max, n) => Math.max(max, n ?? 0), 1);
      const timing = props.result.frame?.trend;
      const points = counts.flatMap((n, index) => {
        if (n === null) return [];
        const time = timing ? timing.start_ms + index * timing.step_ms : NaN;
        const date = new Date(time);
        return [{ x: index * 100 / Math.max(counts.length - 1, 1), y: 23 - n / max * 22, label: Number.isFinite(date.getTime()) ? `${date.toISOString()}: ${n}` : String(n) }];
      });
      return <svg role="img" aria-label="Pattern count trend" width={100} height={24} viewBox="0 0 100 24">
        {points.length>1 && <path data-pattern-area d={`M${points[0].x},24 ${points.map(({x,y})=>`L${x},${y}`).join(" ")} L${points.at(-1)!.x},24 Z`} fill={seriesSlot(0,props.dark)} opacity={.18}/>}
        <polyline fill="none" stroke={seriesSlot(0,props.dark)} strokeWidth={1.5} points={points.map(({ x, y }) => `${x},${y}`).join(" ")} />
        {points.map(({ x, y, label }, index) => <circle key={index} cx={x} cy={y} r={1.5} fill={seriesSlot(0,props.dark)}><title>{label}</title></circle>)}
      </svg>;
    }
    return undefined;
  }, [props.panel, props.result, props.onSelect, props.onPoint, props.dark, props.traceLinks, model]);
  return <div role="region" aria-label={`${props.title ?? props.panel.title}: ${model.rows.length} rows`}>
    <TableViz foldConstants={props.foldConstants} traceLinks={props.traceLinks} panel={props.panel} result={props.result} height={props.height} renderCell={cell} onPoint={props.onPoint} />
    {props.panel.viz === "log_patterns" && <Text data-pattern-summary c="dimmed" fz={12} style={{marginTop:8,flexShrink:0}}>
      {model.rows.length} {model.rows.length === 1 ? "pattern" : "patterns"} · {patternEvents.toLocaleString()} {patternEvents === 1 ? "event" : "events"} in the window{props.result.frame?.truncated ? " (shown)" : ""}
    </Text>}
  </div>;
}
