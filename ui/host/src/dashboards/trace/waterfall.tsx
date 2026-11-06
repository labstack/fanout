import { Box, Table, Text } from "@mantine/core";
import { useMemo } from "react";
import type { TraceSpan } from "../../../../contracts";
import { seriesSlot } from "../../../../chart";
import { duration } from "../../../../format";

/** Parents before their children, siblings by start time. A span whose parent
 *  is not in the trace is a root; spans caught in a parent cycle follow, flat. */
function spanTree(spans: TraceSpan[]): { span: TraceSpan; depth: number }[] {
  const ids = new Set(spans.map(span => span.span_id));
  const start = new Map(spans.map(span => [span, new Date(span.start).valueOf()]));
  const byStart = (a: TraceSpan, b: TraceSpan) => start.get(a)! - start.get(b)!;
  const children = new Map<string, TraceSpan[]>();
  const roots: TraceSpan[] = [];
  for (const span of spans) {
    const parent = span.parent_span_id;
    if (parent && parent !== span.span_id && ids.has(parent)) { const list = children.get(parent); if (list) list.push(span); else children.set(parent, [span]); }
    else roots.push(span);
  }
  const out: { span: TraceSpan; depth: number }[] = [];
  const seen = new Set<TraceSpan>();
  // An explicit stack: a deep trace must not exhaust the call stack.
  const stack = roots.sort(byStart).reverse().map(span => ({ span, depth: 0 }));
  const walk = () => {
    while (stack.length) {
      const next = stack.pop()!;
      if (seen.has(next.span)) continue;
      seen.add(next.span); out.push(next);
      for (const child of [...children.get(next.span.span_id) ?? []].sort(byStart).reverse()) stack.push({ span: child, depth: next.depth + 1 });
    }
  };
  walk();
  for (const span of spans) if (!seen.has(span)) { stack.push({ span, depth: 0 }); walk(); }
  return out;
}

/** Every span of the trace in one scrolling list, indented by depth. */
export function Waterfall({ spans, dark, onSpan, selected }: { spans: TraceSpan[]; dark: boolean; onSpan?: (span: TraceSpan) => void; selected?: string }) {
  const rows = useMemo(() => {
    const starts = spans.map(span => new Date(span.start).valueOf());
    const start = Math.min(...starts);
    const total = Math.max(Math.max(...spans.map((span, i) => starts[i] + span.duration_ms)) - start, 1);
    const services = [...new Set(spans.map(span => span.service))];
    return spanTree(spans).map(({ span, depth }) => {
      const offset = ((new Date(span.start).valueOf() - start) / total) * 100;
      const color = seriesSlot(services.indexOf(span.service), dark);
      return { span, depth, offset, width: Math.min(Math.max((span.duration_ms / total) * 100, 0.6), 100 - offset), color, failed: span.status === "STATUS_CODE_ERROR" };
    });
  }, [spans, dark]);
  return <Box data-waterfall style={{ maxHeight: 520, overflow: "auto" }}>
    <Table highlightOnHover={Boolean(onSpan)} verticalSpacing={6} layout="fixed" stickyHeader>
      <colgroup><col style={{ width: "42%" }} /><col /><col style={{ width: 76 }} /></colgroup>
      <Table.Thead>
        <Table.Tr><Table.Th>Operation</Table.Th><Table.Th>Timeline</Table.Th><Table.Th ta="right">Duration</Table.Th></Table.Tr>
      </Table.Thead>
      <Table.Tbody>
        {rows.map(({ span, depth, offset, width, color, failed }) => {
          const label = `${span.service} · ${span.operation} · ${duration(span.duration_ms)}`;
          return <Table.Tr key={span.span_id} data-depth={depth} aria-selected={onSpan ? span.span_id === selected : undefined}
            bg={span.span_id === selected ? "var(--mantine-primary-color-light)" : undefined}
            tabIndex={onSpan ? 0 : undefined} onClick={onSpan ? () => onSpan(span) : undefined}
            onKeyDown={onSpan ? event => { if (event.key === "Enter" || event.key === " ") { event.preventDefault(); onSpan(span); } } : undefined}
            style={{ cursor: onSpan ? "pointer" : undefined }}>
            <Table.Td title={label}>
              <Box style={{ display: "flex", alignItems: "center", gap: 6, paddingLeft: Math.min(depth, 16) * 10, minWidth: 0 }}>
                <Box w={8} h={8} bg={color} style={{ borderRadius: "50%", flex: "0 0 auto" }} />
                <Box miw={0}>
                  <Text fw={600} size="sm" truncate>{span.operation}</Text>
                  <Text c="dimmed" size="xs" truncate>{span.service}</Text>
                </Box>
              </Box>
            </Table.Td>
            <Table.Td>
              <Box pos="relative" h={14} bg="var(--mantine-color-default-hover)" role="img" title={label}
                aria-label={`${span.operation} on ${span.service} took ${duration(span.duration_ms)}`} style={{ borderRadius: "var(--mantine-radius-sm)" }}>
                <Box pos="absolute" left={`${offset}%`} w={`${width}%`} h="100%" bg={failed ? "bad" : color} style={{ borderRadius: "var(--mantine-radius-sm)", minWidth: 3 }} />
              </Box>
            </Table.Td>
            <Table.Td ta="right"><Text size="sm" ff="monospace">{duration(span.duration_ms)}</Text></Table.Td>
          </Table.Tr>;
        })}
      </Table.Tbody>
    </Table>
  </Box>;
}
