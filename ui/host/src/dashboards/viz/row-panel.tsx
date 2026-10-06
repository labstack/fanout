import { Anchor, Badge, Highlight, Mark, Text } from "@mantine/core";
import { useCallback, useMemo, type ReactNode } from "react";
import { rowModel } from "../../../../panels/rows";
import type { AnalysisProps } from "./analysis-chart";
import { TableViz, type TableCellProps } from "./table";

function BodyHighlight({ text, term, monospace }: { text: string; term: string; monospace: boolean }) {
  const ff = monospace ? "monospace" : undefined;
  if (!/^\s|\s$/.test(term)) return <Highlight highlight={term} accentInsensitive={false} ff={ff} size={monospace ? "sm" : undefined}>{text}</Highlight>;
  // Mantine trims terms internally. Preserve whitespace for the server's literal contains match.
  const parts: ReactNode[] = [];
  const body = text.toLowerCase();
  const needle = term.toLowerCase();
  let start = 0;
  let index = body.indexOf(needle);
  while (index !== -1) {
    parts.push(text.slice(start, index), <Mark key={index}>{text.slice(index, index + term.length)}</Mark>);
    start = index + term.length;
    index = body.indexOf(needle, start);
  }
  parts.push(text.slice(start));
  return <Text ff={ff} size={monospace ? "sm" : undefined}>{parts}</Text>;
}

export function RowPanel(props: AnalysisProps) {
  const model = useMemo(() => rowModel(props.panel, props.result), [props.panel, props.result]);
  const cell = useCallback(({ column, value }: TableCellProps) => {
    const name = column.name;
    // Task 10 supplies captured trace targets through the drill drawer.
    if (name === "trace_id" && value) return <Text size="sm" ff="monospace" data-trace-id={String(value)} aria-label={`Trace ID ${value}`}>{String(value)}</Text>;
    if (name === "service" && value && props.onSelect) return <Anchor component="button" type="button" onClick={() => props.onSelect?.(String(value))}>{String(value)}</Anchor>;
    if (name === "health" || name === "severity" || name === "status") return <Badge color={String(value).includes("ERROR") || value === "FATAL" || value === "CRITICAL" || value === "unhealthy" ? "bad" : value === "degraded" || value === "WARN" ? "warn" : "gray"}>{String(value ?? "Unknown")}</Badge>;
    if ((name === "body" || name === "body_template") && props.panel.options?.highlight) return <BodyHighlight text={String(value ?? "")} term={props.panel.options.highlight} monospace={name === "body_template"} />;
    if (name === "body_template") return <Text size="sm" ff="monospace">{String(value ?? "")}</Text>;
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
        <polyline fill="none" stroke="var(--mantine-primary-color-filled)" points={points.map(({ x, y }) => `${x},${y}`).join(" ")} />
        {points.map(({ x, y, label }, index) => <circle key={index} cx={x} cy={y} r={1.5} fill="var(--mantine-primary-color-filled)"><title>{label}</title></circle>)}
      </svg>;
    }
    return undefined;
  }, [props.panel, props.result, props.onSelect]);
  return <div role="region" aria-label={`${props.title ?? props.panel.title}: ${model.rows.length} rows`}>
    <TableViz panel={props.panel} result={props.result} height={props.height} renderCell={cell} />
  </div>;
}
