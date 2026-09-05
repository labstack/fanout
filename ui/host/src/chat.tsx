import type { Message } from "@ag-ui/client";
import { ActionIcon, Alert, Avatar, Box, Button, Center, Container, Group, Loader, Paper, SimpleGrid, Stack, Text, Textarea, Title, Typography, UnstyledButton } from "@mantine/core";
import { ArrowUpRight, PaperPlaneTilt } from "@phosphor-icons/react";
import { lazy, Suspense } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import { useFanoutApp } from "./app-context";
import { BrandLockup } from "./brand";
import type { MCPAppContent } from "./mcp-app-frame";

const MCPAppFrame = lazy(() => import("./mcp-app-frame"));

const activityLabels: Record<string, string> = {
  observability_overview: "Checking system health…",
  service_topology: "Mapping service dependencies…",
  service_performance: "Reading performance signals…",
  trace_detail: "Inspecting a trace…",
  search_logs: "Searching logs…",
  intelligence_snapshot: "Reviewing detected anomalies…",
};

export function activityLabel(toolName: string): string {
  return activityLabels[toolName] ?? "Working on it…";
}

export function ChatPage() {
  const { agentAvailable, messages, ready, running, error, bottomRef, send, threadMissing, newThread, retry } = useFanoutApp();
  if (!agentAvailable) return <Container size="sm" py={96}><Paper withBorder radius="xl" p={{ base: "xl", sm: 40 }}><Stack gap="md"><Text c="brand" fw={700} size="xs" tt="uppercase" lts="0.12em">Optional capability</Text><Title order={1}>Chat is not configured</Title><Text c="dimmed">Add an AI provider key to enable investigation chat. Telemetry ingest, dashboards, traces, logs, and metrics remain available without it.</Text><Button component="a" href="/dashboards" variant="light" mt="sm">Open dashboards</Button></Stack></Paper></Container>;
  if (threadMissing) return <Container size="sm" py="xl"><Alert color="warn" title="This chat no longer exists"><Button size="compact-sm" variant="light" onClick={newThread}>New chat</Button></Alert></Container>;
  const visibleMessages = messages.filter((message) => message.role !== "tool");
  if (!ready) return <Center mih="50vh"><Loader size="sm" /><Text c="dimmed" size="sm" ml="sm">Loading conversation</Text></Center>;
  return <Container size={1440} px={{ base: "md", sm: "xl", lg: 72 }} pt={{ base: 36, sm: 64 }} pb="xl">
    {visibleMessages.length === 0 && <Welcome onSelect={send} />}
    <Stack gap="xl" aria-live="polite">
      {visibleMessages.map((message) => <ChatMessage key={message.id} message={message} send={send} />)}
      {running && <Group gap="xs"><Loader type="dots" size="sm" /><Text c="dimmed" size="sm">Analyzing your system</Text></Group>}
      {error && <Alert color="bad" title="Something went wrong"><Stack gap="xs" align="flex-start"><Text size="sm">{error}</Text><Button size="compact-sm" variant="light" onClick={retry}>Try again</Button></Stack></Alert>}
      <div ref={bottomRef} />
    </Stack>
    <Composer />
  </Container>;
}

function Composer() {
  const { input, setInput, inputRef, submit, send, ready, running } = useFanoutApp();
  return <Box pb="md" pt="sm">
    <Paper component="form" onSubmit={submit} className="chat-composer-field" withBorder shadow="sm" radius={28} py={6} pl="lg" pr={6}><Group align="flex-end" gap="xs" wrap="nowrap">
      <Textarea ref={inputRef} aria-label="Message Fanout" value={input} onChange={(event) => setInput(event.currentTarget.value)} onKeyDown={(event) => { if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); void send(input); } }} placeholder={running ? "Fanout is analyzing…" : "Ask about health, errors, or latency…"} disabled={!ready || running} autosize minRows={1} maxRows={6} variant="unstyled" flex={1} />
      <ActionIcon type="submit" variant="filled" size={40} radius="xl" disabled={!input.trim() || !ready || running} aria-label="Send message"><PaperPlaneTilt size={17} weight="fill" /></ActionIcon>
    </Group></Paper>
  </Box>;
}

function Welcome({ onSelect }: { onSelect: (text: string) => Promise<void> }) {
  const suggestions = ["Summarize system health for the last hour", "Find the source of elevated errors", "Map the current service dependencies"];
  return <Stack align="center" gap="lg" maw={780} mx="auto" mb={56} ta="center">
    <BrandLockup size="large" />
    <Text c="brand" fw={700} size="xs" tt="uppercase" lts="0.14em">Your system, understood</Text>
    <Title order={1} fz={{ base: 40, sm: 56 }} lh={1.05} lts="-0.045em">See what changed.<br />Know what to do next.</Title>
    <Text c="dimmed" maw={620}>Ask about service health, latency, errors, or dependencies. Fanout turns live signals into clear answers and focused views.</Text>
    <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="sm" w="100%" mt="md">
      {suggestions.map((suggestion, index) => <UnstyledButton key={suggestion} onClick={() => void onSelect(suggestion)}><Paper withBorder radius="lg" p="md" mih={{ base: 74, sm: 120 }} h="100%"><Stack justify="space-between" h="100%" gap="md"><Text c="dimmed" size="xs" fw={700}>0{index + 1}</Text><Group justify="space-between" wrap="nowrap"><Text size="sm" fw={500}>{suggestion}</Text><ArrowUpRight size={17} weight="bold" /></Group></Stack></Paper></UnstyledButton>)}
    </SimpleGrid>
  </Stack>;
}

function ChatMessage({ message, send }: { message: Message; send: (text: string) => Promise<void> }) {
  if (message.role === "activity") {
    const activity = message as Message & { activityType?: string; content: MCPAppContent };
    if (activity.activityType === "mcp-app") return <Paper radius="lg" shadow="md" style={{ overflow: "hidden" }} aria-label={toolTitle(activity.content.toolName)}><Suspense fallback={<Center mih={180}><Loader size="sm" /></Center>}><MCPAppFrame content={activity.content} onMessage={send} /></Suspense></Paper>;
    return null;
  }
  const content = typeof message.content === "string" ? message.content : JSON.stringify(message.content);
  if (!content && message.role === "assistant") return null;
  const user = message.role === "user";
  return <Stack gap="xs" align={user ? "flex-end" : "stretch"} maw={user ? "min(92%, 650px)" : 780} ml={user ? "auto" : undefined}>
    <Group gap="xs" justify={user ? "flex-end" : "flex-start"}><Avatar size={22} radius="sm" color={user ? "gray" : "brand"}>{user ? "Y" : "F"}</Avatar><Text c="dimmed" size="xs" fw={700} tt="uppercase" lts="0.08em">{user ? "You" : "Fanout"}</Text></Group>
    {user ? <Paper withBorder radius="lg" p="sm" bg="var(--mantine-color-brand-light)"><Text style={{ whiteSpace: "pre-wrap" }}>{content}</Text></Paper> : <Typography><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Typography>}
  </Stack>;
}

function toolTitle(name: string) {
  return ({ observability_overview: "System health", service_topology: "Service map", service_performance: "Performance", trace_detail: "Trace analysis", search_logs: "Logs" } as Record<string, string>)[name] ?? "System analysis";
}
