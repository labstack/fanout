import type { Message } from "@ag-ui/client";
import { ActionIcon, Alert, Box, Button, Center, Container, Group, Loader, Paper, Stack, Table, Text, Textarea, Title, Tooltip, Typography, UnstyledButton } from "@mantine/core";
import { Check, Copy, PaperPlaneTilt, Stop, CaretDown, CaretRight, ArrowDown } from "@phosphor-icons/react";
import { lazy, Suspense, useEffect, useState, type ComponentProps, type ReactNode } from "react";
import Markdown, { type Components } from "react-markdown";
import remarkGfm from "remark-gfm";
import { toolFailureLabel, useFanoutApp, useDashboardReceipts } from "./app-context";
import { useTurnAnchor } from "./chat-scroll";
import { BrandMark } from "./brand";
import { useCopy } from "./copy";
import { exactTimestamp } from "../../format";
import { mcpAppContent, type MCPAppContent } from "./mcp-app-content";
import { fragmentTitle } from "../../panels/fragment";
import { interruptedResult } from "./dashboard-receipt";
import { jsonObject, mutationNames, object } from "./dashboard-tool-result";
import { DashboardReceiptView } from "./dashboard-receipt-view";

const MCPAppFrame = lazy(() => import("./mcp-app-frame"));

const isPreset = (content: MCPAppContent) => content.tool_result.view.kind === "preset";
type AppView = { content: MCPAppContent; expanded: boolean };
function chatAppViews(messages: Message[]) {
  const views = new Map<string, AppView>(), duplicates = new Set<string>();
  let turn: Array<{ id: string; content: MCPAppContent }> = [];
  const finish = () => {
    const winners = new Map<string, typeof turn[number]>();
    for (const item of turn) {
      const key = item.content.tool_result.view.key;
      winners.set(key, item);
    }
    const chosen = new Set([...winners.values()].map(item => item.id));
    const hasPreset = [...winners.values()].some(item => isPreset(item.content));
    const last = turn.filter(item => chosen.has(item.id)).at(-1)?.id;
    for (const item of turn) {
      if (!chosen.has(item.id)) duplicates.add(item.id);
      else views.set(item.id, { content: item.content, expanded: isPreset(item.content) || !hasPreset && item.id === last });
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
type ToolFailure = {name:string;message:string;details:string[];readOnly:boolean;rejected:boolean};
function draftProblems(value:unknown,titles?:string[]):string[] {
  return Array.isArray(value) ? value.flatMap(p => {
    if(!object(p) || typeof p.message !== "string") return [];
    const index = typeof p.path === "string" ? /^panels\[(\d+)\](?:\.|$)/.exec(p.path)?.[1] : undefined;
    const title = index === undefined ? undefined : titles?.[Number(index)];
    // Hints instruct the agent; the user sees what was wrong with the draft.
    return [title && !p.message.startsWith(title) ? `${title}: ${p.message}` : p.message];
  }) : [];
}
function toolFailures(messages: Message[]) {
  const groups = new Map<string, ToolFailure[]>();
  const calls = new Map<string, { assistant: string; name: string; titles?: string[] }>();
  const firstFailures = new Map<string, string>();
  let assistant = "unattributed";
  for (const message of messages) {
    if (message.role === "user") {
      calls.clear();
      firstFailures.clear();
      assistant = message.id;
    }
    if (message.role === "assistant") {
      assistant = message.id;
      for (const call of message.toolCalls ?? []) {
        const draft = ["create_dashboard", "replace_dashboard"].includes(call.function.name) ? jsonObject(call.function.arguments)?.dashboard : undefined;
        const titles = object(draft) && Array.isArray(draft.panels) ? draft.panels.map(p => object(p) && typeof p.title === "string" ? p.title : "") : undefined;
        calls.set(call.id, { assistant, name: call.function.name, titles });
      }
    }
    if (message.role === "tool") {
      const payload = jsonObject(message.content);
      if (!message.error && !payload?.error && payload?.isError !== true) continue;
      const call = calls.get(message.toolCallId);
      if(call && mutationNames.has(call.name) && interruptedResult(message)) continue;
      const failure = object(payload?.error) ? payload?.error : undefined;
      // Guidance tells the agent which signal exists; it is not a failed step.
      if (failure?.code === "no_data" || failure?.code === "unknown_service") continue;
      const owner = call?.assistant ?? assistant;
      const first = firstFailures.get(owner) ?? message.id;
      firstFailures.set(owner, first);
      const failures = groups.get(first) ?? [];
      const text = typeof failure?.message === "string" ? failure.message : typeof message.error === "string" && message.error ? message.error : "Could not complete this step. Try again.";
      const rejected = ["create_dashboard", "edit_dashboard", "replace_dashboard"].includes(call?.name ?? "") && (failure?.code === "invalid_spec" || failure?.code === "dashboard_version_conflict");
      failures.push({ name: toolFailureLabel(call?.name ?? ""), message: text, details: rejected ? draftProblems(payload?.problems,call?.titles) : [], readOnly: failure?.code === "answer_only", rejected });
      groups.set(first, failures);
    }
  }
  return groups;
}
function ToolFailures({failures}:{failures:ToolFailure[]}) {
  const [expanded,setExpanded]=useState({rejected:false,failed:false});
  const rejected = failures.filter(f => f.rejected), failed = failures.filter(f => !f.readOnly && !f.rejected);
  const groups = [
    {key:"rejected",items:rejected,label:`${rejected.length} ${rejected.length === 1 ? "draft" : "drafts"} rejected`},
    {key:"failed",items:failed,label:`${failed.length} ${failed.length === 1 ? "step" : "steps"} failed`},
  ] as const;
  return <Box data-chat-anchor>
    {failures.some(f => f.readOnly) && <Text size="sm" c="dimmed">Not saved: Explain is read-only</Text>}
    {groups.map(({key,items,label}) => items.length > 0 && <Box key={key}>
      <UnstyledButton className="chat-app-toggle" aria-expanded={expanded[key]} onClick={()=>setExpanded(v=>({...v,[key]:!v[key]}))} style={{color:"var(--mantine-color-dimmed)"}}>
        <span className="chat-app-chevron" aria-hidden="true">{expanded[key]?<CaretDown size={14}/>:<CaretRight size={14}/>}</span><span className="chat-app-summary">{label}</span>
      </UnstyledButton>
      {expanded[key] && <Stack gap="xs" pl="md">{items.flatMap((failure,i)=>(failure.details.length ? failure.details : [`${failure.name}: ${failure.message}`]).map((text,j)=><Text key={`${i}-${j}`} size="sm" c="dimmed" style={{whiteSpace:"pre-wrap",overflowWrap:"anywhere"}}>{text}</Text>))}</Stack>}
    </Box>)}
  </Box>;
}
function ChatAppView({ view }: { view: AppView }) {
  const [expanded, setExpanded] = useState(view.expanded);
  useEffect(() => setExpanded(view.expanded), [view.expanded]);
  const fragment = view.content.tool_result;
  const title = fragmentTitle(fragment);
  const viz = [...new Set(fragment.dashboard.panels.map(panel => panel.viz === "timeseries" ? "time series" : panel.viz.replaceAll("_", " ")))].join(", ");
  const rows = fragment.results.reduce((count, result) => count + (result.frame?.rows ?? 0), 0);
  const counts=new Map<string,number>();
  for(const result of fragment.results) if(result.status!=="ok") counts.set(result.status,(counts.get(result.status)??0)+1);
  // The kinds of chart already say what a view shows; only what went wrong is counted.
  const status = fragment.dashboard.panels.length>1
    ? Array.from(counts,([status,count])=>`${count} ${status}`).join(" · ")
    : counts.size ? [...counts.keys()].join(", ") : `${rows} ${rows === 1 ? "row" : "rows"}`;
  return <Paper data-chat-app data-chat-anchor withBorder radius="lg" style={{ overflow: "hidden" }}>
    <UnstyledButton className="chat-app-toggle" aria-expanded={expanded} onClick={() => setExpanded(value => !value)}><span className="chat-app-chevron" aria-hidden="true">{expanded ? <CaretDown size={14} /> : <CaretRight size={14} />}</span><span className="chat-app-summary">{[title, viz, status].filter(Boolean).join(" · ")}</span></UnstyledButton>
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
  const { agentAvailable, messages, messageTimes, ready, running, activity, stopped, error, threadMissing, send, retry, reloadThread, newThread } = useFanoutApp();
  const receipts = useDashboardReceipts(messages);
  const lastSent = messages.filter((message) => message.role === "user").at(-1)?.id;
  const { scrollRef, contentRef, turnRef, showJump, jumpToLatest } = useTurnAnchor<HTMLDivElement, HTMLDivElement>(lastSent);
  if (!agentAvailable) return <Container size="sm" py={96}><Paper withBorder radius="lg" p={{ base: "xl", sm: 40 }}><Stack gap="md"><Text c="brand" fw={700} size="xs" tt="uppercase" lts="0.12em">Optional capability</Text><Title order={1} fz={28}>Chat is not configured</Title><Text c="dimmed">Add an AI provider key to enable chat. Telemetry ingest, dashboards, traces, logs, and metrics remain available without it.</Text><Button component="a" href="/dashboards" variant="light" mt="sm">Open dashboards</Button></Stack></Paper></Container>;
  const appViews = chatAppViews(messages);
  const failures = toolFailures(messages);
  const visibleMessages = messages.filter((message) => {
    if(appViews.duplicates.has(message.id)) return false;
    if(message.role === "tool") return failures.has(message.id);
    if(message.role === "assistant") return !message.toolCalls?.length && typeof message.content === "string" && message.content.trim().length > 0;
    if(message.role === "activity") return message.activityType === "mcp-app" || message.activityType === "agent-outcome";
    return message.role === "user";
  });
  // Tool receipts can finish before the buffered final answer. Only text in
  // the current user turn replaces its running status.
  const turnStart = messages.findIndex(message => message.id === lastSent);
  const hasFinalText = messages.slice(turnStart + 1).some(message => message.role === "assistant" && !message.toolCalls?.length && typeof message.content === "string" && message.content.length > 0);
  const answer = messages.slice(turnStart + 1).filter(message => message.role === "assistant" && !message.toolCalls?.length && typeof message.content === "string" && message.content.trim().length > 0).at(-1);
  // Keep each turn's wrapper when the next question arrives so iframe views
  // retain their mounted state as the current turn becomes history.
  const turns: Array<{ id: string; messages: Message[] }> = [];
  for (const message of visibleMessages) {
    if (message.role === "user" || turns.length === 0) turns.push({ id: message.id, messages: [] });
    turns.at(-1)!.messages.push(message);
  }
  if (turns.length === 0) turns.push({ id: "draft", messages: [] });
  const renderMessage = (message: Message) => failures.has(message.id)
    ? <ToolFailures key={message.id} failures={failures.get(message.id)!} />
    : <Box key={message.id}>
      <ChatMessage message={message} time={messageTimes[message.id]} appView={appViews.views.get(message.id)} />
      {receipts.has(message.id) && <Box mt="sm"><DashboardReceiptView receipt={receipts.get(message.id)!} running={running && message.id === lastSent} awaitingAnswer={!hasFinalText} activity={activity} /></Box>}
    </Box>;
  return <Box className="chat-pane">
    {/* A scroll region has to be reachable without a mouse. Chrome makes a
        scroller focusable only when it holds no focusable children, and this
        one holds copy buttons and links, so it says so itself. */}
    <Box className="chat-viewport">
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
            <Stack gap="lg">
              {turns.map((turn, index) => <Stack gap="lg" key={turn.id} ref={turn.id === lastSent ? turnRef : undefined} data-chat-turn={turn.id === lastSent ? lastSent : undefined}>
                {turn.messages.filter(message => message.id !== answer?.id).map(renderMessage)}
                {index === turns.length - 1 && <>
                  {answer && <Box data-answer-position key={`answer-${lastSent ?? "draft"}`}>
                    <ChatMessage message={answer!} time={messageTimes[answer!.id]} />
                  </Box>}
                  {running && !hasFinalText && !receipts.has(lastSent ?? "") && <Group gap="xs" role="status"><Loader type="dots" size="sm" /><Text c="dimmed" size="sm">{activity || "Analyzing your system"}</Text></Group>}
                  {stopped && <Text c="dimmed" size="sm">Stopped</Text>}
                  {error && <RunError message={error} onRetry={retry} />}
                </>}
              </Stack>)}
            </Stack>
          </>}
        </Container>
      </Box>
      {showJump && <Tooltip label="Jump to latest"><ActionIcon className="chat-jump" variant="default" size={36} radius="xl" aria-label="Jump to latest" onClick={jumpToLatest}><ArrowDown size={18} aria-hidden="true" /></ActionIcon></Tooltip>}
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

  if (message.role === "activity") {
    if (message.activityType === "agent-outcome" && message.content && typeof message.content === "object" && "message" in message.content && typeof message.content.message === "string") {
      return "status" in message.content && message.content.status === "stopped" ? <Text c="dimmed" size="sm" data-chat-anchor>{message.content.message}</Text> : <Alert color="bad" title="Something went wrong" data-chat-anchor>{message.content.message}</Alert>;
    }
    if (message.activityType === "mcp-app") return appView ? <ChatAppView view={appView} /> : <Alert color="bad" data-chat-anchor>This view could not be loaded. Please try again.</Alert>;
    return null;
  }
  if (message.role === "assistant" && message.toolCalls?.length) return null;
  const content = typeof message.content === "string" ? message.content : JSON.stringify(message.content);
  if (!content && message.role === "assistant") return null;
  const user = message.role === "user";
  const stamp = time ? new Intl.DateTimeFormat(undefined, { hour: "numeric", minute: "2-digit" }).format(new Date(time)) : "";
  return <Box className="chat-message" data-chat-anchor data-role={user ? "user" : "assistant"}>
    {user
      ? <Paper radius="lg" px="md" py="sm" bg="var(--mantine-color-brand-light)" maw="70%" ml="auto" w="fit-content"><Text style={{ whiteSpace: "pre-wrap" }}>{content}</Text></Paper>
      : <Typography className="chat-markdown"><Markdown remarkPlugins={[remarkGfm]} components={markdownComponents}>{content}</Markdown></Typography>}
    {(stamp || !user) && <Group className="chat-message-meta" gap={6} justify={user ? "flex-end" : "flex-start"} mt={4} h={24}>
      {stamp && time && <Text c="dimmed" size="xs" title={exactTimestamp(time)}>{stamp}</Text>}
      {!user && <CopyButton text={content} label="Copy message" />}
    </Group>}
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
