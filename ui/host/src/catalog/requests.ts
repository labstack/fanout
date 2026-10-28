import type { Message } from "@ag-ui/client";
import type { Status, Viewer } from "../auth";
import type { DashboardSummary } from "../dashboards/api";
import { answer, answeredMessages, dashboard, dashboardSummary, health, healthContent, results, savedMessages, services } from "./fixtures";

export type Scenario = {
  signedOut?: boolean;
  setup?: boolean;
  empty?: boolean;
  dashboard?: "loading" | "empty" | "error";
  chat?: "answered" | "saved" | "error";
  run?: "question" | "dashboard" | "running" | "error";
  pace?: "fast" | "recording";
};
export type CatalogService = ReturnType<typeof makeService>;
type Event = Record<string, unknown>;
const viewer: Viewer = { id: "alex-rivera", email: "alex@example.test", display_name: "Alex Rivera", status: "active", role: "admin" };
const runtime: Status = { setup_required: false, auth_mode: "local", agent_available: true, smtp_configured: true, self_signup: true };
type Thread = { id: string; title: string; updated_at: string; messages: Message[] };
const threadTitle = (messages: Message[]) => {
  const first = messages.find(message => message.role === "user");
  const title = String(first?.content ?? "").trim().replace(/\s+/g, " ");
  const letters = Array.from(title);
  return letters.length > 72 ? `${letters.slice(0, 72).join("").trim()}…` : title || "Untitled investigation";
};

export function makeService(scenario: Scenario = {}, staticFetch: typeof fetch = fetch) {
  const unexpected: string[] = [];
  const requests: string[] = [];
  const timers = new Set<ReturnType<typeof setTimeout>>();
  const streams = new Set<ReadableStreamDefaultController<Uint8Array>>();
  let disposed = false;
  const fail = (message: string): never => { unexpected.push(message); throw new Error(message); };
  const json = (value: unknown, status = 200) => Response.json(value, { status });
  const history = scenario.chat === "answered" ? answeredMessages : scenario.chat === "saved" ? savedMessages : [];
  const threads = new Map<string, Thread>();
  if (!scenario.empty || history.length) threads.set("commerce-review", { id: "commerce-review", title: history.length ? threadTitle(history) : "Commerce health", updated_at: dashboard.updated_at, messages: structuredClone(history) });
  const origin = (threadId: string, messages: Message[]) => {
    const question = messages.filter(message => message.role === "user").at(-1);
    return { thread_id: threadId, message_id: question?.id ?? "question-001", request_excerpt: String(question?.content ?? "") };
  };
  let saved: DashboardSummary | undefined = !scenario.empty || history.length
    ? { ...dashboardSummary, ...(scenario.chat === "saved" && { origin: origin("commerce-review", history) }) }
    : undefined;
  const delay = (callback: () => void, ms: number) => {
    const timer = setTimeout(() => { timers.delete(timer); if (!disposed) callback(); }, ms);
    timers.add(timer);
  };

  function stream(body: { threadId: string; runId: string; messages: Message[] }, signal?: AbortSignal | null) {
    const messages = structuredClone(body.messages);
    const thread: Thread = { id: body.threadId, title: threadTitle(messages), updated_at: dashboard.updated_at, messages };
    threads.set(body.threadId, thread);
    const events: Event[] = [{ type: "RUN_STARTED", threadId: body.threadId, runId: body.runId }];
    const tool = (id: string, name: string, input: unknown, output: unknown) => events.push(
      { type: "TOOL_CALL_START", toolCallId: id, toolCallName: name, parentMessageId: `assistant-${id}` },
      { type: "TOOL_CALL_ARGS", toolCallId: id, delta: JSON.stringify(input) },
      { type: "TOOL_CALL_END", toolCallId: id },
      { type: "TOOL_CALL_RESULT", messageId: `result-${id}`, toolCallId: id, content: JSON.stringify(output), role: "tool" },
    );
    if (scenario.run === "error") events.push({ type: "RUN_ERROR", code: "provider_unavailable", message: "Synthetic provider unavailable" });
    else if (scenario.run === "dashboard") {
      for (let i = 1; i < savedMessages.length - 1; i += 2) {
        const call = savedMessages[i] as Extract<Message, { role: "assistant" }>;
        const result = savedMessages[i + 1] as Extract<Message, { role: "tool" }>;
        const item = call.toolCalls![0];
        tool(item.id, item.function.name, JSON.parse(item.function.arguments), JSON.parse(String(result.content)));
      }
    } else {
      events.push({ type: "TOOL_CALL_START", toolCallId: "overview", toolCallName: "get_observability_overview", parentMessageId: "assistant-overview" });
      if (scenario.run !== "running") events.push(
        { type: "TOOL_CALL_ARGS", toolCallId: "overview", delta: '{"window":"1h"}' },
        { type: "TOOL_CALL_END", toolCallId: "overview" },
        { type: "TOOL_CALL_RESULT", toolCallId: "overview", messageId: "result-overview", role: "tool", content: JSON.stringify(health) },
        { type: "ACTIVITY_SNAPSHOT", messageId: "health-view-001", activityType: "mcp-app", content: healthContent },
      );
    }
    if (scenario.run !== "running" && scenario.run !== "error") {
      const text = scenario.run === "dashboard" ? String(savedMessages.at(-1)!.content) : answer;
      events.push({ type: "TEXT_MESSAGE_START", messageId: "answer-001", role: "assistant" });
      for (const delta of text.match(scenario.pace === "recording" ? /.{1,12}/gs : /.{1,36}/gs) ?? []) events.push({ type: "TEXT_MESSAGE_CONTENT", messageId: "answer-001", delta });
      events.push({ type: "TEXT_MESSAGE_END", messageId: "answer-001" }, { type: "RUN_FINISHED", threadId: body.threadId, runId: body.runId });
    }
    const encoder = new TextEncoder();
    let closed = false;
    const response = new ReadableStream<Uint8Array>({
      start(controller) {
        streams.add(controller);
        const close = () => { if (closed) return; closed = true; streams.delete(controller); controller.close(); };
        signal?.addEventListener("abort", close, { once: true });
        let elapsed = 0;
        events.forEach((event, index) => {
          elapsed += scenario.pace === "recording"
            ? event.type === "TOOL_CALL_RESULT" ? 1500 : event.type === "TEXT_MESSAGE_CONTENT" ? String(event.delta).length * 32 : 60
            : index === 0 ? 0 : 60;
          delay(() => {
            if (closed) return;
            if (event.type === "TOOL_CALL_RESULT" && event.toolCallId === "save") saved = { ...dashboardSummary, origin: origin(body.threadId, messages) };
            if (event.type === "RUN_FINISHED") thread.messages = [...messages, ...structuredClone(scenario.run === "dashboard" ? savedMessages : answeredMessages).slice(1)];
            controller.enqueue(encoder.encode(`data: ${JSON.stringify(event)}\n\n`));
            if (index === events.length - 1 && scenario.run !== "running") { close(); signal?.removeEventListener("abort", close); }
          }, elapsed);
        });
      },
      cancel() { closed = true; },
    });
    return new Response(response, { headers: { "content-type": "text/event-stream" } });
  }

  const handle: typeof fetch = async (input, init) => {
    const url = new URL(input instanceof Request ? input.url : String(input), location.origin);
    if (url.origin !== location.origin) return fail(`Unexpected origin: ${url.origin}`);
    const method = init?.method ?? (input instanceof Request ? input.method : "GET");
    const path = url.pathname;
    requests.push(`${method} ${path}`);
    const body = init?.body ? JSON.parse(String(init.body)) : undefined;
    if (path === "/api/auth/status" && method === "GET") return json({ ...runtime, setup_required: Boolean(scenario.setup) });
    if (path === "/api/auth/me" && method === "GET") return scenario.signedOut || scenario.setup ? json({}, 401) : json(viewer);
    if (path === "/api/agent/threads" && method === "GET") {
      const query = (url.searchParams.get("q") ?? "").toLowerCase();
      return json({ items: [...threads.values()].filter(thread => thread.title.toLowerCase().includes(query)).map(({ messages: _messages, ...summary }) => summary), next_cursor: null });
    }
    if (path.startsWith("/api/agent/threads/") && method === "GET") {
      const thread = threads.get(decodeURIComponent(path.slice("/api/agent/threads/".length)));
      return scenario.chat === "error" ? json({ message: "Synthetic history unavailable" }, 503) : thread ? json({ messages: thread.messages }) : json({ code: "not_found", message: "Thread not found" }, 404);
    }
    if (path === "/api/agent/runs" && method === "POST") return stream(body, init?.signal);
    if (path === "/api/dashboards" && method === "GET") return json({ dashboards: saved ? [saved] : [] });
    if (path === `/api/dashboards/${dashboard.id}` && method === "GET") {
      if (scenario.dashboard === "loading") return new Promise<Response>((_resolve, reject) => init?.signal?.addEventListener("abort", () => reject(new DOMException("Aborted", "AbortError")), { once: true }));
      if (scenario.dashboard === "error") return json({ code: "not_found", message: "Synthetic dashboard unavailable" }, 404);
      return json(scenario.dashboard === "empty" ? { ...dashboard, spec: { ...dashboard.spec, panels: [] } } : dashboard);
    }
    if (path === "/api/panels/query" && method === "POST") return json({ results: results.filter(result => (!body.panels || body.panels.includes(result.id)) && body.dashboard.panels.some((panel: { id: string }) => panel.id === result.id)) });
    if (path === "/api/annotations" && method === "POST") return json({ deploys: [], anomalies: [] });
    if (path === "/api/settings/ingest" && method === "GET") return json({ token_required: true, suggested_endpoint: "telemetry.example.test:443", tls_configured: true, header_name: "authorization" });
    if (path === "/api/auth/oauth/connections" && method === "GET") return json({ connections: [{ client_id: "local-agent", client_name: "Operations assistant", connected_at: dashboard.created_at }] });
    if (path === "/api/telemetry/schema" && method === "GET") return json({ services: services.map(value => ({ value, count: 12000 })), signals: ["spans", "logs", "metrics"], tables: [] });
    if (path === "/api/mcp") {
      if (method === "GET") return new Response(null, { status: 405 });
      if (method === "DELETE") return new Response(null, { status: 204 });
      if (method !== "POST") return fail(`Unexpected MCP HTTP method: ${method}`);
      if (body.method === "notifications/initialized") return new Response(null, { status: 202 });
      let result: unknown;
      switch (body.method) {
        case "server/discover": result = { supportedVersions: ["2026-07-28"], capabilities: { tools: {}, resources: {} }, ttlMs: 0, cacheScope: "private", _meta: { "io.modelcontextprotocol/serverInfo": { name: "Fanout catalog", version: "1.0.0" } } }; break;
        case "initialize": result = { protocolVersion: "2026-07-28", capabilities: { tools: {}, resources: {} }, serverInfo: { name: "Fanout catalog", version: "1.0.0" } }; break;
        case "tools/list": result = { tools: [], ttlMs: 0, cacheScope: "private" }; break;
        case "resources/read": {
          if (body.params?.uri !== healthContent.resource_uri) return fail(`Unexpected resource: ${body.params?.uri}`);
          const response = await staticFetch("/catalog-apps/panels.html");
          if (!response.ok) return fail("Built panels app is unavailable");
          result = { ttlMs: 0, cacheScope: "private", contents: [{ uri: healthContent.resource_uri, mimeType: "text/html;profile=mcp-app", text: await response.text() }] };
          break;
        }
        default: return fail(`Unexpected MCP request: ${body.method}`);
      }
      return json({ jsonrpc: "2.0", id: body.id, result: { resultType: "complete", ...result as Record<string, unknown> } });
    }
    return fail(`Unexpected request: ${method} ${path}`);
  };
  return {
    handle, unexpected, requests,
    dispose() { disposed = true; for (const timer of timers) clearTimeout(timer); timers.clear(); for (const controller of streams) { try { controller.close(); } catch { /* already cancelled */ } } streams.clear(); },
  };
}
