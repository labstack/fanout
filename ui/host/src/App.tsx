import { HttpAgent, type Message } from "@ag-ui/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { ArrowUpRight, ChatCircleText, Layout, PaperPlaneTilt, Plus, SignOut } from "@phosphor-icons/react";
import { FormEvent, lazy, Suspense, useEffect, useMemo, useRef, useState } from "react";
import Markdown from "react-markdown";
import remarkGfm from "remark-gfm";
import AuthGate, { authorizedFetch, logout } from "./auth";
import type { MCPAppContent } from "./mcp-app-frame";
import Dashboard from "./dashboard";
import { createID } from "./id";

const MCPAppFrame = lazy(() => import("./mcp-app-frame"));

const threadKey = "fanout.thread-id";

function Chat() {
  const [view, setView] = useState<"chat" | "dashboard">("chat");
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

  function newThread() {
    agent.abortRun();
    localStorage.removeItem(threadKey);
    location.reload();
  }

  const visibleMessages = messages.filter((message) => message.role !== "tool");
  return (
    <div className="app-shell">
      <header>
        <div className="brand"><span className="brand-mark small" aria-hidden="true">F</span><div><strong>Fanout</strong><small>Operations intelligence</small></div></div>
        <div className="header-actions">
          <span className="live"><i />Connected</span>
          <button type="button" className="ghost view-switch" onClick={() => setView(view === "chat" ? "dashboard" : "chat")}>{view === "chat" ? <Layout size={15} weight="bold" aria-hidden="true" /> : <ChatCircleText size={15} weight="bold" aria-hidden="true" />}<span className="action-label">{view === "chat" ? "Dashboard" : "Chat"}</span></button>
          {view === "chat" && <button type="button" className="ghost new-thread" onClick={newThread} aria-label="New chat"><Plus size={15} weight="bold" aria-hidden="true" /><span className="action-label">New chat</span></button>}
          <button type="button" className="ghost quiet signout" onClick={() => void logout()} aria-label="Sign out"><SignOut size={15} aria-hidden="true" /><span className="action-label">Sign out</span></button>
        </div>
      </header>
      {view === "dashboard" ? <Dashboard onOpenChat={(prompt) => { setView("chat"); if (prompt) void send(prompt); }} /> : <main className="chat">
        {!ready && <div className="thread-loading" role="status"><span /><span /><span /> Loading conversation</div>}
        {visibleMessages.length === 0 && ready && (
          <section className="welcome">
            <div className="welcome-mark" aria-hidden="true">F</div>
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
      </main>}
      {view === "chat" && <footer className="composer-wrap">
        <form className="composer" onSubmit={submit}>
          <textarea ref={inputRef} aria-label="Message Fanout" value={input} onChange={(event) => setInput(event.target.value)} onKeyDown={(event) => { if (event.key === "Enter" && !event.shiftKey) { event.preventDefault(); void send(input); } }} placeholder={running ? "Fanout is analyzing…" : "Ask about health, errors, or latency…"} rows={1} disabled={!ready || running} />
          <button type="submit" disabled={!input.trim() || !ready || running} aria-label="Send message"><PaperPlaneTilt size={19} weight="fill" aria-hidden="true" /></button>
        </form>
        <small>Enter to send <span>·</span> Shift + Enter for a new line</small>
      </footer>}
    </div>
  );
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
  return <QueryClientProvider client={queryClient}><AuthGate><Chat /></AuthGate></QueryClientProvider>;
}
