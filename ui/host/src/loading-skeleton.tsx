import { KeyValueList } from "@labstack/ui";
import { Box, Group, Paper, Skeleton, Stack } from "@mantine/core";
import type { Viz } from "../../panels/types";

type Shape = "chart" | "stat" | "gauge" | "heatmap" | "text" | "table" | "health" | "graph" | "key_value" | "agents" | "settings";

const vizShapes: Record<Viz, Shape> = {
  stat: "stat", gauge: "gauge", timeseries: "chart", bar: "chart", table: "table", text: "text",
  heatmap: "heatmap", histogram: "chart", scatter: "chart", state_timeline: "chart",
  logs: "table", log_patterns: "table", traces: "table", service_map: "graph", health: "health",
};

type Props = ({ viz: Viz; shape?: never } | { shape: Shape; viz?: never }) & { columns?: number; rows?: number };

/** Shared body layouts for panels and pages; DataState owns announcements and hiding. */
export function LoadingSkeleton({ viz, shape: pageShape, columns = 4, rows = 5 }: Props) {
  const shape = viz === undefined ? pageShape : vizShapes[viz];
  return <Box data-skeleton-shape={shape} style={{ minWidth: 0, width: "100%", minHeight: 0, flex: 1, display: "flex", flexDirection: "column", overflow: "hidden" }}>
    {shape === "chart" ? <Stack gap="xs" style={{ flex: 1, minHeight: 0 }}><Skeleton data-skeleton-chart radius="sm" style={{ flex: 1, minHeight: 0 }} /><Box data-skeleton-axis style={{ borderBottom: "1px solid var(--mantine-color-default-border)", marginInline: 8 }} /><Group justify="space-between"><Skeleton height={8} width="16%" /><Skeleton height={8} width="16%" /><Skeleton height={8} width="16%" /></Group></Stack>
      : shape === "stat" ? <Stack gap="md" justify="center" mih={120}><Skeleton height={48} width="52%" /><Skeleton height={12} width="34%" /></Stack>
      : shape === "gauge" ? <Stack gap="lg" justify="center" mih={140}><Skeleton height={40} width="40%" /><Skeleton height={16} radius="xl" /><Group justify="space-between"><Skeleton height={10} width="18%" /><Skeleton height={10} width="18%" /></Group></Stack>
      : shape === "heatmap" ? <Box style={{ display: "grid", gridTemplateColumns: "repeat(8, 1fr)", gap: 4 }}>{Array.from({ length: 40 }, (_, i) => <Skeleton key={i} height={24} radius={2} />)}</Box>
      : shape === "text" ? <Stack gap="sm">{[95, 100, 88, 96, 62].map((width, i) => <Skeleton key={i} height={12} width={`${width}%`} />)}</Stack>
      : shape === "table" ? <Stack gap={0}>{Array.from({ length: rows + 1 }, (_, row) => <Box key={row} data-skeleton-row={row === 0 ? "header" : "body"} style={{ display: "grid", gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))`, gap: 16, paddingBlock: 12, borderBottom: "1px solid var(--mantine-color-default-border)" }}>{Array.from({ length: columns }, (_, col) => <Skeleton key={col} height={row === 0 ? 10 : 14} width={col === columns - 1 ? "60%" : "85%"} />)}</Box>)}</Stack>
      : shape === "key_value" ? <Box><Skeleton height={12} width="24%" mb={4} /><KeyValueList items={Array.from({ length: rows }, (_, i) => ({ label: <Skeleton data-skeleton-label={i} height={21} width="75%" />, value: <Skeleton data-skeleton-value={i} height={21} width="85%" /> }))} /></Box>
      : shape === "health" ? <Box style={{ display: "grid", gridTemplateColumns: "repeat(auto-fit, minmax(120px, 1fr))", gap: 8 }}>{Array.from({ length: 6 }, (_, i) => <Paper key={i} withBorder p="sm" data-skeleton-tile><Stack gap="xs"><Skeleton height={12} width="65%" /><Skeleton height={24} width="45%" /><Skeleton height={10} width="85%" /></Stack></Paper>)}</Box>
      : shape === "graph" ? <Box style={{ position: "relative", flex: 1, minHeight: 80 }}><svg aria-hidden="true" width="100%" height="100%" viewBox="0 0 100 100" preserveAspectRatio="none" style={{ position: "absolute", inset: 0 }}><path data-skeleton-edges d="M20 50 L50 20 L80 50 L50 80 Z M20 50 H80" fill="none" stroke="var(--mantine-color-default-border)" vectorEffect="non-scaling-stroke" /></svg>{[[20, 50], [50, 20], [80, 50], [50, 80]].map(([x, y], i) => <Skeleton key={i} data-skeleton-node width="22%" height={32} style={{ position: "absolute", left: `${x}%`, top: `${y}%`, transform: "translate(-50%, -50%)" }} />)}</Box>
      : shape === "settings" ? <Stack gap="xl">{Array.from({ length: 3 }, (_, i) => <Stack key={i} gap="md" data-skeleton-section><Skeleton height={20} width="25%" /><Skeleton height={12} width="55%" /><Paper withBorder radius="lg" p="lg"><Skeleton height={96} radius="sm" /></Paper></Stack>)}</Stack>
      : <Stack gap="md">{Array.from({ length: rows }, (_, i) => <Group key={i} wrap="nowrap" justify="space-between"><Stack gap={8} style={{ flex: 1 }}><Skeleton height={12} width="45%" /><Skeleton height={10} width="70%" /></Stack><Skeleton height={36} width={96} /></Group>)}</Stack>}
  </Box>;
}
