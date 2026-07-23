import { HttpAgent, type Message } from "@ag-ui/client";
import { ActionIcon, Alert, AppShell, Avatar, Box, Button, Center, Container, Group, Loader, Paper, SimpleGrid, Stack, Text, Textarea, Title, Tooltip, Typography, UnstyledButton } from "@mantine/core";
import { ArrowUpRight, ChatCircleText, GithubLogo, GlobeHemisphereWest, Layout, PaperPlaneTilt, Plus, SignOut } from "@phosphor-icons/react";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { createContext, FormEvent, lazy, Suspense, useContext, useEffect, useMemo, useRef, useState, type RefObject } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import AuthGate, { authorizedFetch, logout } from "./auth";
import { BrandLockup } from "./brand";
import type { MCPAppContent } from "./mcp-app-frame";
import { createID } from "./id";

const MCPAppFrame = lazy(() => import("./mcp-app-frame"));
const threadKey = "fanout.thread-id";

type FanoutAppContextValue = {
  messages: Message[];
  ready: boolean;
  running: boolean;
  input: string;
  setInput: (value: string) => void;
  error: string;
  bottomRef: RefObject<HTMLDivElement | null>;
  inputRef: RefObject<HTMLTextAreaElement | null>;
  send: (text: string) => Promise<void>;
  submit: (event: FormEvent) => void;
  openChat: (prompt?: string) => void;
};

const FanoutAppContext = createContext<FanoutAppContextValue | null>(null);

export function useFanoutApp() {
  const context = useContext(FanoutAppContext);
  if (!context) throw new Error("Fanout app context is unavailable");
  return context;
}

function Chat() {
  const navigate = useNavigate();
  const isChat = useRouterState({ select: (state) => state.location.pathname === "/" });
  const storedThreadID = useMemo(() => localStorage.getItem(threadKey), []);
  const threadID = useMemo(() => storedThreadID ?? createID(), [storedThreadID]);
  const [messages, setMessages] = useState<Message[]>([]);
  const [ready, setReady] = useState(false);
  const [running, setRunning] = useState(false);
  const [input, setInput] = useState("");
  const [error, setError] = useState("");
  const bottomRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const agent = useMemo(() => new HttpAgent({ url: "/api/agent", threadId: threadID, fetch: (url, init) => authorizedFetch(url, init) }), [threadID]);

  useEffect(() => {
    let active = true;
    const loadedThread = storedThreadID
      ? authorizedFetch(`/api/agent/threads/${encodeURIComponent(threadID)}`).then(async (response) => {
        if (response.status === 404) { localStorage.removeItem(threadKey); return { messages: [] }; }
        return response.ok ? response.json() : Promise.reject(new Error(`Unable to load thread (${response.status})`));
      })
      : Promise.resolve({ messages: [] });
    loadedThread.then((thread) => {
      if (!active) return;
      agent.setMessages(thread.messages ?? []);
      setMessages([...(thread.messages ?? [])]);
      setReady(true);
    }).catch(() => { if (active) { setError("This conversation could not be restored. Start a new chat or try again."); setReady(true); } });
    const subscription = agent.subscribe({
      onEvent: ({ messages: next }) => setMessages([...next] as Message[]),
      onRunInitialized: () => { setRunning(true); setError(""); },
      onRunFinalized: ({ messages: next }) => { setMessages([...next] as Message[]); setRunning(false); },
      onRunFailed: (failure) => { console.error("Agent run failed", failure); setError("Fanout could not complete this analysis. Please try again."); setRunning(false); },
    });
    return () => { active = false; subscription.unsubscribe(); agent.abortRun(); };
  }, [agent, storedThreadID, threadID]);

  useEffect(() => { bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" }); }, [messages, running]);

  useEffect(() => {
    const shortcuts = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      const editing = target?.matches("input, textarea, [contenteditable='true']");
      if (event.key === "/" && !editing) {
        event.preventDefault();
        void navigate({ to: "/" });
        requestAnimationFrame(() => inputRef.current?.focus());
      }
      if (event.key === "Escape" && target === inputRef.current) { setInput(""); inputRef.current?.blur(); }
    };
    window.addEventListener("keydown", shortcuts);
    return () => window.removeEventListener("keydown", shortcuts);
  }, [navigate]);

  async function send(text: string) {
    const content = text.trim();
    if (!content || running || !ready) return;
    const message = { id: createID(), role: "user", content } as Message;
    agent.addMessage(message);
    setMessages([...agent.messages]);
    setInput("");
    setRunning(true);
    setError("");
    localStorage.setItem(threadKey, threadID);
    try { await agent.runAgent(); } catch (cause) { console.error("Agent run failed", cause); setError("Fanout could not complete this analysis. Please try again."); setRunning(false); }
  }

  function submit(event: FormEvent) { event.preventDefault(); void send(input); }
  function openChat(prompt?: string) { void navigate({ to: "/" }).then(() => { if (prompt) void send(prompt); }); }
  function newThread() { agent.abortRun(); localStorage.removeItem(threadKey); location.reload(); }

  return <FanoutAppContext.Provider value={{ messages, ready, running, input, setInput, error, bottomRef, inputRef, send, submit, openChat }}>
    <AppShell header={{ height: 56 }} footer={{ height: 42 }} padding={0}>
      <AppShell.Header><Group h="100%" px={{ base: "sm", sm: "lg" }} justify="space-between" wrap="nowrap">
        <BrandLockup size="small" />
        <Group gap="xs" wrap="nowrap">
          <Group gap={6} mr={4} visibleFrom="md"><Box w={7} h={7} bg="teal.6" style={{ borderRadius: "50%" }} /><Text c="dimmed" size="xs" fw={600}>Live</Text></Group>
          <Button variant="subtle" color="gray" size="compact-sm" leftSection={isChat ? <Layout size={16} weight="bold" /> : <ChatCircleText size={16} weight="bold" />} onClick={() => void navigate({ to: isChat ? "/dashboards" : "/" })}>{isChat ? "Dashboard" : "Chat"}</Button>
          {isChat && <Tooltip label="New chat"><ActionIcon variant="subtle" color="gray" aria-label="New chat" onClick={newThread}><Plus size={17} weight="bold" /></ActionIcon></Tooltip>}
          <Tooltip label="Sign out"><ActionIcon variant="subtle" color="gray" aria-label="Sign out" onClick={() => void logout().catch((cause) => setError(cause instanceof Error ? cause.message : "Sign-out failed — your session is still active."))}><SignOut size={17} /></ActionIcon></Tooltip>
        </Group>
      </Group></AppShell.Header>
      <AppShell.Main><Outlet />{isChat && <Composer />}</AppShell.Main>
      <ProductFooter />
    </AppShell>
  </FanoutAppContext.Provider>;
}

function Composer() {
  const { input, setInput, inputRef, submit, send, ready, running } = useFanoutApp();
  return <Box pos="fixed" bottom={42} left={0} right={0} px={{ base: "xs", sm: "md" }} pb="md" pt="xl" style={{ zIndex: 20, background: "linear-gradient(transparent, var(--mantine-color-body) 45%)" }}>
    <Paper component="form" onSubmit={submit} withBorder shadow="lg" radius="xl" p={8} pl="md" maw={820} mx="auto"><Group align="flex-end" gap="xs" wrap="nowrap">
      <Textarea ref={inputRef} aria-label="Message Fanout" value={input} onChange={(event) => setInput(event.currentTarget.value)} onKeyDown={(event) => { if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); void send(input); } }} placeholder={running ? "Fanout is analyzing…" : "Ask about health, errors, or latency…"} disabled={!ready || running} autosize minRows={1} maxRows={6} variant="unstyled" flex={1} />
      <ActionIcon type="submit" size={40} radius="md" disabled={!input.trim() || !ready || running} aria-label="Send message"><PaperPlaneTilt size={19} weight="fill" /></ActionIcon>
    </Group></Paper>
    <Text ta="center" c="dimmed" size="xs" mt={6} visibleFrom="sm">Enter to send · Shift + Enter for a new line</Text>
  </Box>;
}

export function ChatPage() {
  const { messages, ready, running, error, bottomRef, send } = useFanoutApp();
  const visibleMessages = messages.filter((message) => message.role !== "tool");
  return <Container size={1440} px={{ base: "md", sm: "xl", lg: 72 }} pt={{ base: 36, sm: 64 }} pb={190}>
    {!ready && <Center mih="50vh"><Loader size="sm" /><Text c="dimmed" size="sm" ml="sm">Loading conversation</Text></Center>}
    {visibleMessages.length === 0 && ready && <Welcome onSelect={send} />}
    <Stack gap="xl" aria-live="polite">
      {visibleMessages.map((message) => <ChatMessage key={message.id} message={message} send={send} />)}
      {running && <Group gap="xs"><Loader type="dots" size="sm" /><Text c="dimmed" size="sm">Analyzing your system</Text></Group>}
      {error && <Alert color="red" title="Something went wrong">{error}</Alert>}
      <div ref={bottomRef} />
    </Stack>
  </Container>;
}

function Welcome({ onSelect }: { onSelect: (text: string) => Promise<void> }) {
  const suggestions = ["Summarize system health for the last hour", "Find the source of elevated errors", "Map the current service dependencies"];
  return <Stack align="center" gap="lg" maw={780} mx="auto" mb={56} ta="center">
    <BrandLockup size="large" />
    <Text c="teal" fw={700} size="xs" tt="uppercase" lts="0.14em">Your system, understood</Text>
    <Title order={1} fz={{ base: 40, sm: 56 }} lh={1.05} lts="-0.045em">See what changed.<br />Know what to do next.</Title>
    <Text c="dimmed" maw={620}>Ask about service health, latency, errors, or dependencies. Fanout turns live signals into clear answers and focused views.</Text>
    <SimpleGrid cols={{ base: 1, sm: 3 }} spacing="sm" w="100%" mt="md">
      {suggestions.map((suggestion, index) => <UnstyledButton key={suggestion} onClick={() => void onSelect(suggestion)}><Paper withBorder radius="lg" p="md" mih={{ base: 74, sm: 120 }} h="100%"><Stack justify="space-between" h="100%" gap="md"><Text c="dimmed" size="xs" fw={700}>0{index + 1}</Text><Group justify="space-between" wrap="nowrap"><Text size="sm" fw={500}>{suggestion}</Text><ArrowUpRight size={17} weight="bold" /></Group></Stack></Paper></UnstyledButton>)}
    </SimpleGrid>
  </Stack>;
}

function ProductFooter() {
  return <AppShell.Footer><Group h="100%" px={{ base: "sm", sm: "lg" }} justify="space-between" wrap="nowrap">
    <Text c="dimmed" size="xs">© 2026 Fanout by <Text component="a" href="https://labstack.com" target="_blank" rel="noreferrer" inherit fw={600} c="var(--mantine-color-text)">LabStack</Text></Text>
    <Group gap={4}><Tooltip label="GitHub"><ActionIcon component="a" href="https://github.com/labstack/fanout" target="_blank" rel="noreferrer" variant="subtle" color="gray" size="sm" aria-label="Fanout on GitHub"><GithubLogo size={14} weight="bold" /></ActionIcon></Tooltip><Tooltip label="LabStack"><ActionIcon component="a" href="https://labstack.com" target="_blank" rel="noreferrer" variant="subtle" color="gray" size="sm" aria-label="LabStack website"><GlobeHemisphereWest size={14} /></ActionIcon></Tooltip></Group>
  </Group></AppShell.Footer>;
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
    <Group gap="xs" justify={user ? "flex-end" : "flex-start"}><Avatar size={22} radius="sm" color={user ? "gray" : "teal"}>{user ? "Y" : "F"}</Avatar><Text c="dimmed" size="xs" fw={700} tt="uppercase" lts="0.08em">{user ? "You" : "Fanout"}</Text></Group>
    {user ? <Paper withBorder radius="lg" p="sm" bg="teal.0"><Text style={{ whiteSpace: "pre-wrap" }}>{content}</Text></Paper> : <Typography><Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown></Typography>}
  </Stack>;
}

function toolTitle(name: string) {
  return ({ observability_overview: "System health", service_topology: "Service map", service_performance: "Performance", trace_detail: "Trace analysis", search_logs: "Logs" } as Record<string, string>)[name] ?? "System analysis";
}

export default function App() {
  const queryClient = useMemo(() => new QueryClient(), []);
  return <QueryClientProvider client={queryClient}><AuthGate><Chat /></AuthGate></QueryClientProvider>;
}
