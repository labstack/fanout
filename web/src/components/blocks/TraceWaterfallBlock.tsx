import { useRef, useEffect, useState, useCallback, useMemo } from "react";
import { scaleLinear, scaleOrdinal, schemeTableau10 } from "d3";
import type { TraceWaterfallData, TraceSpan } from "@/lib/types";
import { COLORS } from "@/lib/theme";

const ROW_HEIGHT = 30;
const LABEL_WIDTH = 220;
const RULER_HEIGHT = 24;
const BAR_PADDING = 4;
const MARGIN_RIGHT = 80; // room for duration labels

interface FlatSpan extends TraceSpan {
  depth: number;
  row: number;
}

/** Build a tree from flat spans and flatten via DFS for row assignment. */
function flattenSpans(spans: TraceSpan[]): FlatSpan[] {
  const byId = new Map<string, TraceSpan & { children: TraceSpan[] }>();
  for (const s of spans) {
    byId.set(s.id, { ...s, children: [] });
  }

  const roots: (TraceSpan & { children: TraceSpan[] })[] = [];
  for (const s of spans) {
    const node = byId.get(s.id)!;
    if (s.parent && byId.has(s.parent)) {
      byId.get(s.parent)!.children.push(node);
    } else {
      roots.push(node);
    }
  }

  // Sort roots by start time
  roots.sort((a, b) => a.start - b.start);

  const flat: FlatSpan[] = [];
  function dfs(node: TraceSpan & { children: TraceSpan[] }, depth: number) {
    flat.push({ ...node, depth, row: flat.length });
    // Sort children by start time
    node.children.sort((a, b) => a.start - b.start);
    for (const child of node.children) {
      dfs(child as TraceSpan & { children: TraceSpan[] }, depth + 1);
    }
  }
  for (const root of roots) {
    dfs(root, 0);
  }

  return flat;
}

function statusColor(status: string): string {
  switch (status.toLowerCase()) {
    case "error":
      return COLORS.unhealthy;
    case "unset":
    case "ok":
    default:
      return "";
  }
}

export function TraceWaterfallBlock({ data, onAction }: { data: TraceWaterfallData; onAction?: (prompt: string) => void }) {
  const containerRef = useRef<HTMLDivElement>(null);
  const [width, setWidth] = useState(800);
  const [tooltip, setTooltip] = useState<{
    x: number;
    y: number;
    span: FlatSpan;
  } | null>(null);

  const updateWidth = useCallback(() => {
    if (containerRef.current) {
      setWidth(containerRef.current.clientWidth);
    }
  }, []);

  useEffect(() => {
    updateWidth();
    const observer = new ResizeObserver(updateWidth);
    if (containerRef.current) {
      observer.observe(containerRef.current);
    }
    return () => observer.disconnect();
  }, [updateWidth]);

  const flat = useMemo(() => flattenSpans(data.spans), [data.spans]);

  const maxEnd = useMemo(() => {
    if (flat.length === 0) return 1;
    return Math.max(...flat.map((s) => s.start + s.duration)) || 1;
  }, [flat]);

  const services = useMemo(
    () => Array.from(new Set(flat.map((s) => s.service))),
    [flat],
  );

  const serviceColor = useMemo(
    () => scaleOrdinal(schemeTableau10).domain(services),
    [services],
  );

  const barAreaWidth = width - LABEL_WIDTH - MARGIN_RIGHT;
  const xScale = scaleLinear()
    .domain([0, maxEnd])
    .range([0, Math.max(barAreaWidth, 100)]);

  const svgHeight = RULER_HEIGHT + flat.length * ROW_HEIGHT + 8;

  // Tick marks for ruler
  const tickCount = Math.min(8, Math.max(3, Math.floor(barAreaWidth / 100)));
  const ticks: number[] = [];
  for (let i = 0; i <= tickCount; i++) {
    ticks.push((maxEnd / tickCount) * i);
  }

  if (flat.length === 0) {
    return (
      <div className="rounded-lg border border-border bg-muted/50 p-4 text-sm text-muted-foreground">
        No spans to display.
      </div>
    );
  }

  return (
    <div ref={containerRef} className="relative block-card">
      <h3 className="block-title">Trace Waterfall</h3>
      <svg
        width={width}
        height={svgHeight}
        className="block select-none"
        onMouseLeave={() => setTooltip(null)}
      >
        {/* Ruler ticks and grid lines */}
        <g className="ruler">
          {ticks.map((t, i) => {
            const x = LABEL_WIDTH + xScale(t);
            return (
              <g key={i}>
                <line
                  x1={x}
                  y1={RULER_HEIGHT}
                  x2={x}
                  y2={svgHeight}
                  stroke="hsl(var(--border))"
                  strokeWidth={0.5}
                  strokeDasharray="4,4"
                />
                <text
                  x={x}
                  y={RULER_HEIGHT - 8}
                  textAnchor="middle"
                  className="fill-muted-foreground text-[10px]"
                >
                  {Math.round(t)}ms
                </text>
              </g>
            );
          })}
        </g>

        {/* Span rows */}
        {flat.map((span, idx) => {
          const y = RULER_HEIGHT + idx * ROW_HEIGHT;
          const indent = span.depth * 14;
          const barX = LABEL_WIDTH + xScale(span.start);
          const barW = Math.max(xScale(span.duration), 2);
          const isError = span.status.toLowerCase() === "error";
          const color = statusColor(span.status) || serviceColor(span.service);

          // Truncate label to fit
          const maxLabelLen = Math.floor((LABEL_WIDTH - indent - 18) / 6.5);
          const label = `${span.service}: ${span.operation}`;
          const truncatedLabel =
            label.length > maxLabelLen
              ? label.slice(0, Math.max(0, maxLabelLen - 1)) + "\u2026"
              : label;

          return (
            <g
              key={span.id}
              onMouseEnter={(e) => {
                const rect = containerRef.current?.getBoundingClientRect();
                if (rect) {
                  setTooltip({
                    x: e.clientX - rect.left,
                    y: e.clientY - rect.top,
                    span,
                  });
                }
              }}
              onMouseLeave={() => setTooltip(null)}
            >
              {/* Row background on hover */}
              <rect
                x={0}
                y={y}
                width={width}
                height={ROW_HEIGHT}
                fill="transparent"
                className="hover:fill-muted/50"
              />

              {/* Label: clickable service name + operation */}
              <text
                x={indent + 14}
                y={y + ROW_HEIGHT / 2 + 4}
                className="fill-foreground text-[11px]"
              >
                <tspan
                  className={`font-medium ${onAction ? "fill-blue-400 cursor-pointer" : ""}`}
                  onClick={() => onAction?.(`Diagnose ${span.service}`)}
                >
                  {span.service}
                </tspan>
                <tspan className="fill-muted-foreground">
                  {" "}
                  {truncatedLabel.slice(span.service.length + 2)}
                </tspan>
              </text>

              {/* Span bar */}
              <rect
                x={barX}
                y={y + BAR_PADDING}
                width={barW}
                height={ROW_HEIGHT - BAR_PADDING * 2}
                fill={color}
                opacity={0.85}
                rx={2}
                stroke={isError ? COLORS.unhealthy : "none"}
                strokeWidth={isError ? 1.5 : 0}
              />

              {/* Duration label */}
              <text
                x={barX + barW + 6}
                y={y + ROW_HEIGHT / 2 + 4}
                className="fill-muted-foreground text-[10px]"
              >
                {span.duration < 1
                  ? `${(span.duration * 1000).toFixed(0)}\u00b5s`
                  : `${Math.round(span.duration)}ms`}
              </text>
            </g>
          );
        })}
      </svg>

      {/* Service legend */}
      <div className="mt-2 flex flex-wrap gap-3 px-1">
        {services.map((svc) => (
          <div key={svc} className="flex items-center gap-1.5 text-xs">
            <span
              className="inline-block h-2.5 w-2.5 rounded-sm"
              style={{ backgroundColor: serviceColor(svc) }}
            />
            <span className="text-muted-foreground">{svc}</span>
          </div>
        ))}
        <div className="flex items-center gap-1.5 text-xs">
          <span
            className="inline-block h-2.5 w-2.5 rounded-sm"
            style={{ backgroundColor: COLORS.unhealthy }}
          />
          <span className="text-muted-foreground">Error</span>
        </div>
      </div>

      {/* Tooltip */}
      {tooltip && (
        <div
          className="pointer-events-none absolute z-50 rounded-md border border-border bg-popover px-3 py-2 text-xs text-popover-foreground shadow-md"
          style={{
            left: Math.min(tooltip.x + 12, width - 200),
            top: tooltip.y - 8,
          }}
        >
          <div className="mb-1 font-medium">
            {tooltip.span.service}: {tooltip.span.operation}
          </div>
          <div className="space-y-0.5 text-muted-foreground">
            <div className="flex justify-between gap-4">
              <span>Duration</span>
              <span className="font-mono text-foreground">
                {tooltip.span.duration}ms
              </span>
            </div>
            <div className="flex justify-between gap-4">
              <span>Start</span>
              <span className="font-mono text-foreground">
                {tooltip.span.start}ms
              </span>
            </div>
            <div className="flex justify-between gap-4">
              <span>Status</span>
              <span
                className="font-mono"
                style={{
                  color: statusColor(tooltip.span.status) || "inherit",
                }}
              >
                {tooltip.span.status}
              </span>
            </div>
            <div className="flex justify-between gap-4">
              <span>Span ID</span>
              <span className="font-mono text-foreground">
                {tooltip.span.id}
              </span>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
