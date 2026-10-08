import type { Message } from "@ag-ui/client";
import { ActionIcon, Alert, Box, Button, Center, Container, Group, Loader, Paper, Stack, Table, Text, Textarea, Title, Tooltip, Typography } from "@mantine/core";
import { Check, Copy, PaperPlaneTilt, Stop, CaretDown, CaretRight } from "@phosphor-icons/react";
import { lazy, Suspense, useEffect, useState, type ComponentProps, type ReactNode } from "react";
import Markdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { useFanoutApp } from "./app-context";
import { useStickToBottom } from "./chat-scroll";
import { BrandMark } from "./brand";
import { useCopy } from "./copy";
import { exactTimestamp } from "../../format";
import { mcpAppContent, type MCPAppContent } from "./mcp-app-content";
import { Link } from "@tanstack/react-router";
import { fragmentTitle } from "../../panels/fragment";
import { dashboardToolResult } from "./dashboard-tool-result";

const MCPAppFrame = lazy(() => import("./mcp-app-frame"));

type AppView = { content: MCPAppContent; expanded: boolean };
// Sort object keys so reloads and equivalent JSON field orders dedupe alike.
function canonical(value: unknown): string {
  if (Array.isArray(value)) return `[${value.map(canonical).join(",")}]`;
  if (value && typeof value === "object") return `{${Object.entries(value).sort(([a], [b]) => a.localeCompare(b)).map(([key, item]) => `${JSON.stringify(key)}:${canonical(item)}`).join(",")}}`;
  return JSON.stringify(value) ?? "null";
}
function chatAppViews(messages: Message[]) {
  const views = new Map<string, AppView>(), duplicates = new Set<string>();
  let turn: Array<{ id: string; content: MCPAppContent }> = [];
  const finish = () => {
    const seen = new Set<string>();
    for (const item of [...turn].reverse()) {
      const fragment = item.content.tool_result;
      const mapFrame = fragment.dashboard.panels.length === 1 && fragment.dashboard.panels[0].viz === "service_map" && fragment.results[0].frame && !fragment.results[0].frame.truncated ? fragment.results[0].frame : undefined;
      const key = canonical({ dashboard: { ...fragment.dashboard, name: "", panels: fragment.dashboard.panels.map(panel => ({ ...panel, id: "", title: "", ...(panel.query ? { query: { ...panel.query, where: [...(panel.query.where ?? [])].sort(), ...(mapFrame ? { limit: undefined } : {}) } } : {}) })) }, vars: fragment.vars ?? {}, ...(mapFrame ? { map: mapFrame } : {}), ...(fragment.dashboard.time.from ? {} : { windows: fragment.results.map(result => [result.from_ms ?? 0, result.to_ms ?? 0]) }) });
      if (seen.has(key)) duplicates.add(item.id);
      else { views.set(item.id, { content: item.content, expanded: seen.size === 0 }); seen.add(key); }
    }
    turn = [];
  };
  for (const message of messages) {
    if (message.role === "user") finish();
    if (message.role === "activity" && message.activityType === "mcp-app") {
      const content = mcpAppContent(message.content);
      if (content) turn.push({ id: message.id, content });
    }
  }
  finish(); return { views, duplicates };
}
function ChatAppView({ view }: { view: AppView }) {
  const [expanded, setExpanded] = useState(view.expanded);
  useEffect(() => setExpanded(view.expanded), [view.expanded]);
  const fragment = view.content.tool_result;
  const title = fragmentTitle(fragment);
  const viz = [...new Set(fragment.dashboard.panels.map(panel => panel.viz === "timeseries" ? "time series" : panel.viz.replaceAll("_", " ")))].join(", ");
  const rows = fragment.results.reduce((count, result) => count + (result.frame?.rows ?? 0), 0);
  const status = fragment.results.some(result => result.frame) ? `${rows} rows` : fragment.results.map(result => result.status).join(", ");
  return <Paper data-chat-app data-chat-anchor withBorder radius="lg" style={{ overflow: "hidden" }}>
    <Button fullWidth variant="subtle" aria-expanded={expanded} onClick={() => setExpanded(value => !value)} leftSection={expanded ? <CaretDown /> : <CaretRight />} styles={{ label: { display: "block", overflow: "hidden", textOverflow: "ellipsis", whiteSpace: "nowrap" } }}>{title} · {viz} · {status}</Button>
    {expanded && <Suspense fallback={<Center mih={180}><Loader size="sm" /></Center>}><MCPAppFrame content={view.content} /></Suspense>}
  </Paper>;
}

const suggestions = [
  "Summarize system health for the last hour",
  "Find the source of elevated errors",
  "Map the current service dependencies",
  "Show the slowest endpoints",
];

export function ChatPage() {
  const { agentAvailable, messages, messageTimes, ready, running, activity, error, threadMissing, send, retry, reloadThread, newThread } = useFanoutApp();
  const { scrollRef, contentRef, toBottom } = useStickToBottom<HTMLDivElement, HTMLDivElement>();
  // Sending is a request to see the answer, so it returns a reader who had
  // scrolled back through the thread to the bottom of it.
  const lastSent = messages.filter((message) => message.role === "user").at(-1)?.id;
  useEffect(() => { if (lastSent) toBottom(); }, [lastSent, toBottom]);
  if (!agentAvailable) return <Container size="sm" py={96}><Paper withBorder radius="lg" p={{ base: "xl", sm: 40 }}><Stack gap="md"><Text c="brand" fw={700} size="xs" tt="uppercase" lts="0.12em">Optional capability</Text><Title order={1} fz={28}>Chat is not configured</Title><Text c="dimmed">Add an AI provider key to enable chat. Telemetry ingest, dashboards, traces, logs, and metrics remain available without it.</Text><Button component="a" href="/dashboards" variant="light" mt="sm">Open dashboards</Button></Stack></Paper></Container>;
  const dashboardResults = new Map(messages.flatMap(message => {
    if (message.role !== "tool" || message.error) return [];
    const saved = dashboardToolResult(message.toolCallId, message.content, messages);
    return saved ? [[message.id, saved] as const] : [];
  }));
  const appViews = chatAppViews(messages);
  const visibleMessages = messages.filter((message) => !appViews.duplicates.has(message.id) && (message.role !== "tool" || Boolean(message.error) || dashboardResults.has(message.id)));
  return <Box className="chat-pane">
    {/* A scroll region has to be reachable without a mouse. Chrome makes a
        scroller focusable only when it holds no focusable children, and this
        one holds copy buttons and links, so it says so itself. */}
    <Box className="chat-scroll" ref={scrollRef} tabIndex={0} role="log" aria-label="Conversation">
      <Container size={880} px={{ base: "md", sm: "xl" }} py="lg" ref={contentRef}>
        {threadMissing && <Alert color="warn" radius="lg" title="This chat no longer exists"><Group justify="space-between"><Text size="sm">It was deleted, or the link is wrong.</Text><Button size="compact-sm" variant="light" onClick={newThread}>New chat</Button></Group></Alert>}
        {/* A thread whose load failed never becomes ready, so showing the
            loader here would spin forever: state the failure instead. Retry
            here asks the server for the thread again; there is no run to
            retry, because the conversation never arrived. */}
        {!threadMissing && !ready && error && <RunError message={error} onRetry={reloadThread} onNewThread={newThread} />}
        {!threadMissing && !ready && !error && <Center mih="40vh"><Loader size="sm" /><Text c="dimmed" size="sm" ml="sm">Loading chat</Text></Center>}
        {!threadMissing && ready && <>
          {visibleMessages.length === 0 && <Welcome onSelect={send} />}
          <Stack gap="lg" aria-live="polite">
            {visibleMessages.map((message) => {
              const saved = dashboardResults.get(message.id);
              return saved ? <Paper key={message.id} withBorder radius="md" p="sm" data-dashboard-result={saved.id}>
                <Group justify="space-between" gap="sm">
                  <Box miw={0}><Text size="xs" c="dimmed">{saved.label}</Text><Text size="sm" fw={500} style={{ overflowWrap: "anywhere" }}>{saved.name}</Text></Box>
                  <Button renderRoot={(props) => <Link {...props} to="/dashboards/$dashboardId" params={{ dashboardId: saved.id }} search={{}} />} variant="subtle" size="compact-sm">Open dashboard</Button>
                </Group>
              </Paper> : <ChatMessage key={message.id} message={message} time={messageTimes[message.id]} appView={appViews.views.get(message.id)} />;
            })}
            {running && <Group gap="xs"><Loader type="dots" size="sm" /><Text c="dimmed" size="sm">{activity || "Analyzing your system"}</Text></Group>}
            {error && <RunError message={error} onRetry={retry} />}
          </Stack>
        </>}
      </Container>
    </Box>
    {!threadMissing && <Composer />}
  </Box>;
}

function RunError({ message, onRetry, onNewThread }: { message: string; onRetry: () => void; onNewThread?: () => void }) {
  return <Alert color="bad" radius="lg" title="Something went wrong">
    <Group justify="space-between" gap="sm" wrap="nowrap">
      <Text size="sm">{message}</Text>
      <Group gap="xs" wrap="nowrap">
        {onNewThread && <Button size="compact-sm" variant="subtle" color="bad" onClick={onNewThread}>New chat</Button>}
        <Button size="compact-sm" variant="light" color="bad" onClick={onRetry}>Retry</Button>
      </Group>
    </Group>
  </Alert>;
}

function Composer() {
  const { input, setInput, inputRef, submit, send, stop, ready, running } = useFanoutApp();
  return <Box className="chat-composer">
    <Container size={880} px={{ base: "md", sm: "xl" }}>
      <Paper component="form" onSubmit={submit} className="chat-composer-field" withBorder radius={24} py={6} pl="lg" pr={6}>
        <Group align="flex-end" gap="xs" wrap="nowrap">
          <Textarea ref={inputRef} aria-label="Message Fanout" value={input} onChange={(event) => setInput(event.currentTarget.value)} onKeyDown={(event) => { if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); void send(input); } }} placeholder={running ? "Fanout is working…" : "Ask about health, errors, or latency…"} disabled={!ready} autosize minRows={1} maxRows={8} variant="unstyled" flex={1} />
          {running
            ? <Tooltip label="Stop"><ActionIcon type="button" variant="default" size={40} radius="xl" aria-label="Stop" onClick={stop}><Stop size={16} weight="fill" /></ActionIcon></Tooltip>
            : <ActionIcon type="submit" variant="filled" size={40} radius="xl" disabled={!input.trim() || !ready} aria-label="Send message"><PaperPlaneTilt size={17} weight="fill" /></ActionIcon>}
        </Group>
      </Paper>
      <Text c="dimmed" size="xs" ta="center" mt={6}>Enter to send, Shift+Enter for a new line</Text>
    </Container>
  </Box>;
}

function Welcome({ onSelect }: { onSelect: (text: string) => Promise<void> }) {
  return <Stack align="center" gap="md" pt={{ base: 32, sm: 88 }} pb="xl" ta="center">
    <BrandMark size="large" />
    <Title order={1} fz={24} fw={500} lh={1.25} maw={560}>What do you want to know about your system?</Title>
    <Group justify="center" gap="xs" mt="xs" maw={720}>
      {suggestions.map((suggestion) => <Button key={suggestion} variant="default" size="sm" radius="xl" onClick={() => void onSelect(suggestion)}>{suggestion}</Button>)}
    </Group>
  </Stack>;
}

function ChatMessage({ message, time, appView }: { message: Message; time?: number; appView?: AppView }) {
  if (message.role === "tool" && message.error) return <Alert color="bad" data-chat-anchor>{String(message.content || message.error)}</Alert>;
  if (message.role === "activity") {
    if (message.activityType === "mcp-app") return appView ? <ChatAppView view={appView} /> : <Alert color="bad" data-chat-anchor>This view could not be loaded. Please try again.</Alert>;
    return null;
  }
  const content = typeof message.content === "string" ? message.content : JSON.stringify(message.content);
  if (!content && message.role === "assistant") return null;
  const user = message.role === "user";
  const stamp = time ? new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(new Date(time)) : "";
  return <Box className="chat-message" data-chat-anchor data-role={user ? "user" : "assistant"}>
    {user
      ? <Paper radius="lg" px="md" py="sm" bg="var(--mantine-color-brand-light)" maw="70%" ml="auto" w="fit-content"><Text style={{ whiteSpace: "pre-wrap" }}>{content}</Text></Paper>
      : <Typography className="chat-markdown"><Markdown remarkPlugins={[remarkGfm]} components={markdownComponents}>{content}</Markdown></Typography>}
    <Group className="chat-message-meta" gap={6} justify={user ? "flex-end" : "flex-start"} mt={4}>
      {stamp && time && <Text c="dimmed" size="xs" title={exactTimestamp(time)}>{stamp}</Text>}
      {!user && <CopyButton text={content} label="Copy message" />}
    </Group>
  </Box>;
}

function CopyButton({ text, label }: { text: string; label: string }) {
  const { state, copy } = useCopy();
  return <Tooltip label={state === "failed" ? "Could not copy" : state === "copied" ? "Copied" : label}>
    <ActionIcon variant="subtle" color="gray" size="sm" aria-label={label} data-state={state === "idle" ? undefined : state} onClick={() => copy(text)}>
      {state === "copied" ? <Check size={13} weight="bold" /> : <Copy size={13} />}
    </ActionIcon>
  </Tooltip>;
}

/* Every block the model can emit gets product chrome: tables scroll inside
   their own container instead of widening the column, code blocks carry a
   copy button, links open in a new tab. */
const markdownComponents: Components = {
  table: ({ children }) => <Box className="chat-table"><Table striped highlightOnHover withTableBorder verticalSpacing="xs" fz="sm">{children}</Table></Box>,
  thead: ({ children }) => <Table.Thead>{children}</Table.Thead>,
  tbody: ({ children }) => <Table.Tbody>{children}</Table.Tbody>,
  tr: ({ children }) => <Table.Tr>{children}</Table.Tr>,
  th: ({ children }) => <Table.Th>{children}</Table.Th>,
  td: ({ children }) => <Table.Td>{children}</Table.Td>,
  pre: ({ children }) => <CodeBlock>{children}</CodeBlock>,
  a: ({ href, children }) => <a href={href} target="_blank" rel="noopener noreferrer">{children}</a>,
};

function CodeBlock({ children }: { children: ReactNode }) {
  const text = codeText(children);
  return <Box className="chat-code" pos="relative">
    <Box className="chat-code-actions" pos="absolute" top={6} right={6}><CopyButton text={text} label="Copy code" /></Box>
    <pre>{children}</pre>
  </Box>;
}

function codeText(node: ReactNode): string {
  if (typeof node === "string") return node;
  if (Array.isArray(node)) return node.map(codeText).join("");
  if (node && typeof node === "object" && "props" in node) return codeText((node as { props: ComponentProps<"code"> }).props.children);
  return "";
}
