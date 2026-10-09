import { MantineProvider } from "@mantine/core";
import type { Message } from "@ag-ui/client";
import { createMemoryHistory, createRootRoute, createRoute, createRouter, RouterProvider } from "@tanstack/react-router";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, expect, it, vi } from "vitest";
import App from "./App";
import { useFanoutApp } from "./app-context";

vi.mock("./auth", () => ({
  default: ({ children }: { children: React.ReactNode }) => children,
  useRuntimeStatus: () => ({ agent_available: true, setup_required: false, auth_mode: "local" }),
  useViewer: () => ({ id: "viewer", email: "viewer@example.test", display_name: "Viewer", status: "active" as const, role: "viewer" }),
  authorizedFetch: (input: RequestInfo | URL, init?: RequestInit) => fetch(input, init),
  logout: vi.fn(), clearSession: vi.fn(),
}));

function Controls() {
  const app = useFanoutApp();
  return <div>
    <button disabled={!app.ready || app.running} onClick={() => app.openChat("Explain observed latency", { answer_only: true })}>Explain observed panel</button>
    <button disabled={!app.ready || app.running} onClick={app.retry}>Retry observed turn</button>
    <button disabled={!app.ready || app.running} onClick={() => void app.send("Please fix the dashboard")}>Ordinary edit</button>
    <button disabled={!app.ready || app.running} onClick={app.newThread}>New ordinary thread</button>
  </div>;
}
afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ""; });
it("carries answer-only through openChat and HttpAgent, retains it on retry and clears it on ordinary sends and new threads", async () => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true);
  const requests: { threadId: string; runId: string; messages: Message[]; forwardedProps: { answer_only?: boolean } }[] = [];
  vi.stubGlobal("fetch", vi.fn(async (input: RequestInfo | URL, init?: RequestInit) => {
    const path = new URL(String(input), "https://fanout.example.test").pathname;
    if (path === "/api/agent/runs") {
      const body = JSON.parse(String(init?.body)); requests.push(body);
      const events = [{ type: "RUN_STARTED", threadId: body.threadId, runId: body.runId }, { type: "RUN_FINISHED", threadId: body.threadId, runId: body.runId }];
      return new Response(events.map(event => `data: ${JSON.stringify(event)}\n\n`).join(""), { headers: { "Content-Type": "text/event-stream" } });
    }
    return Response.json(path === "/api/dashboards" ? { dashboards: [] } : { messages: [], items: [], next_cursor: null });
  }));
  const rootRoute = createRootRoute({ component: App });
  const chat = createRoute({ getParentRoute: () => rootRoute, path: "/chat/$threadId", component: Controls });
  const draft = createRoute({ getParentRoute: () => rootRoute, path: "/chat", component: Controls });
  const dashboard = createRoute({ getParentRoute: () => rootRoute, path: "/dashboards/$dashboardId", component: Controls });
  const history = createMemoryHistory({ initialEntries: ["/dashboards/board"] });
  const router = createRouter({ routeTree: rootRoute.addChildren([chat, draft, dashboard]), history });
  const host = document.createElement("div"); document.body.append(host); const root = createRoot(host);
  const press = async (label: string) => {
    const button = await vi.waitFor(() => { const button = [...host.querySelectorAll<HTMLButtonElement>("button")].find(button => button.textContent === label)!; expect(button?.disabled).toBe(false); return button; });
    await act(async () => button.click());
  };
  try {
    await act(async () => root.render(<MantineProvider><RouterProvider router={router} /></MantineProvider>));
    await press("Explain observed panel");
    await vi.waitFor(() => expect(requests).toHaveLength(1));
    expect(requests[0].forwardedProps).toEqual({ answer_only: true });
    expect(requests[0].messages.at(-1)?.content).toBe("Explain observed latency");
    await press("Retry observed turn");
    await vi.waitFor(() => expect(requests).toHaveLength(2));
    expect(requests[1].forwardedProps).toEqual({ answer_only: true });
    expect(requests[1].messages.filter(message => message.role === "user")).toHaveLength(1);
    await press("Ordinary edit");
    await vi.waitFor(() => expect(requests).toHaveLength(3));
    expect(requests[2].forwardedProps.answer_only).toBeUndefined();
    expect(requests[2].messages.at(-1)?.content).toBe("Please fix the dashboard");
    await press("Explain observed panel");
    await vi.waitFor(() => expect(requests).toHaveLength(4));
    expect(requests[3].forwardedProps).toEqual({ answer_only: true });
    await press("New ordinary thread");
    await press("Ordinary edit");
    await vi.waitFor(() => expect(requests).toHaveLength(5));
    expect(requests[4].forwardedProps.answer_only).toBeUndefined();
    expect(requests[4].threadId).not.toBe(requests[3].threadId);
    expect(requests[4].messages.filter(message => message.role === "user")).toHaveLength(1);
  } finally { await act(async () => root.unmount()); }
});
