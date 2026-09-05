import { HttpAgent, type Message } from "@ag-ui/client";
import { QueryClient, QueryClientProvider, useQueryClient } from "@tanstack/react-query";
import { Outlet, useNavigate, useParams } from "@tanstack/react-router";
import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { FanoutAppContext } from "./app-context";
import AuthGate, { authorizedFetch, useRuntimeStatus } from "./auth";
import { activityLabel } from "./chat";
import { createID } from "./id";
import { threadHistoryQueryKey } from "./rail";
import Shell from "./shell";

function Session() {
  const { agent_available: agentAvailable } = useRuntimeStatus();
  const navigate = useNavigate();
  const queryClient = useQueryClient();
  const { threadId: routeThreadID } = useParams({ strict: false }) as { threadId?: string };
  // A draft is a thread the browser has named but the server has not seen.
  // Its id is remembered so the route change on first send does not trigger
  // a fetch for a thread that does not exist yet.
  const [draftID, setDraftID] = useState(createID);
  const draftsRef = useRef(new Set<string>());
  const threadID = routeThreadID ?? draftID;
  const [messages, setMessages] = useState<Message[]>([]);
  const [messageTimes, setMessageTimes] = useState<Record<string, number>>({});
  const [loadedThreadID, setLoadedThreadID] = useState("");
  const [threadMissing, setThreadMissing] = useState(false);
  const [running, setRunning] = useState(false);
  const [activity, setActivity] = useState("");
  const [input, setInput] = useState("");
  const [error, setError] = useState("");
  const pendingPromptRef = useRef("");
  const bottomRef = useRef<HTMLDivElement>(null);
  const inputRef = useRef<HTMLTextAreaElement>(null);
  const agent = useMemo(() => new HttpAgent({ url: "/api/agent", threadId: threadID, fetch: (url, init) => authorizedFetch(url, init) }), [threadID]);
  const ready = !agentAvailable || loadedThreadID === threadID;

  // Keyed on the thread, not on the route. Naming a draft moves the same
  // conversation from /chat to /chat/<id>; restarting the session there would
  // abort the run that send() has just started and clear its first message.
  // Every other route change also changes threadID, so nothing is missed.
  useEffect(() => {
    let active = true;
    setMessages([]);
    setMessageTimes({});
    setLoadedThreadID("");
    setThreadMissing(false);
    setRunning(false);
    setActivity("");
    setError("");
    if (!agentAvailable) { setLoadedThreadID(threadID); return; }
    const isDraft = !routeThreadID || draftsRef.current.has(threadID);
    if (isDraft) {
      agent.setMessages([]);
      setLoadedThreadID(threadID);
    } else {
      authorizedFetch(`/api/agent/threads/${encodeURIComponent(threadID)}`).then(async (response) => {
        if (response.status === 404) return null;
        if (!response.ok) throw new Error(`Unable to load thread (${response.status})`);
        return response.json() as Promise<{ messages?: Message[] }>;
      }).then((thread) => {
        if (!active) return;
        if (thread === null) { setThreadMissing(true); setLoadedThreadID(threadID); return; }
        agent.setMessages(thread.messages ?? []);
        setMessages([...(thread.messages ?? [])]);
        setLoadedThreadID(threadID);
      }).catch(() => {
        if (!active) return;
        pendingPromptRef.current = "";
        setError("This chat could not be restored. Start a new chat or try again.");
      });
    }
    const subscription = agent.subscribe({
      onEvent: ({ messages: next }) => setMessages([...next] as Message[]),
      onRunInitialized: () => { setRunning(true); setError(""); },
      onToolCallStartEvent: ({ event }: { event: { toolCallName: string } }) => setActivity(activityLabel(event.toolCallName)),
      onToolCallEndEvent: () => setActivity(""),
      onRunFinalized: ({ messages: next }) => {
        const finished = [...next] as Message[];
        setMessages(finished);
        setMessageTimes((times) => {
          const stamped = { ...times };
          for (const message of finished) if (message.role === "assistant" && !stamped[message.id]) stamped[message.id] = Date.now();
          return stamped;
        });
        setRunning(false);
        setActivity("");
        draftsRef.current.delete(threadID);
        void queryClient.invalidateQueries({ queryKey: threadHistoryQueryKey });
      },
      onRunFailed: (failure) => {
        console.error("Agent run failed", failure);
        setError("Fanout could not complete this analysis.");
        setRunning(false);
        setActivity("");
        void queryClient.invalidateQueries({ queryKey: threadHistoryQueryKey });
      },
    });
    return () => { active = false; subscription.unsubscribe(); agent.abortRun(); };
  }, [agent, agentAvailable, queryClient, threadID]);

  useEffect(() => { bottomRef.current?.scrollIntoView({ behavior: "smooth", block: "end" }); }, [messages, running]);

  useEffect(() => {
    if (!agentAvailable) return;
    const shortcuts = (event: KeyboardEvent) => {
      const target = event.target as HTMLElement | null;
      const editing = target?.matches("input, textarea, [contenteditable='true']");
      if (event.key === "/" && !editing) {
        event.preventDefault();
        if (routeThreadID) requestAnimationFrame(() => inputRef.current?.focus());
        else void navigate({ to: "/chat" }).then(() => requestAnimationFrame(() => inputRef.current?.focus()));
      }
      if (event.key === "Escape" && target === inputRef.current) { setInput(""); inputRef.current?.blur(); }
    };
    window.addEventListener("keydown", shortcuts);
    return () => window.removeEventListener("keydown", shortcuts);
  }, [agentAvailable, navigate, routeThreadID]);

  async function run() {
    setRunning(true);
    setError("");
    try { await agent.runAgent(); } catch (cause) {
      console.error("Agent run failed", cause);
      setError("Fanout could not complete this analysis.");
      setRunning(false);
      setActivity("");
    }
  }

  async function send(text: string) {
    const content = text.trim();
    if (!agentAvailable || !content || running || !ready || threadMissing) return;
    if (!routeThreadID) {
      draftsRef.current.add(threadID);
      void navigate({ to: "/chat/$threadId", params: { threadId: threadID }, replace: true });
    }
    const message = { id: createID(), role: "user", content } as Message;
    agent.addMessage(message);
    setMessages([...agent.messages]);
    setMessageTimes((times) => ({ ...times, [message.id]: Date.now() }));
    setInput("");
    await run();
  }

  useEffect(() => {
    const prompt = pendingPromptRef.current;
    if (!ready || !routeThreadID || !prompt) return;
    pendingPromptRef.current = "";
    void send(prompt);
  }, [ready, routeThreadID, threadID]);

  function submit(event: FormEvent) { event.preventDefault(); void send(input); }
  function stop() { agent.abortRun(); setRunning(false); setActivity(""); }
  function retry() { if (!running && agent.messages.some((message) => message.role === "user")) void run(); }
  function openChat(prompt?: string) {
    if (!agentAvailable) return;
    const nextThreadID = createID();
    draftsRef.current.add(nextThreadID);
    pendingPromptRef.current = prompt ?? "";
    void navigate({ to: "/chat/$threadId", params: { threadId: nextThreadID } });
  }
  function newThread() {
    agent.abortRun();
    pendingPromptRef.current = "";
    setDraftID(createID());
    void navigate({ to: "/chat" });
  }
  function selectThread(selectedThreadID: string) {
    void navigate({ to: "/chat/$threadId", params: { threadId: selectedThreadID } });
  }

  return <FanoutAppContext.Provider value={{ agentAvailable, threadID, threadMissing, messages, messageTimes, ready, running, activity, input, setInput, error, bottomRef, inputRef, send, submit, stop, retry, openChat, newThread, selectThread }}>
    <Shell><Outlet /></Shell>
  </FanoutAppContext.Provider>;
}

export default function App() {
  const queryClient = useMemo(() => new QueryClient(), []);
  return <QueryClientProvider client={queryClient}><AuthGate><Session /></AuthGate></QueryClientProvider>;
}
