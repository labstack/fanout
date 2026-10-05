import { Box, Group, Text } from "@mantine/core";
import { previousStatValue as previousStat } from "./previous";
import { statValue, sparkline } from "../../../../panels/frame";
import { statusFor, thresholdDirection } from "../../../../panels/thresholds";
import type { Panel, PanelResult } from "../../../../panels/types";
import { formatValue } from "../../../../panels/units";
import { StatusChip } from "../status-chip";

export function StatViz({ panel, result }: { panel: Panel; result: PanelResult }) {
  const frame = result.frame!;
  const value = statValue(panel, frame);
  const unit = frame.columns.find((c) => c.role === "measure")?.unit ?? panel.unit;
  const better = panel.better ?? result.better ?? thresholdDirection(panel.thresholds);
  const status = statusFor(value, panel.thresholds, better);
  const previous = previousStat(panel, result);
  const points = sparkline(frame);
  return <Box h="100%" style={{ display: "flex", flexDirection: "column", justifyContent: "space-between", gap: 4 }}>
    <Group justify="space-between" align="flex-start" wrap="nowrap" gap="xs">
      <Text fz={30} fw={600} lh={1.1} lts="-0.02em" ff="var(--mantine-font-family-monospace)">{formatValue(unit, value)}</Text>
      {status && <StatusChip status={status} />}
    </Group>
    {previous !== null && value !== null && <Delta value={value} previous={previous} better={better} unit={unit} />}
    <Sparkline points={points} />
  </Box>;
}

function Delta({ value, previous, better, unit }: { value: number; previous: number; better?: "lower" | "higher"; unit?: string }) {
  const percentUnit = unit === "percent";
  const change = percentUnit ? value - previous : previous === 0 ? (value === 0 ? 0 : Infinity) : ((value - previous) / Math.abs(previous)) * 100;
  const up = change >= 0;
  const flat = Math.abs(change) < 2;
  const good = flat || !better ? null : (up ? better === "higher" : better === "lower");
  const text = !Number.isFinite(change) ? "new" : percentUnit ? `${up ? "+" : "−"}${Math.abs(change).toFixed(1)} pts` : `${up ? "+" : "−"}${Math.abs(change).toFixed(0)}%`;
  return <Group gap={6}>
    <Text size="xs" fw={600} c={good === null ? "dimmed" : good ? "ok" : "bad"}>{up ? "▲" : "▼"} {text}</Text>
    <Text size="xs" c="dimmed">vs previous period</Text>
  </Group>;
}

function Sparkline({ points }: { points: (number | null)[] }) {
  const present = points.filter((p): p is number => p !== null);
  if (present.length < 2) return null;
  const min = Math.min(...present);
  const max = Math.max(...present);
  const w = 200;
  const h = 32;
  const x = (i: number) => (i / (points.length - 1)) * w;
  const y = (v: number) => h - 2 - ((v - min) / (max - min || 1)) * (h - 4);
  const line = points.map((p, i) => (p === null ? "" : `${i === 0 || points[i - 1] === null ? "M" : "L"}${x(i).toFixed(1)},${y(p).toFixed(1)}`)).join("");
  return <svg viewBox={`0 0 ${w} ${h}`} preserveAspectRatio="none" width="100%" height={h} aria-hidden>
    <path d={line} fill="none" stroke="var(--mantine-primary-color-filled)" strokeWidth={1.5} vectorEffect="non-scaling-stroke" />
  </svg>;
}
