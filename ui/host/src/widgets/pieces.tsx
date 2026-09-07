import { Badge, Box, Button, Center, Paper, Text, VisuallyHidden } from "@mantine/core";
import { ListMagnifyingGlass, WarningCircle } from "@phosphor-icons/react";
import { LineChart } from "echarts/charts";
import { useMemo, type ReactNode } from "react";
import { EChart, useECharts } from "../echart";
import { healthColor } from "../../../chart";
import { typeScale } from "../../../tokens";

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

/** Health states, in the order severity reads. The glyph is the second channel:
 *  the badge was hue alone, which is the one channel a reader with a colour
 *  vision deficiency does not have. */
const healthGlyph: Record<string, string> = { unhealthy: "◆", degraded: "■", healthy: "●", unknown: "○" };

const healthWord: Record<string, string> = { unhealthy: "unhealthy", degraded: "degraded", healthy: "healthy", unknown: "health unknown" };

export function HealthBadge({ health, label }: { health: string; label: string }) {
  // A Badge is an inline-grid with hidden overflow, so a narrow row collapses
  // its track and the label measures zero. It keeps its content's width.
  //
  // The glyph is a second channel for the eye and carries nothing for a screen
  // reader, which heard the service name and no state at all — in the one
  // component whose whole point is that hue is not enough. The state is added
  // as text rather than as an aria-label: a Badge renders a bare div, which
  // maps to role=generic, where a name from the author is discarded.
  return <Badge color={healthColor(health)} variant="light" tt="none" style={{ minWidth: "max-content" }}
    leftSection={<Box component="span" aria-hidden style={{ fontSize: typeScale.micro, lineHeight: 1 }}>{healthGlyph[health] ?? healthGlyph.unknown}</Box>}>
    {label}<VisuallyHidden> — {healthWord[health] ?? healthWord.unknown}</VisuallyHidden>
  </Badge>;
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

// The sparkline draws a line, so it registers the line chart here rather than
// relying on whichever widget happened to import it first.
useECharts([LineChart]);

/** A line with no axes, for a metric tile.
 *
 *  A trend with no scale is decoration: the reader cannot tell a line hovering
 *  near zero from one near its own peak, which is the only question a sparkline
 *  is asked. Stating the peak answers it. A zero rule does not — the axis is
 *  pinned at zero and auto-scales its top, so the shape fills the box either
 *  way and the rule lands on the floor of the plot. */
export function Sparkline({ values, color, label, format }: { values: number[]; color: string; label: string; format?: (value: number) => string }) {
  const peak = values.length ? Math.max(...values) : 0;
  const option = useMemo(() => ({
    animation: false,
    grid: { left: 0, right: 0, top: 2, bottom: 2 },
    xAxis: { type: "category", show: false, data: values.map((_, index) => index) },
    yAxis: { type: "value", show: false, min: 0 },
    tooltip: { show: false },
    // Decals are the chart layer's second channel for colour vision, and they
    // stay on everywhere a chart distinguishes one series from another. A
    // sparkline draws a single series 28px tall, where the texture only muddies
    // the shape it is meant to support. `description` is repeated because
    // EChart spreads the option over its defaults, and dropping it lets ECharts
    // generate an aria-label that reads out the data instead of the label.
    aria: { enabled: true, decal: { show: false }, description: label },
    series: [{
      type: "line", data: values, showSymbol: false, smooth: 0.3,
      lineStyle: { width: 1.5, color }, areaStyle: { opacity: 0.12, color },
    }],
  }), [values, color, label]);
  return <Box>
    <EChart option={option} height={28} label={label} />
    {peak > 0 && format && <Text c="dimmed" size="xs" ta="right" mt={2}>peak {format(peak)}</Text>}
  </Box>;
}
