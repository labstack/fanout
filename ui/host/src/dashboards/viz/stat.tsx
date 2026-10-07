import { Box, Group, Text } from "@mantine/core";
import { previousStatValue as previousStat } from "./previous";
import { statValue, sparkline } from "../../../../panels/frame";
import { statusFor, thresholdDirection } from "../../../../panels/thresholds";
import type { Panel, PanelResult } from "../../../../panels/types";
import { formatValue } from "../../../../panels/units";
import { seriesSlot } from "../../../../chart";
import { chartThemeFor } from "../../../../panels/compile";

export function StatViz({ panel, result, dark = false, compare = true, range }: { panel: Panel; result: PanelResult; dark?: boolean; compare?: boolean; range?: string }) {
  const frame = result.frame!;
  const value = statValue(panel, frame);
  const unit = frame.columns.find((c) => c.role === "measure")?.unit ?? panel.unit;
  const better = panel.better ?? result.better ?? thresholdDirection(panel.thresholds);
  const status = statusFor(value, panel.thresholds, better) ?? (frame.health?.health === "healthy" ? "ok" : frame.health?.health === "degraded" ? "warn" : frame.health?.health === "unhealthy" ? "bad" : null);
  const previous = previousStat(panel, result);
  const points = sparkline(frame);
  const thresholdLabel = panel.thresholds?.find(t => t.status === status && value !== null && (better === "higher" ? value <= t.value : value >= t.value))?.label;
  const theme = chartThemeFor(dark);
  return <Box h="100%" style={{ display: "flex", flexDirection: "column", justifyContent: "space-between", gap: 4 }}>
    <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs">
      <Text data-stat-value fz={32} fw={700} lh={1.1} lts="-0.02em" ff="var(--mantine-font-family-monospace)">{formatValue(unit, value)}</Text>
      {status && <Text data-stat-status fz={12} fw={500} style={{ borderRadius: 999, padding: "1px 8px", whiteSpace: "nowrap", background: `${theme.status[status]}22`, color: theme.text }}><span style={{ color: theme.status[status] }}>{status === "bad" ? "◆" : status === "warn" ? "■" : "●"}</span> {thresholdLabel ?? (status === "bad" ? "Unhealthy" : status === "warn" ? "Degraded" : "Healthy")}</Text>}
    </Group>
    {compare && previous !== null && value !== null && <Delta value={value} previous={previous} better={better} unit={unit} period={previousPeriod(range, result)} />}
    <Sparkline points={points} area dark={dark} height={40} />
  </Box>;
}

function Delta({ value, previous, better, unit, period }: { value: number; previous: number; better?: "lower" | "higher"; unit?: string; period: string }) {
  const percentUnit = unit === "percent";
  const change = percentUnit ? value - previous : previous === 0 ? (value === 0 ? 0 : Infinity) : ((value - previous) / Math.abs(previous)) * 100;
  const up = change >= 0;
  const good = change === 0 || !better ? null : (up ? better === "higher" : better === "lower");
  const text = !Number.isFinite(change) ? "new" : percentUnit ? `${up ? "+" : "−"}${Math.abs(change).toFixed(1)} pts` : `${up ? "+" : "−"}${Math.abs(change).toFixed(0)}%`;
  return <Group gap={6}>
    <Text data-stat-delta fz={12} fw={600} style={{ color: good === null ? "var(--mantine-color-dimmed)" : `var(--mantine-color-${good ? "ok" : "bad"}-text)` }}>{up ? "▲" : "▼"} {text}</Text>
    <Text fz={12} c="dimmed">vs previous {period}</Text>
  </Group>;
}

function previousPeriod(range: string | undefined, result: PanelResult): string {
  const ms = result.from_ms !== undefined && result.to_ms !== undefined ? result.to_ms - result.from_ms : result.shift_ms;
  const match = /^(\d+)(s|m|h|d|w)$/.exec(range ?? "");
  const duration = match ? Number(match[1]) * ({ s: 1000, m: 60000, h: 3600000, d: 86400000, w: 604800000 }[match[2] as "s" | "m" | "h" | "d" | "w"]) : ms;
  if (!duration) return "period";
  const unit = duration >= 604800000 ? "week" : duration >= 86400000 ? "day" : duration >= 3600000 ? "hour" : duration >= 60000 ? "minute" : "second";
  const step = { week: 604800000, day: 86400000, hour: 3600000, minute: 60000, second: 1000 }[unit];
  const count = duration / step;
  return count === 1 ? unit : `${Number(count.toFixed(2))} ${unit}s`;
}

export function Sparkline({ points, area = false, dark = false, height = 32 }: { points: (number | null)[]; area?: boolean; dark?: boolean; height?: number }) {
  const present = points.filter((p): p is number => p !== null && Number.isFinite(p));
  if (present.length < 2) return null;
  const min = Math.min(...present);
  const max = Math.max(...present);
  const w = 200;
  const h = height;
  const x = (i: number) => (i / (points.length - 1)) * w;
  const y = (v: number) => h - 2 - ((v - min) / (max - min || 1)) * (h - 4);
  const line = points.map((p, i) => (p === null || !Number.isFinite(p) ? "" : `${i === 0 || points[i - 1] === null || !Number.isFinite(points[i - 1]) ? "M" : "L"}${x(i).toFixed(1)},${y(p).toFixed(1)}`)).join("");
  // Close each contiguous run independently so gaps never become filled data.
  const runs: { start: number; end: number; path: string }[] = [];
  points.forEach((p, i) => {
    if (p === null || !Number.isFinite(p)) return;
    if (!i || points[i - 1] === null || !Number.isFinite(points[i - 1])) runs.push({ start: i, end: i, path: `M${x(i).toFixed(1)},${y(p).toFixed(1)}` });
    else { const run = runs.at(-1)!; run.end = i; run.path += `L${x(i).toFixed(1)},${y(p).toFixed(1)}`; }
  });
  return <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" width="100%" height={h} role="img" aria-label="Value over this panel's time range">
    {area && runs.filter(run => run.end > run.start).map(run => <path key={run.start} data-sparkline-area d={`${run.path}L${x(run.end)},${h}L${x(run.start)},${h}Z`} fill={seriesSlot(0, dark)} opacity={0.18} />)}
    <path data-sparkline-line d={line} fill="none" stroke={area ? seriesSlot(0, dark) : "var(--mantine-primary-color-filled)"} strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
  </svg>;
}
