import { ActionIcon, Alert, Box, Button, Center, Divider, Drawer, Group, Loader, Menu, Modal, ScrollArea, Stack, Text, TextInput, UnstyledButton } from "@mantine/core";
import { useDebouncedValue, useMediaQuery } from "@mantine/hooks";
import { DotsThree, MagnifyingGlass, PencilSimple, Plus, Trash } from "@phosphor-icons/react";
import { useInfiniteQuery, useQueryClient } from "@tanstack/react-query";
import { useEffect, useMemo, useRef, useState } from "react";
import { authorizedFetch } from "./auth";

export const threadHistoryQueryKey = ["agent-threads"] as const;

type ThreadSummary = {
  threadId: string;
  title: string;
  updatedAt: string;
};

type ThreadPage = {
  threads: ThreadSummary[];
  nextCursor: string;
};

type ChatHistoryDrawerProps = {
  opened: boolean;
  activeThreadID?: string;
  onClose: () => void;
  onNewChat: () => void;
  onSelect: (threadID: string) => void;
  onDeleted: (threadID: string) => void;
};

async function fetchThreads(query: string, cursor: string): Promise<ThreadPage> {
  const params = new URLSearchParams({ limit: "30" });
  if (query) params.set("q", query);
  if (cursor) params.set("cursor", cursor);
  const response = await authorizedFetch(`/api/agent/threads?${params}`);
  if (!response.ok) throw new Error(`Unable to load conversations (${response.status})`);
  return response.json();
}

export default function ChatHistoryDrawer({ opened, activeThreadID, onClose, onNewChat, onSelect, onDeleted }: ChatHistoryDrawerProps) {
  const mobile = useMediaQuery("(max-width: 48em)");
  const queryClient = useQueryClient();
  const [search, setSearch] = useState("");
  const [query] = useDebouncedValue(search.trim(), 250);
  const [renaming, setRenaming] = useState<ThreadSummary | null>(null);
  const [deleting, setDeleting] = useState<ThreadSummary | null>(null);
  const [renameTitle, setRenameTitle] = useState("");
  const [mutationError, setMutationError] = useState("");
  const [busy, setBusy] = useState(false);
  const searchRef = useRef<HTMLInputElement>(null);
  const history = useInfiniteQuery({
    queryKey: [...threadHistoryQueryKey, query],
    queryFn: ({ pageParam }) => fetchThreads(query, pageParam),
    initialPageParam: "",
    getNextPageParam: (last) => last.nextCursor || undefined,
    enabled: opened,
  });
  const threads = useMemo(() => history.data?.pages.flatMap((page) => page.threads) ?? [], [history.data]);
  const groups = useMemo(() => groupThreads(threads), [threads]);

  useEffect(() => {
    if (opened) requestAnimationFrame(() => searchRef.current?.focus());
  }, [opened]);

  function beginRename(thread: ThreadSummary) {
    setMutationError("");
    setRenameTitle(thread.title);
    setRenaming(thread);
  }

  async function renameThread() {
    if (!renaming || !renameTitle.trim()) return;
    setBusy(true);
    setMutationError("");
    try {
      const response = await authorizedFetch(`/api/agent/threads/${encodeURIComponent(renaming.threadId)}`, {
        method: "PATCH",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ title: renameTitle.trim() }),
      });
      if (!response.ok) throw new Error(`Unable to rename conversation (${response.status})`);
      await queryClient.invalidateQueries({ queryKey: threadHistoryQueryKey });
      setRenaming(null);
    } catch (cause) {
      setMutationError(cause instanceof Error ? cause.message : "Unable to rename this conversation.");
    } finally {
      setBusy(false);
    }
  }

  async function deleteThread() {
    if (!deleting) return;
    setBusy(true);
    setMutationError("");
    try {
      const response = await authorizedFetch(`/api/agent/threads/${encodeURIComponent(deleting.threadId)}`, { method: "DELETE" });
      if (!response.ok) throw new Error(`Unable to delete conversation (${response.status})`);
      const deletedID = deleting.threadId;
      setDeleting(null);
      await queryClient.invalidateQueries({ queryKey: threadHistoryQueryKey });
      onDeleted(deletedID);
    } catch (cause) {
      setMutationError(cause instanceof Error ? cause.message : "Unable to delete this conversation.");
    } finally {
      setBusy(false);
    }
  }

  return <>
    <Drawer
    opened={opened}
    onClose={onClose}
    position="left"
    size={mobile ? "100%" : 372}
    title={<Text fw={700}>Investigations</Text>}
    padding="md"
    overlayProps={{ backgroundOpacity: 0.24, blur: 1 }}
  >
    <Stack gap="sm" h="calc(100dvh - 86px)">
      <Button leftSection={<Plus size={17} weight="bold" />} onClick={onNewChat}>New investigation</Button>
      <TextInput
        ref={searchRef}
        value={search}
        onChange={(event) => setSearch(event.currentTarget.value)}
        leftSection={<MagnifyingGlass size={16} />}
        placeholder="Search investigations"
        aria-label="Search investigations"
      />
      <Divider />
      <ScrollArea type="auto" offsetScrollbars flex={1}>
        {history.isLoading && <Center py="xl"><Loader size="sm" /></Center>}
        {history.isError && <Alert color="red" title="History unavailable">Your conversations could not be loaded.</Alert>}
        {!history.isLoading && !history.isError && threads.length === 0 && <Box py="xl" px="sm" ta="center">
          <Text fw={600}>{query ? "No matching investigations" : "No investigations yet"}</Text>
          <Text c="dimmed" size="sm" mt={4}>{query ? "Try words from the opening question." : "Your completed investigations will appear here."}</Text>
        </Box>}
        <Stack gap="lg" pb="md">
          {groups.map((group) => <Stack key={group.label} gap={4}>
            <Text c="dimmed" size="xs" fw={700} tt="uppercase" lts="0.08em" px="sm">{group.label}</Text>
            {group.threads.map((thread) => {
              const active = thread.threadId === activeThreadID;
              return <Box key={thread.threadId} data-active={active || undefined} className="chat-history-item">
                <Group gap={2} wrap="nowrap">
                  <UnstyledButton
                    onClick={() => onSelect(thread.threadId)}
                    aria-current={active ? "page" : undefined}
                    p="sm"
                    flex={1}
                    style={{ minWidth: 0 }}
                  >
                    <Group justify="space-between" gap="sm" wrap="nowrap">
                      <Text size="sm" fw={active ? 650 : 500} truncate>{thread.title}</Text>
                      <Text c="dimmed" size="xs" style={{ flexShrink: 0 }}>{threadTime(thread.updatedAt)}</Text>
                    </Group>
                  </UnstyledButton>
                  <Menu position="bottom-end" withinPortal>
                    <Menu.Target>
                      <ActionIcon className="chat-history-actions" variant="subtle" color="gray" size="sm" mr={6} aria-label={`Actions for ${thread.title}`}>
                        <DotsThree size={18} weight="bold" />
                      </ActionIcon>
                    </Menu.Target>
                    <Menu.Dropdown>
                      <Menu.Item leftSection={<PencilSimple size={15} />} onClick={() => beginRename(thread)}>Rename</Menu.Item>
                      <Menu.Item color="red" leftSection={<Trash size={15} />} onClick={() => { setMutationError(""); setDeleting(thread); }}>Delete</Menu.Item>
                    </Menu.Dropdown>
                  </Menu>
                </Group>
              </Box>;
            })}
          </Stack>)}
          {history.hasNextPage && <Button variant="subtle" color="gray" loading={history.isFetchingNextPage} onClick={() => void history.fetchNextPage()}>Load older</Button>}
        </Stack>
      </ScrollArea>
    </Stack>
    </Drawer>
    <Modal opened={renaming !== null} onClose={() => !busy && setRenaming(null)} title="Rename investigation" centered>
      <form onSubmit={(event) => { event.preventDefault(); void renameThread(); }}>
        <Stack>
          <TextInput label="Name" value={renameTitle} onChange={(event) => setRenameTitle(event.currentTarget.value)} maxLength={120} autoFocus />
          {mutationError && <Alert color="red">{mutationError}</Alert>}
          <Group justify="flex-end">
            <Button variant="default" onClick={() => setRenaming(null)} disabled={busy}>Cancel</Button>
            <Button type="submit" loading={busy} disabled={!renameTitle.trim()}>Save</Button>
          </Group>
        </Stack>
      </form>
    </Modal>
    <Modal opened={deleting !== null} onClose={() => !busy && setDeleting(null)} title="Delete investigation?" centered>
      <Stack>
        <Text size="sm">This permanently removes <Text span fw={650}>{deleting?.title}</Text> and its saved conversation.</Text>
        {mutationError && <Alert color="red">{mutationError}</Alert>}
        <Group justify="flex-end">
          <Button variant="default" onClick={() => setDeleting(null)} disabled={busy}>Cancel</Button>
          <Button color="red" loading={busy} onClick={() => void deleteThread()}>Delete</Button>
        </Group>
      </Stack>
    </Modal>
  </>;
}

function parseSQLiteTime(value: string): Date {
  if (value.includes("T")) return new Date(value);
  return new Date(`${value.replace(" ", "T")}Z`);
}

function dayStart(value: Date): number {
  return new Date(value.getFullYear(), value.getMonth(), value.getDate()).getTime();
}

function groupThreads(threads: ThreadSummary[]): Array<{ label: string; threads: ThreadSummary[] }> {
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

function threadTime(value: string): string {
  const date = parseSQLiteTime(value);
  const today = dayStart(new Date());
  const age = Math.floor((today - dayStart(date)) / (24 * 60 * 60 * 1000));
  if (age <= 1) return new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(date);
  return new Intl.DateTimeFormat(undefined, { month: "short", day: "numeric" }).format(date);
}
