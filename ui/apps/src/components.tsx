import "@mantine/core/styles.css";
import { Alert, Badge, Box, Button, Center, Group, Loader, MantineProvider, Pagination, Paper, ScrollArea, Stack, Tabs as MantineTabs, Text, ThemeIcon, Title, Tooltip, createTheme } from "@mantine/core";
import { ArrowClockwise } from "@phosphor-icons/react";
import { useEffect, useState, type ReactNode } from "react";
import { fanoutThemeConfig } from "../../theme";

const fanoutTheme = createTheme(fanoutThemeConfig);

export function ViewShell({ dark, children }: { dark: boolean; children: ReactNode }) {
  return <MantineProvider theme={fanoutTheme} forceColorScheme={dark ? "dark" : "light"}><Paper withBorder radius="lg" style={{ overflow: "hidden" }}>{children}</Paper></MantineProvider>;
}

export function ViewHeader({ eyebrow, title, summary, onRefresh, disabled }: { eyebrow: string; title: string; summary?: string; onRefresh: () => void | Promise<unknown>; disabled?: boolean }) {
  return <Group justify="space-between" align="flex-start" wrap="nowrap" px={{ base: "md", sm: "lg" }} pt="md" pb="sm">
    <Box miw={0}><Text c="dimmed" size="xs" fw={700} tt="uppercase" lts="0.1em">{eyebrow}</Text><Title order={1} fz="lg" mt={2} style={{ overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" }}>{title}</Title>{summary && <Text c="dimmed" size="sm" mt={4}>{summary}</Text>}</Box>
    <Button variant="default" size="xs" leftSection={<ArrowClockwise size={15} weight="bold" />} onClick={() => void onRefresh()} disabled={disabled}>Refresh</Button>
  </Group>;
}

export function ViewStatus({ error, loading }: { error?: string | null; loading?: string }) {
  if (error) return <Alert color="red" m="md">{error}</Alert>;
  if (loading) return <Center mih={160} p="xl"><Loader size="sm" /><Text c="dimmed" size="sm" ml="sm">{loading}</Text></Center>;
  return null;
}

export function Tabs<T extends string>({ active, items, onChange }: { active: T; items: Array<{ id: T; label: string; count?: number }>; onChange: (id: T) => void }) {
  return <ScrollArea type="auto" offsetScrollbars scrollbarSize={6}><MantineTabs value={active} onChange={(value) => value && onChange(value as T)} variant="pills" px={{ base: "md", sm: "lg" }} pb="sm"><MantineTabs.List style={{ flexWrap: "nowrap" }}>{items.map((item) => <MantineTabs.Tab key={item.id} value={item.id} rightSection={item.count !== undefined ? <Badge size="xs" variant="light" circle>{item.count}</Badge> : undefined}>{item.label}</MantineTabs.Tab>)}</MantineTabs.List></MantineTabs></ScrollArea>;
}

export function Hint({ label, children }: { label: string; children: ReactNode }) {
  return <Tooltip label={label} multiline maw={280} withArrow>{children}</Tooltip>;
}

export function EmptyState({ icon, title, children, tall = false }: { icon: ReactNode; title: string; children: ReactNode; tall?: boolean }) {
  return <Center mih={tall ? 220 : 130} p="xl"><Group wrap="nowrap"><ThemeIcon variant="light" size="xl" radius="md">{icon}</ThemeIcon><Box><Text fw={700} size="sm">{title}</Text><Text c="dimmed" size="xs" mt={3}>{children}</Text></Box></Group></Center>;
}

export function MetaFooter({ left, right }: { left: ReactNode; right: ReactNode }) {
  return <Group justify="space-between" px={{ base: "md", sm: "lg" }} py="xs" style={{ borderTop: "1px solid var(--mantine-color-default-border)" }}><Text c="dimmed" size="xs">{left}</Text><Text c="dimmed" size="xs" ta="right">{right}</Text></Group>;
}

export function Metric({ label, value, color }: { label: string; value: ReactNode; color?: string }) {
  return <Paper withBorder radius="md" p="sm"><Text c="dimmed" size="xs">{label}</Text><Text fw={700} fz="xl" c={color} mt={3}>{value}</Text></Paper>;
}

export function usePagedItems<T>(items: T[], pageSize = 8) {
  const [page, setPage] = useState(1);
  const totalPages = Math.max(1, Math.ceil(items.length / pageSize));
  useEffect(() => { if (page > totalPages) setPage(totalPages); }, [page, totalPages]);
  const start = (page - 1) * pageSize;
  return {
    page,
    setPage,
    totalPages,
    pageItems: items.slice(start, start + pageSize),
    from: items.length === 0 ? 0 : start + 1,
    to: Math.min(start + pageSize, items.length),
    total: items.length,
  };
}

export function PageControls({ page, totalPages, from, to, total, onChange }: { page: number; totalPages: number; from: number; to: number; total: number; onChange: (page: number) => void }) {
  if (totalPages <= 1) return null;
  return <Group justify="space-between" gap="sm" px={{ base: "md", sm: "lg" }} py="xs" style={{ borderTop: "1px solid var(--mantine-color-default-border)" }}>
    <Text c="dimmed" size="xs">{from}–{to} of {total}</Text>
    <Pagination value={page} total={totalPages} onChange={onChange} size="xs" withEdges aria-label="Table pages" />
  </Group>;
}

export function healthColor(health: string) {
  return health === "healthy" ? "teal" : health === "degraded" ? "yellow" : "red";
}

export function chartTheme(dark: boolean) {
  return dark
    ? { text: "#c1c9c5", muted: "#8c9892", grid: "#303a35", surface: "#1b211e", border: "#38443e" }
    : { text: "#344039", muted: "#748078", grid: "#e5e9e6", surface: "#ffffff", border: "#d7ddd9" };
}
