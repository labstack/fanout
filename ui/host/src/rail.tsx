import { ActionIcon, Alert, Badge, Box, Button, Center, Group, Kbd, Loader, Menu, Modal, ScrollArea, Stack, Text, TextInput, UnstyledButton } from "@mantine/core";
import { useDebouncedValue } from "@mantine/hooks";
import { DotsThree, MagnifyingGlass, PencilSimple, Plus, Sparkle, Trash } from "@phosphor-icons/react";
import { useInfiniteQuery, useQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useImperativeHandle, useMemo, useRef, useState, type Ref } from "react";
import { dashboardsQueryKey, getJSON, threadHistoryQueryKey, type DashboardSummary } from "./api";
import { authorizedFetch } from "./auth";

export type RailHandle = { focusSearch(): void };

export type RailProps = {
  agentAvailable: boolean;
  activeThreadID?: string;
  activeDashboardID?: string;
  /** The mobile drawer mounts a second rail; it focuses search on open. */
  autoFocusSearch?: boolean;
  onNewChat: () => void;
  onSelectThread: (threadID: string) => void;
  onDeletedThread: (threadID: string) => void;
  onSelectDashboard: (dashboardID: string) => void;
  onCreateDashboard: () => void;
  ref?: Ref<RailHandle>;
};

type ThreadSummary = { threadId: string; title: string; updatedAt: string };
type ThreadPage = { threads: ThreadSummary[]; nextCursor: string };

async function fetchThreads(query: string, cursor: string): Promise<ThreadPage> {
  const params = new URLSearchParams({ limit: "30" });
  if (query) params.set("q", query);
  if (cursor) params.set("cursor", cursor);
  const response = await authorizedFetch(`/api/agent/threads?${params}`);
  if (!response.ok) throw new Error(`Unable to load chats (${response.status})`);
  return response.json();
}

export default function Rail({ agentAvailable, activeThreadID, activeDashboardID, autoFocusSearch = false, onNewChat, onSelectThread, onDeletedThread, onSelectDashboard, onCreateDashboard, ref }: RailProps) {
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [query] = useDebouncedValue(search.trim(), 250);
  const [renaming, setRenaming] = useState<ThreadSummary | null>(null);
  const [deleting, setDeleting] = useState<ThreadSummary | null>(null);
  const [renameTitle, setRenameTitle] = useState("");
  const [mutationError, setMutationError] = useState("");
  const [busy, setBusy] = useState(false);
  const searchRef = useRef<HTMLInputElement>(null);
  useImperativeHandle(ref, () => ({ focusSearch: () => searchRef.current?.focus() }), []);
  useEffect(() => { if (autoFocusSearch) requestAnimationFrame(() => searchRef.current?.focus()); }, [autoFocusSearch]);

  const history = useInfiniteQuery({
    queryKey: [...threadHistoryQueryKey, query],
    queryFn: ({ pageParam }) => fetchThreads(query, pageParam),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor || undefined,
    enabled: agentAvailable,
  });
  const dashboards = useQuery({ queryKey: dashboardsQueryKey, queryFn: () => getJSON<{ dashboards: DashboardSummary[] }>("/api/dashboards"), refetchInterval: 30_000 });
  const threads = useMemo(() => history.data?.pages.flatMap((page) => page.threads) ?? [], [history.data]);
  const groups = useMemo(() => groupThreads(threads), [threads]);
  const visibleDashboards = useMemo(() => {
    const items = dashboards.data?.dashboards ?? [];
    const needle = query.toLowerCase();
    return needle ? items.filter((item) => item.name.toLowerCase().includes(needle)) : items;
  }, [dashboards.data, query]);

  function beginRename(thread: ThreadSummary) { setMutationError(""); setRenameTitle(thread.title); setRenaming(thread); }

  async function renameThread() {
    if (!renaming || !renameTitle.trim()) return;
    setBusy(true); setMutationError("");
    try {
      const response = await authorizedFetch(`/api/agent/threads/${encodeURIComponent(renaming.threadId)}`, { method: "PATCH", headers: { "Content-Type": "application/json" }, body: JSON.stringify({ title: renameTitle.trim() }) });
      if (!response.ok) throw new Error(`Unable to rename chat (${response.status})`);
      await queryClient.invalidateQueries({ queryKey: threadHistoryQueryKey });
      setRenaming(null);
    } catch (cause) {
      setMutationError(cause instanceof Error ? cause.message : "Unable to rename this chat.");
    } finally { setBusy(false); }
  }

  async function deleteThread() {
    if (!deleting) return;
    setBusy(true); setMutationError("");
    try {
      const response = await authorizedFetch(`/api/agent/threads/${encodeURIComponent(deleting.threadId)}`, { method: "DELETE" });
      if (!response.ok) throw new Error(`Unable to delete chat (${response.status})`);
      const deletedID = deleting.threadId;
      setDeleting(null);
      await queryClient.invalidateQueries({ queryKey: threadHistoryQueryKey });
      onDeletedThread(deletedID);
    } catch (cause) {
      setMutationError(cause instanceof Error ? cause.message : "Unable to delete this chat.");
    } finally { setBusy(false); }
  }

  return <Stack gap="sm" h="100%" component="nav" aria-label="Navigation">
    {agentAvailable && <Button leftSection={<Plus size={16} weight="bold" />} onClick={onNewChat}>New chat</Button>}
    <ScrollArea type="auto" offsetScrollbars flex={1} mx={-4} px={4}>
      <Stack gap="lg" pb="sm">
        {agentAvailable && <Stack gap={4}>
          <SectionLabel>Chats</SectionLabel>
          {history.isLoading && <Center py="md"><Loader size="xs" /></Center>}
          {history.isError && <Alert color="bad" radius="md" p="xs">Chats could not be loaded.</Alert>}
          {!history.isLoading && !history.isError && threads.length === 0 && <Text c="dimmed" size="sm" px="sm" py="xs">{query ? "No matching chats" : "No chats yet"}</Text>}
          {groups.map((group) => <Stack key={group.label} gap={2}>
            {groups.length > 1 && <Text c="dimmed" size="xs" px="sm" pt={4}>{group.label}</Text>}
            {group.threads.map((thread) => <ThreadRow key={thread.threadId} thread={thread} active={thread.threadId === activeThreadID} onSelect={() => onSelectThread(thread.threadId)} onRename={() => beginRename(thread)} onDelete={() => { setMutationError(""); setDeleting(thread); }} />)}
          </Stack>)}
          {history.hasNextPage && <Button variant="subtle" color="gray" size="compact-sm" loading={history.isFetchingNextPage} onClick={() => void history.fetchNextPage()}>See all</Button>}
        </Stack>}
        <Stack gap={4}>
          <SectionLabel>Dashboards</SectionLabel>
          {dashboards.isLoading && <Center py="md"><Loader size="xs" /></Center>}
          {dashboards.isError && <Alert color="bad" radius="md" p="xs">Dashboards could not be loaded.</Alert>}
          {visibleDashboards.map((dashboard) => {
            const active = dashboard.id === activeDashboardID;
            return <UnstyledButton key={dashboard.id} className="rail-row" data-active={active || undefined} aria-current={active ? "page" : undefined} p="sm" onClick={() => onSelectDashboard(dashboard.id)}>
              <Group justify="space-between" gap="sm" wrap="nowrap">
                <Text size="sm" fw={active ? 600 : 500} truncate>{dashboard.name}</Text>
                {dashboard.is_default && <Badge size="xs" variant="light" color="gray">Default</Badge>}
              </Group>
            </UnstyledButton>;
          })}
          {agentAvailable && <Button variant="subtle" color="gray" size="compact-sm" justify="flex-start" leftSection={<Sparkle size={14} weight="fill" />} onClick={onCreateDashboard}>Create with AI</Button>}
        </Stack>
      </Stack>
    </ScrollArea>
    <TextInput ref={searchRef} value={search} onChange={(event) => setSearch(event.currentTarget.value)} leftSection={<MagnifyingGlass size={15} />} rightSection={<Kbd size="xs">⌘K</Kbd>} rightSectionWidth={44} placeholder="Search" aria-label="Search chats and dashboards" size="sm" />
    <Modal opened={renaming !== null} onClose={() => !busy && setRenaming(null)} title="Rename chat" centered>
      <form onSubmit={(event) => { event.preventDefault(); void renameThread(); }}>
        <Stack>
          <TextInput label="Name" value={renameTitle} onChange={(event) => setRenameTitle(event.currentTarget.value)} maxLength={120} autoFocus />
          {mutationError && <Alert color="bad">{mutationError}</Alert>}
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setRenaming(null)} disabled={busy}>Cancel</Button>
            <Button type="submit" loading={busy} disabled={!renameTitle.trim()}>Save</Button>
          </Group>
        </Stack>
      </form>
    </Modal>
    <Modal opened={deleting !== null} onClose={() => !busy && setDeleting(null)} title="Delete chat?" centered>
      <Stack>
        <Text size="sm">This permanently removes <Text span fw={650}>{deleting?.title}</Text> and its saved messages.</Text>
        {mutationError && <Alert color="bad">{mutationError}</Alert>}
        <Group justify="flex-end">
          <Button variant="default" onClick={() => setDeleting(null)} disabled={busy}>Cancel</Button>
          <Button color="bad" loading={busy} onClick={() => void deleteThread()}>Delete</Button>
        </Group>
      </Stack>
    </Modal>
  </Stack>;
}

function SectionLabel({ children }: { children: string }) {
  return <Text c="dimmed" size="xs" fw={700} tt="uppercase" lts="0.08em" px="sm">{children}</Text>;
}

function ThreadRow({ thread, active, onSelect, onRename, onDelete }: { thread: ThreadSummary; active: boolean; onSelect: () => void; onRename: () => void; onDelete: () => void }) {
  return <Box data-active={active || undefined} className="rail-row">
    <Group gap={2} wrap="nowrap">
      {/* Two lines rather than one: a fixed-width time beside the title left it
          about a hundred pixels of a 219px row, and the cut point moved with
          the length of the time. On its own line the title gets the row. */}
      <UnstyledButton onClick={onSelect} aria-current={active ? "page" : undefined} p="sm" flex={1} style={{ minWidth: 0 }}>
        <Stack gap={0}>
          <Text size="sm" fw={active ? 600 : 500} truncate>{thread.title}</Text>
          <Text c="dimmed" size="xs">{threadTime(thread.updatedAt)}</Text>
        </Stack>
      </UnstyledButton>
      <Menu position="bottom-end" withinPortal>
        <Menu.Target>
          <ActionIcon className="rail-row-actions" variant="subtle" color="gray" size="sm" mr={4} aria-label={`Actions for ${thread.title}`}><DotsThree size={18} weight="bold" /></ActionIcon>
        </Menu.Target>
        <Menu.Dropdown>
          <Menu.Item leftSection={<PencilSimple size={15} />} onClick={onRename}>Rename</Menu.Item>
          <Menu.Item color="bad" leftSection={<Trash size={15} />} onClick={onDelete}>Delete</Menu.Item>
        </Menu.Dropdown>
      </Menu>
    </Group>
  </Box>;
}

function parseSQLiteTime(value: string): Date {
  if (value.includes("T")) return new Date(value);
  return new Date(`${value.replace(" ", "T")}Z`);
}

function dayStart(value: Date): number {
  return new Date(value.getFullYear(), value.getMonth(), value.getDate()).getTime();
}

export function groupThreads(threads: ThreadSummary[]): Array<{ label: string; threads: ThreadSummary[] }> {
  const today = dayStart(new Date());
  const day = 24 * 60 * 60 * 1000;
  const groups = new Map<string, ThreadSummary[]>();
  for (const thread of threads) {
    const age = Math.floor((today - dayStart(parseSQLiteTime(thread.updatedAt))) / day);
    const label = age <= 0 ? "Today" : age === 1 ? "Yesterday" : age <= 7 ? "Previous 7 days" : "Older";
    const group = groups.get(label) ?? [];
    group.push(thread);
    groups.set(label, group);
  }
  return [...groups].map(([label, items]) => ({ label, threads: items }));
}

export function threadTime(value: string): string {
  const date = parseSQLiteTime(value);
  const today = dayStart(new Date());
  const age = Math.floor((today - dayStart(date)) / (24 * 60 * 60 * 1000));
  if (age <= 1) return new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(date);
  return new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" }).format(date);
}
