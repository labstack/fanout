import { Box, Paper, Text } from "@mantine/core";
import { useMemo, type ReactNode } from "react";
import { healthColor, healthSymbol, healthBorderType } from "../../../../chart";
import { EChartCanvas } from "../echart-canvas";

export function Metric({ label, value, color, hint, children }: { label: string; value: ReactNode; color?: string; hint?: string; children?: ReactNode }) {
  return <Paper withBorder radius="md" p="sm" bg="var(--mantine-color-default)" miw={0} h="100%">
    <Text c="dimmed" size="xs" truncate>{label}</Text>
    <Box fw={600} fz="xl" c={color} mt={2} lh={1.2}>{value}</Box>
    {hint && <Text c="dimmed" size="xs" mt={2}>{hint}</Text>}
    {children && <Box mt={4}>{children}</Box>}
  </Paper>;
}

export function HealthShape({ health }: { health: string }) {
  const symbol = healthSymbol(health);
  const color = `var(--mantine-color-${healthColor(health)}-filled)`;
  return <svg aria-hidden width={12} height={12} viewBox="0 0 12 12" fill={health === "unknown" ? "none" : color} stroke={color}
    strokeDasharray={healthBorderType(health) === "dashed" ? "2 2" : undefined}>
    {symbol === "diamond" ? <polygon points="6,0 12,6 6,12 0,6" />
      : symbol === "roundRect" ? <rect x={1} y={1} width={10} height={10} rx={2} /> : <circle cx={6} cy={6} r={5} />}
  </svg>;
}

export function HealthTrend({ values, color }: { values: number[]; color: string }) {
  const finite = useMemo(() => values.map(value => Number.isFinite(value) ? value : null), [values]);
  const peak = Math.max(0, ...finite.filter((value): value is number => value !== null));
  const option = useMemo(() => ({
    animation: false, grid: { left: 0, right: 0, top: 2, bottom: 2 },
    xAxis: { type: "category", show: false, data: finite.map((_, index) => index) }, yAxis: { type: "value", show: false, min: 0 },
    tooltip: { show: false },
    series: [{ type: "line", data: finite, showSymbol: false, smooth: .3, lineStyle: { width: 1.5, color }, areaStyle: { opacity: .12, color } }],
  }), [finite, color]);
  return <Box>
    <EChartCanvas option={option} height={28} label="Error rate trend" />
    {peak > 0 && <Text c="dimmed" size="xs" ta="right" mt={2}>peak {peak.toFixed(2)}%</Text>}
  </Box>;
}
