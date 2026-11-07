import { afterEach, expect, it, vi } from "vitest";
import { dashboardQuestion, question, savedMessages, userMessage } from "./fixtures";
import { makeService, type CatalogService } from "./requests";

const active: CatalogService[] = [];
afterEach(() => { active.splice(0).forEach(service => service.dispose()); vi.useRealTimers(); });
const service = (scenario: Parameters<typeof makeService>[0]) => { const next = makeService(scenario); active.push(next); return next; };
const get = (api: CatalogService, path: string) => api.handle(path).then(response => response.json());
const run = (api: CatalogService, prompt: string) => api.handle("/api/agent/runs", { method: "POST", body: JSON.stringify({ threadId: "new-thread", runId: "run-001", messages: [{ ...userMessage, content: prompt }] }) });

it("persists a new thread, saved dashboard provenance and transcript within one story only", async () => {
  vi.useFakeTimers();
  const api = service({ empty: true, run: "dashboard" });
  const other = service({ empty: true });
  const response = await run(api, dashboardQuestion);
  expect((await get(api, "/api/agent/threads")).items).toEqual([expect.objectContaining({ id: "new-thread", title: `${dashboardQuestion.slice(0, 72).trim()}…` })]);
  expect((await get(api, "/api/dashboards")).dashboards).toEqual([]);
  const stream = response.text();
  await vi.runAllTimersAsync();
  await stream;
  expect((await get(api, "/api/dashboards")).dashboards).toEqual([expect.objectContaining({ origin: { thread_id: "new-thread", message_id: userMessage.id, request_excerpt: dashboardQuestion } })]);
  expect((await get(api, "/api/agent/threads/new-thread")).messages).toEqual([{ ...userMessage, content: dashboardQuestion }, ...savedMessages.slice(1)]);
  expect((await get(api, "/api/agent/threads?q=missing")).items).toEqual([]);
  expect((await get(other, "/api/agent/threads")).items).toEqual([]);
  expect((await get(other, "/api/dashboards")).dashboards).toEqual([]);
});

it.each(["answered", "saved"] as const)("seeds thread and dashboard lists for the %s history independently", async chat => {
  const api = service({ empty: true, chat });
  expect((await get(api, "/api/agent/threads")).items).toEqual([expect.objectContaining({ id: "commerce-review" })]);
  expect((await get(api, "/api/dashboards")).dashboards).toHaveLength(1);
  expect((await get(api, "/api/agent/threads/commerce-review")).messages[0].content).toBe(chat === "saved" ? dashboardQuestion : question);
});

it("paces recording tool work at 1–2 seconds and streams text at a readable rate", async () => {
  vi.useFakeTimers();
  const api = service({ empty: true, run: "question", pace: "recording" });
  const response = await run(api, question);
  const reader = response.body!.getReader();
  const events: { type: string; delta?: string; at: number }[] = [];
  const consume = async () => {
    for (;;) {
      const next = await reader.read();
      if (next.done) break;
      events.push({ ...JSON.parse(new TextDecoder().decode(next.value).slice(6)), at: Date.now() });
    }
  };
  const finished = consume();
  await vi.runAllTimersAsync();
  await finished;
  const work = events.find(event => event.type === "TOOL_CALL_RESULT")!.at - events.find(event => event.type === "TOOL_CALL_START")!.at;
  expect(work).toBeGreaterThanOrEqual(1000);
  expect(work).toBeLessThanOrEqual(2000);
  const chunks = events.filter(event => event.type === "TEXT_MESSAGE_CONTENT");
  const rate = chunks.slice(1).reduce((sum, event) => sum + event.delta!.length, 0) / ((chunks.at(-1)!.at - chunks[0].at) / 1000);
  expect(rate).toBeGreaterThanOrEqual(20);
  expect(rate).toBeLessThanOrEqual(40);
});
