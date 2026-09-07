import type { Message } from "@ag-ui/client";
import { ActionIcon, Alert, Box, Button, Center, Container, Group, Loader, Paper, Stack, Table, Text, Textarea, Title, Tooltip, Typography } from "@mantine/core";
import { Check, Copy, PaperPlaneTilt, Stop } from "@phosphor-icons/react";
import { lazy, Suspense, useEffect, type ComponentProps, type ReactNode } from "react";
import Markdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { useFanoutApp } from "./app-context";
import { useStickToBottom } from "./chat-scroll";
import { BrandMark } from "./brand";
import { useCopy } from "./copy";
import { exactTimestamp } from "../../format";
import type { MCPAppContent } from "./mcp-app-frame";

const MCPAppFrame = lazy(() => import("./mcp-app-frame"));

const suggestions = [
  "Summarize system health for the last hour",
  "Find the source of elevated errors",
  "Map the current service dependencies",
  "Show the slowest endpoints",
];

export function toolTitle(name: string) {
  return ({ observability_overview: "System health", service_topology: "Service map", service_performance: "Performance", trace_detail: "Trace analysis", search_logs: "Logs" } as Record<string, string>)[name] ?? "System analysis";
}

export function ChatPage() {
  const { agentAvailable, messages, messageTimes, ready, running, activity, error, threadMissing, send, retry, reloadThread, newThread } = useFanoutApp();
  const { scrollRef, contentRef, toBottom } = useStickToBottom<HTMLDivElement, HTMLDivElement>();
  // Sending is a request to see the answer, so it returns a reader who had
  // scrolled back through the thread to the bottom of it.
  const lastSent = messages.filter((message) => message.role === "user").at(-1)?.id;
  useEffect(() => { if (lastSent) toBottom(); }, [lastSent, toBottom]);
  if (!agentAvailable) return <Container size="sm" py={96}><Paper withBorder radius="lg" p={{ base: "xl", sm: 40 }}><Stack gap="md"><Text c="brand" fw={700} size="xs" tt="uppercase" lts="0.12em">Optional capability</Text><Title order={1} fz={28}>Chat is not configured</Title><Text c="dimmed">Add an AI provider key to enable chat. Telemetry ingest, dashboards, traces, logs, and metrics remain available without it.</Text><Button component="a" href="/dashboards" variant="light" mt="sm">Open dashboards</Button></Stack></Paper></Container>;
  const visibleMessages = messages.filter((message) => message.role !== "tool");
  return <Box className="chat-pane">
    <Box className="chat-scroll" ref={scrollRef}>
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
            {visibleMessages.map((message) => <ChatMessage key={message.id} message={message} time={messageTimes[message.id]} send={send} />)}
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

function ChatMessage({ message, time, send }: { message: Message; time?: number; send: (text: string) => Promise<void> }) {
  if (message.role === "activity") {
    const activity = message as Message & { activityType?: string; content: MCPAppContent };
    if (activity.activityType === "mcp-app") return <Paper withBorder radius="lg" style={{ overflow: "hidden" }} aria-label={toolTitle(activity.content.toolName)}><Suspense fallback={<Center mih={180}><Loader size="sm" /></Center>}><MCPAppFrame content={activity.content} onMessage={send} /></Suspense></Paper>;
    return null;
  }
  const content = typeof message.content === "string" ? message.content : JSON.stringify(message.content);
  if (!content && message.role === "assistant") return null;
  const user = message.role === "user";
  const stamp = time ? new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(new Date(time)) : "";
  return <Box className="chat-message" data-role={user ? "user" : "assistant"}>
    {user
      ? <Paper radius="lg" px="md" py="sm" bg="var(--mantine-color-brand-light)" maw="70%" ml="auto" w="fit-content"><Text style={{ whiteSpace: "pre-wrap" }}>{content}</Text></Paper>
      : <Typography className="chat-markdown"><Markdown remarkPlugins={[remarkGfm]} components={markdownComponents}>{content}</Markdown></Typography>}
    <Group className="chat-message-meta" gap={6} justify={user ? "flex-end" : "flex-start"} mt={4}>
      {stamp && time && <Text c="dimmed" size="xs" title={exactTimestamp(new Date(time).toISOString())}>{stamp}</Text>}
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
