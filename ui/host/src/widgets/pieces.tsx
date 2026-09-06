import { Badge, Box, Button, Center, Paper, Text } from "@mantine/core";
import { ListMagnifyingGlass, WarningCircle } from "@phosphor-icons/react";
import type { ReactNode } from "react";
import { EChart } from "../echart";
import { healthColor } from "../../../chart";

/** A tile. Anything passed as children sits under the value, inside the tile's
 *  own border — a trend line belongs to the number it describes, and tiles in a
 *  grid row end level because each one fills its cell. */
export function Metric({ label, value, color, hint, children }: { label: string; value: string | number; color?: string; hint?: string; children?: ReactNode }) {
  return <Paper withBorder radius="md" p="sm" bg="var(--mantine-color-default)" miw={0} h="100%">
    <Text c="dimmed" size="xs" truncate>{label}</Text>
    <Text fw={600} fz="xl" c={color} mt={2} lh={1.2} truncate>{value}</Text>
    {hint && <Text c="dimmed" size="xs" mt={2} truncate>{hint}</Text>}
    {children && <Box mt={4}>{children}</Box>}
  </Paper>;
}

export function HealthBadge({ health, label }: { health: string; label: string }) {
  return <Badge color={healthColor(health)} variant="light" tt="none">{label}</Badge>;
}

export function Empty({ text }: { text: string }) {
  return <Center py="xl"><ListMagnifyingGlass size={20} /><Text c="dimmed" size="sm" ml="xs">{text}</Text></Center>;
}

export function WidgetError({ retry }: { retry: () => void }) {
  return <Center py="xl" style={{ flexDirection: "column", gap: 8 }}>
    <Box display="flex" style={{ alignItems: "center", gap: 8 }}><WarningCircle size={20} weight="fill" color="var(--mantine-color-bad-filled)" /><Text c="bad" fw={500} size="sm">Couldn't load this view</Text></Box>
    <Button size="compact-xs" variant="light" color="bad" onClick={retry}>Retry now</Button>
    <Text c="dimmed" size="xs">Retrying automatically</Text>
  </Center>;
}

/** A line with no axes, for a metric tile. */
export function Sparkline({ values, color, label }: { values: number[]; color: string; label: string }) {
  const option = {
    animation: false,
    grid: { left: 0, right: 0, top: 2, bottom: 2 },
    xAxis: { type: "category", show: false, data: values.map((_, index) => index) },
    yAxis: { type: "value", show: false, min: 0 },
    tooltip: { show: false },
    series: [{ type: "line", data: values, showSymbol: false, smooth: 0.3, lineStyle: { width: 1.5, color }, areaStyle: { opacity: 0.12, color } }],
  };
  return <EChart option={option} height={28} label={label} />;
}
