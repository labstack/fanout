import { Accordion, Alert, Badge, Box, Button, Code, Drawer, Group, Loader, Paper, ScrollArea, Stack, Text, Title } from "@mantine/core";
import { useMutation, useQuery, useQueryClient, type Query } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { exactTimestamp } from "../../../format";
import { changeLabel } from "../dashboard-receipt";
import { ApiError, dashboardsKey, getVersion, listVersions, restoreVersion, type DashboardRecord, type VersionInfo, type VersionRecord } from "./api";

type HistoryProps = { id: string; currentVersion: number; opened: boolean; onClose(): void; onRestored(record: DashboardRecord): void };

// Annotations share the panel batch cache. Cancel and invalidate that batch and
// variable options even when a restored spec happens to match a cached key.
function isDashboardData(query: Query, id: string): boolean {
  const [kind, scope] = query.queryKey;
  if (kind === "variables") return scope === `dashboard-${id}`;
  if (kind !== "panels" || typeof scope !== "string") return false;
  try { return JSON.parse(scope)[0] === id; } catch { return false; }
}

export function HistoryDrawer({ id, currentVersion, opened, onClose, onRestored }: HistoryProps) {
  const client = useQueryClient();
  const [selected, setSelected] = useState<number>();
  const [success, setSuccess] = useState("");
  // Synchronous guard covers clicks in the same render and closing/reopening
  // during a POST. Mutation retries are disabled; a new click is required.
  const submitting = useRef(false);
  const mounted = useRef(false);
  const restoredCallback = useRef(onRestored);
  restoredCallback.current = onRestored;
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const restore = useMutation({
    mutationFn: (version: number) => restoreVersion(id, version),
    retry: false,
    onSuccess: async (record, version) => {
      await Promise.all([
        client.cancelQueries({ queryKey: ["dashboard", id], exact: true }),
        client.cancelQueries({ predicate: query => isDashboardData(query, id) }),
        client.cancelQueries({ queryKey: ["exemplars"] }),
        client.cancelQueries({ queryKey: ["drill-trace"] }),
      ]);
      client.setQueryData(["dashboard", id], record);
      if (mounted.current) {
        setSuccess(`Restored v${version} as v${record.version}`);
        restoredCallback.current(record);
      }
      await Promise.all([
        client.invalidateQueries({ queryKey: dashboardsKey }),
        client.invalidateQueries({ queryKey: ["dashboard-versions", id] }),
        client.invalidateQueries({ predicate: query => isDashboardData(query, id) }),
        client.invalidateQueries({ queryKey: ["exemplars"] }),
        client.invalidateQueries({ queryKey: ["drill-trace"] }),
      ]);
    },
    onSettled: () => { submitting.current = false; },
  });
  const submit = (version: number) => {
    if (submitting.current) return;
    submitting.current = true;
    setSuccess("");
    restore.mutate(version);
  };
  return <Drawer opened={opened} onClose={onClose} title="Version history" position="right" size="xl">
    {opened && <HistoryContent id={id} currentVersion={currentVersion} selected={selected} onSelect={setSelected} pending={restore.isPending} error={restore.error} success={success} onRestore={submit} />}
  </Drawer>;
}

function HistoryContent({ id, currentVersion, selected, onSelect, pending, error, success, onRestore }: {
  id: string; currentVersion: number; selected?: number; onSelect(version: number): void;
  pending: boolean; error: Error | null; success: string; onRestore(version: number): void;
}) {
  const list = useQuery({ queryKey: ["dashboard-versions", id], queryFn: ({ signal }) => listVersions(id, signal), retry: false });
  const version = selected ?? list.data?.[0]?.version;
  const detail = useQuery({ queryKey: ["dashboard-version", id, version], queryFn: ({ signal }) => getVersion(id, version!, signal), enabled: version !== undefined, retry: false });
  return <Stack gap="md" data-version-history>
    <Text size="sm" c="dimmed">Inspect saved specs and changes. Restoring adds a new version.</Text>
    {success && <Alert color="good" role="status">{success}</Alert>}
    {error && <Alert color="bad" title="Restore failed">{error.message} Try again with the restore button.</Alert>}
    <Box mih={160}>
      {list.isPending && <Group gap="xs" role="status"><Loader size="sm" /><Text size="sm">Loading history…</Text></Group>}
      {list.error && <Alert color="bad" title="History unavailable">{list.error.message}<Button mt="xs" variant="default" size="xs" onClick={() => void list.refetch()}>Retry history</Button></Alert>}
      {list.data?.length === 0 && <Text c="dimmed">No versions available.</Text>}
      {!!list.data?.length && <ScrollArea h={200} type="auto" aria-label="Saved versions">
        <Stack gap="xs" pr="sm">
          {list.data.map(item => <Paper key={item.version} withBorder p="xs" radius="sm" style={{ borderColor: version === item.version ? "var(--mantine-color-accent-filled)" : undefined }}>
            <Group justify="space-between" gap="xs">
              <Button variant={version === item.version ? "light" : "subtle"} size="compact-sm" aria-pressed={version === item.version} onClick={() => onSelect(item.version)}>Version {item.version}</Button>
              {item.version === currentVersion && <Badge color="gray" variant="light" tt="none">Current</Badge>}
              <VersionTime at={item.created_at} />
            </Group>
            <Text size="xs" c="dimmed" mt={4}>{authorLabel(item)} · {item.message || "No edit message"}</Text>
          </Paper>)}
        </Stack>
      </ScrollArea>}
    </Box>
    <Box mih={280} aria-live="polite" aria-busy={detail.isFetching}>
      {version !== undefined && detail.isPending && <Group gap="xs" role="status"><Loader size="sm" /><Text size="sm">Loading version…</Text></Group>}
      {detail.error && <Alert color="bad" title={detail.error instanceof ApiError && detail.error.status === 404 ? "This version is no longer available" : "Version unavailable"}>
        {detail.error.message}<Button mt="xs" variant="default" size="xs" onClick={() => void detail.refetch()}>Retry version</Button>
      </Alert>}
      {detail.data && !detail.error && <VersionDetails record={detail.data} pending={pending} onRestore={() => onRestore(detail.data.dashboard.version)} />}
    </Box>
  </Stack>;
}

function authorLabel(info: Pick<VersionInfo, "author_kind">): string {
  return info.author_kind === "agent" ? "Agent" : info.author_kind === "user" ? "You" : "System";
}

function VersionTime({ at }: { at: string }) {
  const seconds = (Date.parse(at) - Date.now()) / 1000;
  const [divisor, unit]: [number, Intl.RelativeTimeFormatUnit] = Math.abs(seconds) < 60 ? [1, "second"] : Math.abs(seconds) < 3600 ? [60, "minute"] : Math.abs(seconds) < 86400 ? [3600, "hour"] : [86400, "day"];
  return <Text component="time" dateTime={at} title={exactTimestamp(at)} size="xs" c="dimmed">{new Intl.RelativeTimeFormat(undefined, { numeric: "auto" }).format(Math.round(seconds / divisor), unit)}</Text>;
}

function VersionDetails({ record, pending, onRestore }: { record: VersionRecord; pending: boolean; onRestore(): void }) {
  const { dashboard } = record;
  const labels = [...record.changes.map(changeLabel), ...record.dashboard_fields.map(field => `Dashboard: ${field}`)];
  return <Stack gap="sm" data-history-version={dashboard.version}>
    <Group justify="space-between" align="flex-start">
      <Box miw={0}><Title order={2} fz="lg">{dashboard.name} · v{dashboard.version}</Title><Text size="sm" c="dimmed">{authorLabel(record)} · {record.message || "No edit message"}</Text><VersionTime at={record.created_at} /></Box>
      <Button size="sm" disabled={pending} loading={pending} onClick={onRestore}>Restore version {dashboard.version}</Button>
    </Group>
    {dashboard.description && <Text size="sm">{dashboard.description}</Text>}
    {record.changes_available ? <>
      <Group gap={4} data-edit-chips>{labels.map((label, index) => <Badge key={index} color="gray" variant="light" size="sm" tt="none" maw="100%" title={label} style={{ color: "var(--mantine-color-text)", height: "auto", whiteSpace: "normal", overflowWrap: "anywhere" }}>{label}</Badge>)}</Group>
      {record.layout_changed && <Text size="xs" c="dimmed">layout changed</Text>}
      {!labels.length && !record.layout_changed && <Text size="sm" c="dimmed">No authored changes.</Text>}
    </> : <Alert color="warn" title="Changes unavailable">The preceding version is no longer retained. This saved spec is still available.</Alert>}
    <Text size="sm" c="dimmed">Read-only · {dashboard.spec.panels.length} panels. Historical data is not queried.</Text>
    <Accordion variant="contained">
      <Accordion.Item value="spec"><Accordion.Control>Spec</Accordion.Control><Accordion.Panel><Code block style={{ maxHeight: 320, overflow: "auto" }}>{JSON.stringify(dashboard.spec, null, 2)}</Code></Accordion.Panel></Accordion.Item>
      <Accordion.Item value="data"><Accordion.Control>Data</Accordion.Control><Accordion.Panel><Stack gap="xs">{dashboard.spec.panels.map(panel => <Box key={panel.id}><Text size="sm" fw={500}>{panel.title} · {panel.viz}</Text><Code block>{panel.content ?? panel.sql ?? JSON.stringify(panel.query ?? {}, null, 2)}</Code></Box>)}</Stack></Accordion.Panel></Accordion.Item>
    </Accordion>
  </Stack>;
}
