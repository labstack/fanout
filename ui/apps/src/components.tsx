import * as TabsPrimitive from "@radix-ui/react-tabs";
import * as TooltipPrimitive from "@radix-ui/react-tooltip";
import { ArrowClockwise } from "@phosphor-icons/react";
import type { ReactNode } from "react";

export function Tabs<T extends string>({ active, items, onChange }: { active: T; items: Array<{ id: T; label: string; count?: number }>; onChange: (id: T) => void }) {
  return <TabsPrimitive.Root value={active} onValueChange={(value) => onChange(value as T)}><TabsPrimitive.List className="tabs" aria-label="Visualization view">{items.map((item) => <TabsPrimitive.Trigger key={item.id} value={item.id}>{item.label}{item.count !== undefined && <span>{item.count}</span>}</TabsPrimitive.Trigger>)}</TabsPrimitive.List></TabsPrimitive.Root>;
}

export function Hint({ label, children }: { label: string; children: ReactNode }) {
  return <TooltipPrimitive.Provider delayDuration={250}><TooltipPrimitive.Root><TooltipPrimitive.Trigger asChild>{children}</TooltipPrimitive.Trigger><TooltipPrimitive.Portal><TooltipPrimitive.Content className="tooltip" sideOffset={6}>{label}<TooltipPrimitive.Arrow className="tooltip-arrow" /></TooltipPrimitive.Content></TooltipPrimitive.Portal></TooltipPrimitive.Root></TooltipPrimitive.Provider>;
}

export function RefreshButton({ disabled, onClick }: { disabled?: boolean; onClick: () => void | Promise<unknown> }) {
  return <button className="refresh" onClick={() => void onClick()} disabled={disabled}><ArrowClockwise size={15} weight="bold" aria-hidden="true" />Refresh</button>;
}

export function EmptyState({ icon, title, children }: { icon: ReactNode; title: string; children: ReactNode }) {
  return <section className="empty-state rich-empty"><span className="empty-icon" aria-hidden="true">{icon}</span><div><strong>{title}</strong><p>{children}</p></div></section>;
}

export interface ChartSeries { label: string; values: number[]; color: string; format?: (value: number) => string }

export function SeriesChart({ labels, series, height = 150 }: { labels: string[]; series: ChartSeries[]; height?: number }) {
  if (labels.length === 0 || series.every((item) => item.values.length === 0)) return null;
  const width = 760;
  const left = 22;
  const right = 12;
  const top = 14;
  const bottom = 25;
  const innerWidth = width - left - right;
  const innerHeight = height - top - bottom;
  const all = series.flatMap((item) => item.values).filter(Number.isFinite);
  const min = Math.min(0, ...all);
  const max = Math.max(...all, 1);
  const x = (index: number) => left + index * innerWidth / Math.max(labels.length - 1, 1);
  const y = (value: number) => top + (max - value) * innerHeight / Math.max(max - min, 1);
  return <div className="series-chart">
    <div className="chart-legend">{series.map((item) => <span key={item.label}><i style={{ background: item.color }} /><span>{item.label}</span><strong>{item.format?.(item.values.at(-1) ?? 0) ?? (item.values.at(-1) ?? 0).toLocaleString()}</strong></span>)}</div>
    <svg viewBox={`0 0 ${width} ${height}`} role="img" aria-label={`${series.map((item) => item.label).join(", ")} over time`}>
      {[0, .5, 1].map((value) => <line key={value} x1={left} x2={width - right} y1={top + innerHeight * value} y2={top + innerHeight * value} className="grid-line" />)}
      {series.map((item) => <polyline key={item.label} points={item.values.map((value, index) => `${x(index)},${y(value)}`).join(" ")} fill="none" stroke={item.color} strokeWidth="2.2" strokeLinejoin="round" strokeLinecap="round" />)}
      {labels.length > 0 && <><text x={left} y={height - 6}>{shortTime(labels[0])}</text><text x={width - right} y={height - 6} textAnchor="end">{shortTime(labels.at(-1)!)}</text></>}
    </svg>
  </div>;
}

function shortTime(value: string) {
  const time = new Date(value);
  return Number.isNaN(time.valueOf()) ? value : time.toLocaleTimeString([], { hour: "numeric", minute: "2-digit" });
}
