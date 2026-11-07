import { Box, Group, Paper, Skeleton, Stack } from "@mantine/core";
import { LoadingSkeleton } from "../loading-skeleton";
import { margin, pixels } from "./layout";

export function DashboardSkeleton() {
  return <Stack gap="md" data-dashboard-skeleton>
    <Stack gap="sm" data-dashboard-header-skeleton>
      <Group justify="space-between"><Skeleton height={28} width="32%" /><Skeleton height={28} width={28} /></Group>
      <Skeleton height={12} width="52%" />
      <Group justify="space-between"><Skeleton height={32} width={180} /><Skeleton height={32} width={120} /></Group>
    </Stack>
    <Box style={{ containerType: "inline-size" }}>
      <Box className="dashboard-loading-grid" style={{ display: "grid", gridTemplateColumns: "repeat(12, minmax(0, 1fr))", gap: margin }}>
        {(["stat", "stat", "stat", "timeseries", "table"] as const).map((viz, i) => <Paper key={i} withBorder p="md" className="dashboard-loading-panel" data-skeleton-columns={viz === "stat" ? 4 : 6} style={{ display: "flex", flexDirection: "column", minHeight: pixels(viz === "stat" ? 4 : 6) }}>
          <Skeleton height={14} width="44%" mb="xl" />
          <LoadingSkeleton viz={viz} rows={4} />
        </Paper>)}
      </Box>
    </Box>
  </Stack>;
}
