import { HttpAgent, type Message } from "@ag-ui/client";
import { QueryClient, QueryClientProvider, useQueryClient } from "@tanstack/react-query";
import { Outlet, useNavigate, useParams } from "@tanstack/react-router";
import { FormEvent, useEffect, useMemo, useRef, useState } from "react";
import { threadHistoryQueryKey } from "./api";
import { activityLabel, FanoutAppContext, runErrorMessage, type TurnOptions } from "./app-context";
import AuthGate, { authorizedFetch, useRuntimeStatus } from "./auth";
import { createID } from "./id";
import { dashboardsKey } from "./dashboards/api";
import { dashboardToolResult } from "./dashboard-tool-result";
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
  const [stopped, setStopped] = useState(false);
  const stoppingRef = useRef(false);
  const [input, setInput] = useState("");
  const [error, setError] = useState("");
  // Bumped by the Retry button on a thread that failed to load. It is a
  // dependency of the session effect, so a bump asks the server again.
  const [reloadCount, setReloadCount] = useState(0);
  const pendingPromptRef = useRef<{ text: string; options?: TurnOptions } | null>(null);
  const turnOptionsRef = useRef<TurnOptions>({});
  const inputRef = useRef<HTMLTextAreaElement>(null);
  // A turn can hold several tool calls at once, and each one ends separately.
  // Tracking them keeps the activity line on the work still in flight instead
  // of blanking it the moment the first call returns.
  const toolCallsRef = useRef(new Map<string, string>());
  const agent = useMemo(() => new HttpAgent({ url: "/api/agent/runs", threadId: threadID, fetch: (url, init) => authorizedFetch(url, init) }), [threadID]);
  const ready = !agentAvailable || loadedThreadID === threadID;

  function clearActivity() { toolCallsRef.current.clear(); setActivity(""); }
  function isAbort(cause: unknown) { return stoppingRef.current || cause instanceof Error && cause.name === "AbortError"; }

  // Keyed on the thread, not on the route. Naming a draft moves the same
  // conversation from /chat to /chat/<id>; restarting the session there would
  // abort the run that send() has just started and clear its first message.
  // Every other route change also changes threadID, so nothing is missed.
  useEffect(() => {
    let active = true;
    setMessages([]);
    turnOptionsRef.current = {};
    setMessageTimes({});
    setLoadedThreadID("");
    setThreadMissing(false);
    setRunning(false);
    stoppingRef.current = false;
    setStopped(false);
    clearActivity();
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
        setMessages(thread.messages ?? []);
        setLoadedThreadID(threadID);
      }).catch(() => {
        if (!active) return;
        pendingPromptRef.current = null;
        setError("This chat could not be restored. Start a new chat or try again.");
      });
    }
    const subscription = agent.subscribe({
      onMessagesChanged: ({ messages: next }) => setMessages([...next]),
      onRunInitialized: () => { stoppingRef.current = false; setStopped(false); setRunning(true); setError(""); },
      onTextMessageContentEvent: ({ event }) => { if (event.delta) setActivity(""); },
      onToolCallStartEvent: ({ event }) => {
        toolCallsRef.current.set(event.toolCallId, activityLabel(event.toolCallName));
        setActivity(activityLabel(event.toolCallName));
      },
      onToolCallResultEvent: ({ event, messages: next }) => {
        toolCallsRef.current.delete(event.toolCallId);
        setActivity([...toolCallsRef.current.values()].at(-1) ?? "Analyzing your system");
        // Only a committed server receipt may refresh the dashboard/rail cache.
        const saved = dashboardToolResult(event.toolCallId, event.content, next);
        if (!saved) return;
        void queryClient.invalidateQueries({ queryKey: dashboardsKey });
        void queryClient.invalidateQueries({ queryKey: ["dashboard", saved.id] });
        void queryClient.invalidateQueries({ queryKey: ["dashboard-versions", saved.id] });
      },
      onRunErrorEvent: ({ event }) => {
        if (event.code === "abort" || stoppingRef.current) {setError("");setStopped(true);}
        else {console.warn("Agent run error", event.code);setError(runErrorMessage(event.code));}
        setRunning(false);
        clearActivity();
      },
      onRunFinalized: ({ messages: next }) => {
        const finished = [...next];
        setMessages(finished);
        setMessageTimes((times) => {
          const stamped = { ...times };
          for (const message of finished) if (message.role === "assistant" && !stamped[message.id]) stamped[message.id] = Date.now();
          return stamped;
        });
        setRunning(false);
        clearActivity();
        draftsRef.current.delete(threadID);
        void queryClient.invalidateQueries({ queryKey: threadHistoryQueryKey });
      },
      onRunFailed: (failure) => {
        if (isAbort(failure.error)) {setError("");setStopped(true);}
        else {console.error("Agent run failed", failure);setError(runErrorMessage());}
        setRunning(false);
        clearActivity();
        // A run that did not finish may have persisted nothing, so stop
        // treating this id as a draft: coming back to it must ask the server
        // rather than open an empty pane over a thread that does exist.
        draftsRef.current.delete(threadID);
        void queryClient.invalidateQueries({ queryKey: threadHistoryQueryKey });
      },
    });
    return () => { active = false; subscription.unsubscribe(); agent.abortRun(); };
  }, [agent, agentAvailable, queryClient, reloadCount, threadID]);

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
    try {
      const { context = [], ...forwardedProps } = turnOptionsRef.current;
      await agent.runAgent({ context, forwardedProps });
    } catch (cause) {
      if (isAbort(cause)) {setError("");setStopped(true);}
      else {console.error("Agent run failed", cause);setError(runErrorMessage());}
      setRunning(false);
      clearActivity();
      draftsRef.current.delete(threadID);
    }
  }

  async function send(text: string, options?: TurnOptions) {
    const content = text.trim();
    if (!agentAvailable || !content || running || !ready || threadMissing) return;
    turnOptionsRef.current = { ...options };
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
    pendingPromptRef.current = null;
    void send(prompt.text, prompt.options);
  }, [ready, routeThreadID, threadID]);

  function submit(event: FormEvent) { event.preventDefault(); void send(input); }
  function stop() { stoppingRef.current = true;setError("");setStopped(true);agent.abortRun(); setRunning(false); clearActivity(); draftsRef.current.delete(threadID); }
  function retry() { if (!running && agent.messages.some((message) => message.role === "user")) void run(); }
  function openChat(prompt?: string, options?: TurnOptions) {
    if (!agentAvailable) return;
    const nextThreadID = createID();
    draftsRef.current.add(nextThreadID);
    pendingPromptRef.current = prompt ? { text: prompt, options: { ...options } } : null;
    void navigate({ to: "/chat/$threadId", params: { threadId: nextThreadID } });
  }
  function newThread() {
    agent.abortRun();
    pendingPromptRef.current = null;
    turnOptionsRef.current = {};
    setDraftID(createID());
    void navigate({ to: "/chat" });
  }
  function selectThread(selectedThreadID: string) {
    pendingPromptRef.current = null;
    turnOptionsRef.current = {};
    void navigate({ to: "/chat/$threadId", params: { threadId: selectedThreadID } });
  }
  function reloadThread() { setReloadCount((n) => n + 1); }

  return <FanoutAppContext.Provider value={{ agentAvailable, threadID, threadMissing, messages, messageTimes, ready, running, activity, stopped, input, setInput, error, inputRef, send, submit, stop, retry, reloadThread, openChat, newThread, selectThread }}>
    <Shell><Outlet /></Shell>
  </FanoutAppContext.Provider>;
}

export default function App() {
  const queryClient = useMemo(() => new QueryClient(), []);
  return <QueryClientProvider client={queryClient}><AuthGate><Session /></AuthGate></QueryClientProvider>;
}
