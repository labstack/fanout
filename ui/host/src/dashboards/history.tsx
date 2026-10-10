import { Accordion, Alert, Badge, Box, Button, Code, Drawer, Group, Loader, Paper, ScrollArea, Stack, Text, Title, VisuallyHidden } from "@mantine/core";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useRef, useState } from "react";
import { exactTimestamp } from "../../../format";
import { changeChips } from "../dashboard-receipt";
import { EditChips } from "../dashboard-change-chips";
import { dashboardDataPredicate } from "./cache";
import { ApiError, dashboardsKey, getVersion, listVersions, restoreVersion, type DashboardRecord, type VersionInfo, type VersionRecord } from "./api";

type HistoryProps = { id: string; currentVersion: number; opened: boolean; onClose(): void; onRestored(record: DashboardRecord): void };

export function HistoryDrawer({ id, currentVersion, opened, onClose, onRestored }: HistoryProps) {
  const client = useQueryClient();
  const [selected, setSelected] = useState<number>();
  const [success, setSuccess] = useState("");
  const [pending, setPending] = useState(false);
  const [contentMounted, setContentMounted] = useState(opened);
  const feedbackGeneration = useRef(0);
  const isOpen = useRef(opened);
  isOpen.current = opened;
  // Synchronous guard covers clicks in the same render and closing/reopening
  // during a POST. Mutation retries are disabled; a new click is required.
  const submitting = useRef(false);
  const mounted = useRef(false);
  const restoredCallback = useRef(onRestored);
  restoredCallback.current = onRestored;
  useEffect(() => { mounted.current = true; return () => { mounted.current = false; }; }, []);
  const restore = useMutation({
    mutationFn: ({ version }: { version: number; generation: number }) => restoreVersion(id, version),
    retry: false,
    onSuccess: (record, { version, generation }) => {
      void client.cancelQueries({ queryKey: ["dashboard", id], exact: true });
      void client.cancelQueries({ predicate: dashboardDataPredicate(id) });
      void client.cancelQueries({ queryKey: ["exemplars"] });
      void client.cancelQueries({ queryKey: ["drill-trace"] });
      client.setQueryData(["dashboard", id], record);
      // Mark data stale without re-posting the old spec. The page refetches
      // mounted keys after the committed record renders.
      void client.invalidateQueries({ predicate: dashboardDataPredicate(id), refetchType: "none" });
      if (mounted.current) {
        if (isOpen.current && generation === feedbackGeneration.current) setSuccess(`Restored v${version} as v${record.version}`);
        restoredCallback.current(record);
      }
      void client.invalidateQueries({ queryKey: dashboardsKey });
      void client.invalidateQueries({ queryKey: ["dashboard-versions", id] }, { cancelRefetch: false });
      void client.invalidateQueries({ queryKey: ["exemplars"], refetchType: "none" });
      void client.invalidateQueries({ queryKey: ["drill-trace"], refetchType: "none" });
    },
    onError: error => {
      if (error instanceof ApiError && error.code === "dashboard_version_not_found") void client.invalidateQueries({ queryKey: ["dashboard-versions", id] });
    },
    onSettled: () => { submitting.current = false; if (mounted.current) setPending(false); },
  });
  const resetFeedback = () => { feedbackGeneration.current++; restore.reset(); setSuccess(""); };
  useEffect(() => {
    if (opened) { setContentMounted(true); return; }
    feedbackGeneration.current++;
    restore.reset(); setSuccess("");
    void client.cancelQueries({ queryKey: ["dashboard-versions", id] });
    void client.cancelQueries({ queryKey: ["dashboard-version", id] });
  }, [opened, id, client, restore.reset]);
  const observedVersion = useRef(currentVersion);
  useEffect(() => {
    if (observedVersion.current === currentVersion) return;
    observedVersion.current = currentVersion;
    void client.invalidateQueries({ queryKey: ["dashboard-versions", id] }, { cancelRefetch: false });
  }, [currentVersion, id, client]);
  const submit = (version: number) => {
    if (submitting.current) return;
    submitting.current = true;
    setPending(true);
    setSuccess("");
    restore.mutate({ version, generation: feedbackGeneration.current });
  };
  return <Drawer opened={opened} onClose={() => { resetFeedback(); onClose(); }} onExitTransitionEnd={() => setContentMounted(false)} title="Version history" position="right" size="xl">
    {(opened || contentMounted) && <HistoryContent id={id} opened={opened} currentVersion={currentVersion} selected={selected} onSelect={version => { resetFeedback(); setSelected(version); }} pending={pending} error={restore.error} failedVersion={restore.variables?.version} success={success} onRestore={submit} />}
  </Drawer>;
}

function HistoryContent({ id, opened, currentVersion, selected, onSelect, pending, error, failedVersion, success, onRestore }: {
  id: string; opened: boolean; currentVersion: number; selected?: number; onSelect(version: number): void;
  pending: boolean; error: Error | null; failedVersion?: number; success: string; onRestore(version: number): void;
}) {
  const list = useQuery({ queryKey: ["dashboard-versions", id], queryFn: ({ signal }) => listVersions(id, signal), enabled: opened, retry: false });
  const version = selected ?? list.data?.[0]?.version;
  const detail = useQuery({ queryKey: ["dashboard-version", id, version], queryFn: ({ signal }) => getVersion(id, version!, signal), enabled: opened && version !== undefined, retry: false });
  return <Stack gap="md" data-version-history>
    <Text size="sm" c="dimmed">Inspect saved specs and changes. Restoring adds a new version.</Text>
    {success && <Alert color="ok" role="status">{success}</Alert>}
    {error && failedVersion === version && <RestoreError error={error} version={version!} />}
    <Box mih={160}>
      {list.isPending && <Group gap="xs" role="status"><Loader size="sm" /><Text size="sm">Loading history…</Text></Group>}
      {list.error && <Alert color="bad" title="History unavailable">{list.error.message}<Button mt="xs" variant="default" size="xs" onClick={() => void list.refetch()}>Retry history</Button></Alert>}
      {list.data?.length === 0 && <Text c="dimmed">No versions available.</Text>}
      {!!list.data?.length && <ScrollArea h={200} type="auto">
        <Stack gap="xs" pr="sm" role="list" aria-label="Saved versions">
          {list.data.map(item => <Paper key={item.version} role="listitem" withBorder p="xs" radius="sm" style={{ borderColor: version === item.version ? "var(--mantine-color-brand-filled)" : undefined }}>
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
    <VisuallyHidden role="status" aria-live="polite">{version === undefined ? "Select a version." : detail.isFetching ? `Loading version ${version}…` : detail.error ? `Version ${version} unavailable.` : `Version ${version} ready.`}</VisuallyHidden>
    <Box mih={280} aria-busy={detail.isFetching}>
      {version !== undefined && detail.isPending && <Group gap="xs" role="status"><Loader size="sm" /><Text size="sm">Loading version…</Text></Group>}
      {detail.error && <Alert color="bad" title={detail.error instanceof ApiError && detail.error.code === "dashboard_version_not_found" ? "This version is no longer available" : "Version unavailable"}>
        {detail.error.message}<Button mt="xs" variant="default" size="xs" onClick={() => void detail.refetch()}>Retry version</Button>
      </Alert>}
      {detail.data && !detail.error && <VersionDetails record={detail.data} currentVersion={currentVersion} pending={pending} onRestore={() => onRestore(detail.data.dashboard.version)} />}
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

function VersionDetails({ record, currentVersion, pending, onRestore }: { record: VersionRecord; currentVersion: number; pending: boolean; onRestore(): void }) {
  const { dashboard } = record;
  const labels = changeChips(record);
  return <Stack gap="sm" data-history-version={dashboard.version}>
    <Group justify="space-between" align="flex-start">
      <Box miw={0}><Title order={2} fz="lg">{dashboard.name} · v{dashboard.version}</Title><Text size="sm" c="dimmed">{authorLabel(record)} · {record.message || "No edit message"}</Text><VersionTime at={record.created_at} /></Box>
      {dashboard.version === currentVersion ? <Button size="sm" variant="default" disabled>Current version</Button> : <Button size="sm" disabled={pending} loading={pending} onClick={onRestore}>Restore version {dashboard.version}</Button>}
    </Group>
    {dashboard.description && <Text size="sm">{dashboard.description}</Text>}
    {record.changes_available ? <>
      <EditChips labels={labels} />
      {!labels.length && !record.layout_changed && <Text size="sm" c="dimmed">No authored changes.</Text>}
    </> : <Alert color="warn" title="Changes unavailable">The preceding version is no longer retained. This saved spec is still available.</Alert>}
    <Text size="sm" c="dimmed">Read-only · {dashboard.spec.panels.length} panels. Historical data is not queried.</Text>
    <Accordion variant="contained">
      <Accordion.Item value="spec"><Accordion.Control>Spec</Accordion.Control><Accordion.Panel><Code block tabIndex={0} style={{ maxHeight: 320, overflow: "auto" }}>{JSON.stringify(dashboard.spec, null, 2)}</Code></Accordion.Panel></Accordion.Item>
      <Accordion.Item value="data"><Accordion.Control>Data</Accordion.Control><Accordion.Panel><Stack gap="xs">{dashboard.spec.panels.map(panel => <Box key={panel.id}><Text size="sm" fw={500}>{panel.title} · {panel.viz}</Text><Code block tabIndex={0}>{panel.content ?? panel.sql ?? JSON.stringify(panel.query ?? {}, null, 2)}</Code></Box>)}</Stack></Accordion.Panel></Accordion.Item>
    </Accordion>
  </Stack>;
}

function RestoreError({ error, version }: { error: Error; version: number }) {
  const code = error instanceof ApiError ? error.code : undefined;
  const advice = !(error instanceof ApiError) || error.status >= 500 ? "Try again with the restore button." : code === "invalid_spec" ? "This saved spec is invalid. Review the fields below and choose another version." : code === "dashboard_version_not_found" ? "Reload history and choose a retained version." : ["already_current", "dashboard_version_conflict", "conflict"].includes(code ?? "") ? "This dashboard changed or its name conflicts. Reload history and choose another version." : "Check your access and reload history.";
  return <Alert color="bad" title={`Restore of version ${version} failed`}>
    <Text size="sm">{error.message}</Text>
    {error instanceof ApiError && error.problems.length > 0 && <ul>{error.problems.map((problem, index) => <li key={index}>{problem.path}: {problem.message}{problem.hint ? ` (${problem.hint})` : ""}</li>)}</ul>}
    <Text size="sm" mt="xs">{advice}</Text>
  </Alert>;
}
