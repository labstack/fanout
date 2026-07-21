import { HttpAgent, type Message } from "@ag-ui/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { Outlet, useNavigate, useRouterState } from "@tanstack/react-router";
import { ArrowUpRight, ChatCircleText, GithubLogo, GlobeHemisphereWest, Layout, PaperPlaneTilt, Plus, SignOut } from "@phosphor-icons/react";
import { createContext, FormEvent, lazy, Suspense, useContext, useEffect, useMemo, useRef, useState, type RefObject } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import AuthGate, { authorizedFetch, logout } from "./auth";
import { BrandMark } from "./brand";
import type { MCPAppContent } from "./mcp-app-frame";
import { createID } from "./id";
import { Tooltip, TooltipProvider } from "./ui";

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
  const agent = useMemo(() => new HttpAgent({
    url: "/api/agent",
    threadId: threadID,
    fetch: (url, init) => authorizedFetch(url, init),
  }), [threadID]);

  useEffect(() => {
    let active = true;
    const loadedThread = storedThreadID
      ? authorizedFetch(`/api/agent/threads/${encodeURIComponent(threadID)}`)
      .then(async (response) => {
        if (response.status === 404) {
          localStorage.removeItem(threadKey);
          return { messages: [] };
        }
        return response.ok ? response.json() : Promise.reject(new Error(`Unable to load thread (${response.status})`));
      })
      : Promise.resolve({ messages: [] });
    loadedThread
      .then((thread) => {
        if (!active) return;
        agent.setMessages(thread.messages ?? []);
        setMessages([...(thread.messages ?? [])]);
        setReady(true);
      })
      .catch(() => { if (active) { setError("This conversation could not be restored. Start a new chat or try again."); setReady(true); } });
    const subscription = agent.subscribe({
      onEvent: ({ messages: next }) => setMessages([...next] as Message[]),
      onRunInitialized: () => { setRunning(true); setError(""); },
      onRunFinalized: ({ messages: next }) => { setMessages([...next] as Message[]); setRunning(false); },
      onRunFailed: () => { setError("Fanout could not complete this analysis. Please try again."); setRunning(false); },
    });
    return () => { active = false; subscription.unsubscribe(); agent.abortRun(); };
  }, [agent, storedThreadID, threadID]);

  useEffect(() => {
    bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" });
  }, [messages, running]);

  useEffect(() => {
    const textarea = inputRef.current;
    if (!textarea) return;
    textarea.style.height = "auto";
    textarea.style.height = `${Math.min(textarea.scrollHeight, 160)}px`;
  }, [input]);

  useEffect(() => {
    const shortcuts = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      const editing = target?.matches("input, textarea, [contenteditable='true']");
      if (event.key === "/" && !editing) {
        event.preventDefault();
        void navigate({ to: "/" });
        requestAnimationFrame(() => inputRef.current?.focus());
      }
      if (event.key === "Escape" && target === inputRef.current) {
        setInput("");
        inputRef.current?.blur();
      }
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
    try { await agent.runAgent(); } catch { setError("Fanout could not complete this analysis. Please try again."); setRunning(false); }
  }

  function submit(event: FormEvent) { event.preventDefault(); void send(input); }

  function openChat(prompt?: string) {
    void navigate({ to: "/" }).then(() => { if (prompt) void send(prompt); });
  }

  function newThread() {
    agent.abortRun();
    localStorage.removeItem(threadKey);
    location.reload();
  }

  return (
    <FanoutAppContext.Provider value={{ messages, ready, running, input, setInput, error, bottomRef, inputRef, send, submit, openChat }}><div className="app-shell">
      <header className="sticky top-0 z-20 flex h-[54px] min-w-0 items-center justify-between border-b border-line bg-canvas/90 px-6 backdrop-blur-xl">
        <div className="flex min-w-0 items-center gap-2.5"><BrandMark size="small" /><div><strong className="block text-sm tracking-[-.02em]">Fanout</strong><small className="mt-0.5 hidden text-[10px] text-muted sm:block">Operations intelligence</small></div></div>
        <div className="flex items-center gap-1.5">
          <span className="mr-1 hidden items-center gap-1.5 text-[10px] font-semibold text-muted md:inline-flex"><i className="size-1.5 rounded-full bg-accent shadow-[0_0_8px_var(--accent-glow)]" />Live</span>
          <button type="button" className="inline-flex h-8 items-center gap-1.5 rounded-lg px-2.5 text-xs font-semibold text-text-soft transition hover:bg-panel-raised hover:text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent" onClick={() => void navigate({ to: isChat ? "/dashboards" : "/" })}>{isChat ? <Layout size={15} weight="bold" aria-hidden="true" /> : <ChatCircleText size={15} weight="bold" aria-hidden="true" />}<span>{isChat ? "Dashboard" : "Chat"}</span></button>
          {isChat && <Tooltip label="New chat"><button type="button" className="grid size-8 place-items-center rounded-lg text-muted transition hover:bg-panel-raised hover:text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent" onClick={newThread} aria-label="New chat"><Plus size={16} weight="bold" aria-hidden="true" /></button></Tooltip>}
          <Tooltip label="Sign out"><button type="button" className="grid size-8 place-items-center rounded-lg text-muted transition hover:bg-panel-raised hover:text-text focus-visible:outline-2 focus-visible:outline-offset-2 focus-visible:outline-accent" onClick={() => void logout()} aria-label="Sign out"><SignOut size={16} aria-hidden="true" /></button></Tooltip>
        </div>
      </header>
      <Outlet />
      {isChat && <footer className="composer-wrap">
        <form className="composer" onSubmit={submit}>
          <textarea ref={inputRef} aria-label="Message Fanout" value={input} onChange={(event) => setInput(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); void send(input); } }} placeholder={running ? "Fanout is analyzing…" : "Ask about health, errors, or latency…"} rows={1} disabled={!ready || running} />
          <button type="submit" disabled={!input.trim() || !ready || running} aria-label="Send message"><PaperPlaneTilt size={19} weight="fill" aria-hidden="true" /></button>
        </form>
        <small>Enter to send <span>·</span> Shift + Enter for a new line</small>
      </footer>}
      <ProductFooter />
    </div></FanoutAppContext.Provider>
  );
}

export function ChatPage() {
  const { messages, ready, running, error, bottomRef, send } = useFanoutApp();
  const visibleMessages = messages.filter((message) => message.role !== "tool");
  return <main className="chat">
    {!ready && <div className="thread-loading" role="status"><span /><span /><span /> Loading conversation</div>}
    {visibleMessages.length === 0 && ready && (
      <section className="welcome">
        <BrandMark size="large" />
        <p className="eyebrow">YOUR SYSTEM, UNDERSTOOD</p>
        <h1>See what changed.<br />Know what to do next.</h1>
        <p>Ask about service health, latency, errors, or dependencies. Fanout turns live signals into clear answers and focused views.</p>
        <div className="suggestions">
          {["Summarize system health for the last hour", "Find the source of elevated errors", "Map the current service dependencies"].map((suggestion, index) => <button key={suggestion} onClick={() => void send(suggestion)}><small>0{index + 1}</small>{suggestion}<ArrowUpRight size={17} weight="bold" aria-hidden="true" /></button>)}
        </div>
      </section>
    )}
    <section className="messages" aria-live="polite">
      {visibleMessages.map((message) => <ChatMessage key={message.id} message={message} send={send} />)}
      {running && <div className="thinking" role="status"><span /><span /><span /> Analyzing your system</div>}
      {error && <div className="error-banner" role="alert"><strong>Something went wrong</strong><span>{error}</span></div>}
      <div ref={bottomRef} />
    </section>
  </main>;
}

function ProductFooter() {
  return <footer className="fixed inset-x-0 bottom-0 z-30 flex h-[42px] items-center justify-between border-t border-line bg-canvas/95 px-6 text-[10px] text-muted backdrop-blur-xl">
    <div><span>© 2026 Recast by <a className="font-semibold text-text-soft transition hover:text-text" href="https://labstack.com" target="_blank" rel="noreferrer">LabStack</a></span></div>
    <div className="hidden items-center gap-3 md:flex"><span className="inline-flex items-center gap-1.5"><kbd className="min-w-5 rounded border border-line bg-panel-soft px-1.5 py-0.5 text-center font-mono text-[9px] text-text-soft">/</kbd> focus</span><span className="inline-flex items-center gap-1.5"><kbd className="rounded border border-line bg-panel-soft px-1.5 py-0.5 font-mono text-[9px] text-text-soft">Esc</kbd> clear</span></div>
    <div className="flex items-center gap-1"><Tooltip label="GitHub"><a className="grid size-7 place-items-center rounded-md transition hover:bg-panel-raised hover:text-text" href="https://github.com/labstack/fanout" target="_blank" rel="noreferrer" aria-label="Fanout on GitHub"><GithubLogo size={14} weight="bold" /></a></Tooltip><Tooltip label="LabStack"><a className="grid size-7 place-items-center rounded-md transition hover:bg-panel-raised hover:text-text" href="https://labstack.com" target="_blank" rel="noreferrer" aria-label="LabStack website"><GlobeHemisphereWest size={14} /></a></Tooltip></div>
  </footer>;
}

function ChatMessage({ message, send }: { message: Message; send: (text: string) => Promise<void> }) {
  if (message.role === "activity") {
    const activity = message as Message & { activityType?: string; content: MCPAppContent };
    if (activity.activityType === "mcp-app") return <section className="activity" aria-label={toolTitle(activity.content.toolName)}>
      <Suspense fallback={<div className="app-loading">Preparing view…</div>}><MCPAppFrame content={activity.content} onMessage={send} /></Suspense>
    </section>;
    return null;
  }
  const content = typeof message.content === "string" ? message.content : JSON.stringify(message.content);
  if (!content && message.role === "assistant") return null;
  const user = message.role === "user";
  return <article className={`message ${message.role}`}>
    <div className="message-identity"><span className="message-avatar" aria-hidden="true">{user ? "Y" : "F"}</span><span>{user ? "You" : "Fanout"}</span></div>
    <div className={`message-body ${user ? "" : "markdown"}`}>
      {user ? content : <Markdown remarkPlugins={[remarkGfm]}>{content}</Markdown>}
    </div>
  </article>;
}

function toolTitle(name: string) {
  return ({
    observability_overview: "System health",
    service_topology: "Service map",
    service_performance: "Performance",
    trace_detail: "Trace analysis",
    search_logs: "Logs",
  } as Record<string, string>)[name] ?? "System analysis";
}

export default function App() {
  const queryClient = useMemo(() => new QueryClient(), []);
  return <QueryClientProvider client={queryClient}><TooltipProvider delayDuration={300}><AuthGate><Chat /></AuthGate></TooltipProvider></QueryClientProvider>;
}
