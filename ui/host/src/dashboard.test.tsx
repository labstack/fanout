import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("./echart", () => ({
  EChart: ({ label }: { label: string }) => <div data-chart={label} />,
  useECharts: () => undefined,
}));

import Dashboard from "./dashboard";

const fetchMock = vi.fn<typeof fetch>();
const provenance = { query_id: "q", window: "2026-09-05T17:00:00Z/2026-09-05T18:00:00Z", generated_at: "2026-09-05T18:00:00Z", complete: true, data_source: "test" };
const record = {
  id: "dash-main", name: "System overview", description: "Live health, dependencies, and recent activity.", is_default: true, updated_at: "2026-09-05 18:00:00",
  state: {
    filters: { window: "1h", namespace: "" },
    widgets: [{ id: "w-health", type: "overview", title: "System health", enabled: true }],
    layout: [{ i: "w-health", x: 0, y: 0, w: 4, h: 4, minW: 3, minH: 4 }],
  },
};

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function respond(input: RequestInfo | URL, init?: RequestInit) {
  const url = new URL(String(input), "http://localhost");
  if (url.pathname === "/api/dashboards") return json({ dashboards: [{ id: record.id, name: record.name, description: record.description, is_default: true, widget_count: 1, updated_at: record.updated_at }] });
  if (url.pathname === `/api/dashboards/${record.id}` && init?.method === "PUT") return json({ ...record, state: JSON.parse(String(init.body)).state });
  if (url.pathname === `/api/dashboards/${record.id}`) return json(record);
  if (url.pathname === "/api/observability/overview") return json({ schema: "t", summary: "", provenance, data: { health: "healthy", counts: { healthy: 1, degraded: 0, unhealthy: 0 }, total_spans: 10, error_rate: 0, services: [{ service: "orders", health: "healthy", spans: 10, error_rate: 0, p50_ms: 1, p95_ms: 2, log_count: 0, metric_count: 0 }], service_count: 1 } });
  if (url.pathname === "/api/observability/performance") return json({ schema: "t", summary: "", provenance, data: { points: [], endpoints: [], heatmap: [], comparison: [] } });
  throw new Error(`unexpected request: ${url.pathname}`);
}

describe("Dashboard", () => {
  beforeEach(() => {
    vi.stubGlobal("fetch", fetchMock);
    fetchMock.mockReset();
    fetchMock.mockImplementation(async (input, init) => respond(input, init));
    localStorage.clear();
  });
  afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ""; });

  it("renders the header without refresh chrome and adds a widget into the free slot", async () => {
    const container = document.createElement("div");
    document.body.append(container);
    const root = createRoot(container);
    const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
    await act(async () => root.render(
      <QueryClientProvider client={client}><MantineProvider>
        <Dashboard dashboardID="dash-main" agentAvailable onOpenChat={() => undefined} />
      </MantineProvider></QueryClientProvider>,
    ));

    await vi.waitFor(() => expect(document.body.textContent).toContain("System overview"));
    await vi.waitFor(() => expect(document.body.textContent).toContain("Healthy"));
    const buttons = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).map((button) => button.textContent?.trim());
    expect(buttons).not.toContain("Refresh");
    expect(buttons).not.toContain("Dashboards");
    expect(document.body.textContent).not.toContain("Saved");
    await vi.waitFor(() => expect(document.body.textContent).toMatch(/Updated \d/));

    const addView = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Add view"));
    await act(async () => addView?.click());
    const topology = Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).find((item) => item.textContent?.includes("Service map"));
    await act(async () => topology?.click());

    await vi.waitFor(() => expect(fetchMock.mock.calls.some(([, init]) => init?.method === "PUT")).toBe(true));
    const put = fetchMock.mock.calls.find(([, init]) => init?.method === "PUT");
    const body = JSON.parse(String(put?.[1]?.body)) as { state: { layout: Array<{ i: string; x: number; y: number; w: number; h: number }>; widgets: Array<{ type: string }> } };
    expect(body.state.widgets.map((widget) => widget.type)).toEqual(["overview", "topology"]);
    const placed = body.state.layout.find((item) => item.i !== "w-health");
    expect(placed).toMatchObject({ x: 4, y: 0, w: 8, h: 5 });
    await act(async () => root.unmount());
  });
});
