import type { TraceSpan } from "../../../../contracts";
import { spanStarts } from "./span-timing";

/** Milliseconds relative to the minimum start of the trace. */
export type CriticalPathSegment = { span_id: string; start: number; end: number };

/** Jaeger's backwards critical-path walk over the longest root. Each sibling
 * list is sorted once; each child is examined at most once thereafter. The
 * explicit stack also bounds work and avoids recursion on very deep traces.
 * Missing parents and self-parent spans are roots; rootless cycles contribute
 * nothing. Zero/negative durations contribute no segments. Producer-to-consumer
 * edges (and their consumer subtrees) are not blocking dependencies.
 * Deviations from Jaeger: end == cursor is accepted, equal-end ties prefer the
 * later start, and only the longest root is walked.
 */
export function criticalPathSegments(spans: readonly TraceSpan[]): CriticalPathSegment[] {
  const starts = spanStarts(spans);
  const nodes = spans.map(span => {
    const start = starts.get(span)!;
    return { span, start, end: start + Math.max(0, span.duration_ms), children: [] as number[] };
  });
  const ids = new Map(nodes.map((node, i) => [node.span.span_id, i]));
  let root: number | undefined;
  for (let i = 0; i < nodes.length; i++) {
    const node = nodes[i];
    const parent = ids.get(node.span.parent_span_id ?? "");
    if (parent !== undefined && parent !== i) {
      if (nodes[parent].span.kind !== "SPAN_KIND_PRODUCER" || node.span.kind !== "SPAN_KIND_CONSUMER") nodes[parent].children.push(i);
    }
    else if (root === undefined || node.end - node.start > nodes[root].end - nodes[root].start) root = i;
  }
  if (root === undefined) return [];
  for (const node of nodes) node.children.sort((a, b) =>
    Math.min(nodes[b].end, node.end) - Math.min(nodes[a].end, node.end) || nodes[b].start - nodes[a].start || a - b);
  const frame = (id: number, start: number, end: number) => ({ id, start, end, cursor: end, next: 0 });
  const stack = [frame(root, nodes[root].start, nodes[root].end)];
  const result: CriticalPathSegment[] = [];
  const add = (id: number, start: number, end: number) => {
    if (end > start) result.push({ span_id: nodes[id].span.span_id, start, end });
  };
  while (stack.length) {
    const current = stack[stack.length - 1];
    const node = nodes[current.id];
    const childID = node.children[current.next++];
    if (childID === undefined || current.cursor <= current.start) {
      add(current.id, current.start, current.cursor);
      stack.pop();
      continue;
    }
    const child = nodes[childID];
    const end = Math.min(child.end, current.end);
    // Overlapping siblings ending after the cursor are not dependencies of
    // the selected child. Async children are clamped at the parent's end.
    if (end > current.cursor) continue;
    const start = Math.max(child.start, current.start);
    if (end <= start) continue;
    add(current.id, end, current.cursor);
    current.cursor = start;
    stack.push(frame(childID, start, end));
  }
  return result;
}
