import { Box, Group, Skeleton, Stack } from "@mantine/core";
import type { Viz } from "../../panels/types";

/** Shared body layouts for panels and pages; DataState owns announcements and hiding. */
export function LoadingSkeleton({ viz, columns = 4, rows = 5 }: { viz: Viz | "list"; columns?: number; rows?: number }) {
  const shape = viz === "stat" ? "stat" : viz === "gauge" ? "gauge" : viz === "heatmap" ? "heatmap"
    : viz === "text" ? "text" : viz === "list" || viz === "health" || viz === "service_map" ? "list"
    : ["table", "logs", "traces", "log_patterns"].includes(viz) ? "table" : "chart";
  return <Box data-skeleton-shape={shape} style={{ minWidth: 0, width: "100%" }}>
    {shape === "chart" ? <Stack gap="xs"><Skeleton height={160} radius="sm" /><Box data-skeleton-axis style={{ borderBottom: "1px solid var(--mantine-color-default-border)", marginInline: 8 }} /><Group justify="space-between"><Skeleton height={8} width="16%" /><Skeleton height={8} width="16%" /><Skeleton height={8} width="16%" /></Group></Stack>
      : shape === "stat" ? <Stack gap="md" justify="center" mih={120}><Skeleton height={48} width="52%" /><Skeleton height={12} width="34%" /></Stack>
      : shape === "gauge" ? <Stack gap="lg" justify="center" mih={140}><Skeleton height={40} width="40%" /><Skeleton height={16} radius="xl" /><Group justify="space-between"><Skeleton height={10} width="18%" /><Skeleton height={10} width="18%" /></Group></Stack>
      : shape === "heatmap" ? <Box style={{ display: "grid", gridTemplateColumns: "repeat(8, 1fr)", gap: 4 }}>{Array.from({ length: 40 }, (_, i) => <Skeleton key={i} height={24} radius={2} />)}</Box>
      : shape === "text" ? <Stack gap="sm">{[95, 100, 88, 96, 62].map((width, i) => <Skeleton key={i} height={12} width={`${width}%`} />)}</Stack>
      : shape === "table" ? <Stack gap={0}>{Array.from({ length: rows + 1 }, (_, row) => <Box key={row} data-skeleton-row={row === 0 ? "header" : "body"} style={{ display: "grid", gridTemplateColumns: `repeat(${columns}, minmax(0, 1fr))`, gap: 16, paddingBlock: 12, borderBottom: "1px solid var(--mantine-color-default-border)" }}>{Array.from({ length: columns }, (_, col) => <Skeleton key={col} height={row === 0 ? 10 : 14} width={col === columns - 1 ? "60%" : "85%"} />)}</Box>)}</Stack>
      : <Stack gap="md">{Array.from({ length: rows }, (_, i) => <Group key={i} wrap="nowrap"><Skeleton height={32} width={32} radius="sm" /><Stack gap={8} style={{ flex: 1 }}><Skeleton height={12} width="45%" /><Skeleton height={10} width="70%" /></Stack></Group>)}</Stack>}
  </Box>;
}
