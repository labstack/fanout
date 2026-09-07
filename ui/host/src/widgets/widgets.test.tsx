import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, describe, expect, it, vi } from "vitest";

vi.mock("../echart", () => ({
  EChart: ({ label }: { label: string }) => <div data-chart={label} />,
  useECharts: () => undefined,
}));

import WidgetCard, { type Widget } from "./widget-card";

const fetchMock = vi.fn<typeof fetch>();
const filters = { window: "1h", namespace: "" };
const provenance = { query_id: "q", window: "2026-09-05T17:00:00Z/2026-09-05T18:00:00Z", generated_at: "2026-09-05T18:00:00Z", complete: true, data_source: "test" };
const services = [
  { service: "payments", health: "unhealthy", spans: 1813, error_rate: 0.13, p50_ms: 169, p95_ms: 729.9, log_count: 326, metric_count: 0 },
  { service: "orders", health: "healthy", spans: 2109, error_rate: 0.009, p50_ms: 80, p95_ms: 650.4, log_count: 20, metric_count: 0 },
];
const points = Array.from({ length: 6 }, (_, index) => ({ time: `2026-09-05T17:${String(index * 10).padStart(2, "0")}:00Z`, spans: 100 + index, error_rate: 0.01, p50_ms: 40, p95_ms: 120 + index, log_count: 3, metric_count: 0 }));

function json(body: unknown, status = 200) {
  return new Response(JSON.stringify(body), { status, headers: { "content-type": "application/json" } });
}

function respond(input: RequestInfo | URL) {
  const url = new URL(String(input), "http://localhost");
  const data = {
    "/api/observability/overview": { health: "unhealthy", counts: { healthy: 1, degraded: 0, unhealthy: 1 }, total_spans: 12221, error_rate: 0.0216, services, service_count: 2 },
    "/api/observability/topology": { nodes: services, edges: [{ caller: "orders", callee: "payments", type: "http", calls: 1700, average_ms: 140, error_rate: 0.13 }] },
    "/api/observability/performance": { points, endpoints: [{ method: "POST", path: "/charge", calls: 1802, p50_ms: 140, p95_ms: 500, p99_ms: 900, error_rate: 0.13, health: "unhealthy" }], heatmap: [], comparison: [] },
    "/api/observability/logs": { entries: [{ time: "2026-09-05T17:59:00Z", severity: "ERROR", service: "payments", body: "POST /charge failed", trace_id: "abc" }], buckets: [{ time: "2026-09-05T17:50:00Z", severity: "ERROR", count: 3 }] },
    "/api/observability/trace": { trace_id: "78bf7f484abf8a4c726d2d24359ec5e4", duration_ms: 994.6, has_error: true, services: ["gateway", "orders"], spans: [{ span_id: "s1", service: "gateway", operation: "GET /api/orders", kind: "server", start: "2026-09-05T17:59:00.000Z", duration_ms: 994.6, status: "ERROR" }, { span_id: "s2", parent_span_id: "s1", service: "orders", operation: "GET /orders/{id}", kind: "server", start: "2026-09-05T17:59:00.010Z", duration_ms: 600, status: "OK" }], logs: [] },
  } as Record<string, unknown>;
  const body = data[url.pathname];
  if (!body) throw new Error(`unexpected request: ${url.pathname}`);
  return json({ schema: "test", summary: "", data: body, provenance });
}

async function mount(widget: Widget, handlers: { onRemove?: () => void; onConfigure?: (config: Record<string, unknown>) => void; onOpenChat?: (prompt?: string) => void } = {}) {
  const container = document.createElement("div");
  document.body.append(container);
  const root = createRoot(container);
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } });
  await act(async () => root.render(
    <QueryClientProvider client={client}><MantineProvider>
      <WidgetCard widget={widget} filters={filters} dark={false} services={["payments", "orders"]} agentAvailable onOpenChat={handlers.onOpenChat ?? (() => undefined)} onRemove={handlers.onRemove ?? (() => undefined)} onConfigure={handlers.onConfigure ?? (() => undefined)} />
    </MantineProvider></QueryClientProvider>,
  ));
  return root;
}

function setValue(input: HTMLInputElement, value: string) {
  Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, "value")?.set?.call(input, value);
  input.dispatchEvent(new InputEvent("input", { bubbles: true, data: value, inputType: "insertText" }));
}

describe("widgets", () => {
  beforeEach(() => { vi.stubGlobal("fetch", fetchMock); fetchMock.mockReset(); fetchMock.mockImplementation(async (input) => respond(input)); });
  afterEach(() => { vi.unstubAllGlobals(); document.body.innerHTML = ""; });

  it("overview shows health, an error-rate sparkline and the distribution", async () => {
    const root = await mount({ id: "w1", type: "overview", title: "System health", enabled: true });
    await vi.waitFor(() => expect(document.body.textContent).toContain("Unhealthy"));
    expect(document.body.textContent).toContain("2.16%");
    expect(document.querySelector('[data-chart="Error rate trend"]')).not.toBeNull();
    expect(document.body.textContent).toContain("1 healthy");
    expect(document.body.textContent).toContain("1 unhealthy");
    await act(async () => root.unmount());
  });

  it("overview reports an empty window as no data rather than healthy", async () => {
    fetchMock.mockImplementation(async (input) => {
      const url = new URL(String(input), "http://localhost");
      if (url.pathname === "/api/observability/overview") {
        return json({ schema: "test", summary: "no services reported telemetry in this window", provenance, data: { health: "unknown", counts: { healthy: 0, degraded: 0, unhealthy: 0 }, total_spans: 0, error_rate: 0, services: [], service_count: 0 } });
      }
      return respond(input);
    });
    const root = await mount({ id: "w1b", type: "overview", title: "System health", enabled: true });
    await vi.waitFor(() => expect(document.body.textContent).toContain("No data"));
    expect(document.body.textContent).not.toContain("Healthy");
    expect(document.body.textContent).not.toContain("0 healthy");
    expect(document.body.textContent).not.toContain("0.00%");
    await act(async () => root.unmount());
  });

  it("trace scopes its query to the configured service", async () => {
    const root = await mount({ id: "w5b", type: "trace", title: "Slow checkout trace", enabled: true, config: { service: "payments" } });
    await vi.waitFor(() => expect(fetchMock).toHaveBeenCalled());
    const requested = fetchMock.mock.calls.map(([input]) => String(input)).filter((url) => url.includes("/api/observability/trace"));
    expect(requested.length).toBeGreaterThan(0);
    // Without this the widget asks for the worst trace anywhere in the window,
    // which is rarely the service the widget is named after.
    expect(requested.every((url) => new URL(url, "http://localhost").searchParams.get("service") === "payments")).toBe(true);
    await act(async () => root.unmount());
  });

  it("topology draws a graph and counts routes", async () => {
    const root = await mount({ id: "w2", type: "topology", title: "Service map", enabled: true });
    await vi.waitFor(() => expect(document.body.textContent).toContain("2 services · 1 route"));
    expect(document.querySelector('[data-chart="Service dependency graph"]')).not.toBeNull();
    await act(async () => root.unmount());
  });

  it("trace formats durations in lower-case units", async () => {
    const root = await mount({ id: "w3", type: "trace", title: "Trace focus", enabled: true });
    await vi.waitFor(() => expect(document.body.textContent).toContain("995ms"));
    expect(document.body.textContent).not.toContain("Ms");
    expect(document.body.textContent).toContain("78bf7f48…c5e4");
    expect(document.body.textContent).toContain("GET /api/orders");
    await act(async () => root.unmount());
  });

  it("logs show a histogram, timestamps and a severity badge that holds its width", async () => {
    const root = await mount({ id: "w4", type: "logs", title: "Logs", enabled: true });
    await vi.waitFor(() => expect(document.body.textContent).toContain("POST /charge failed"));
    expect(document.querySelector('[data-chart="Log volume by severity"]')).not.toBeNull();
    expect(document.body.textContent).toMatch(/\d{1,2}:59/);
    // A Badge is an inline-grid with hidden overflow, so a squeezed row
    // collapses its track and ERROR measures zero. happy-dom does not lay out,
    // so the guard is on the declaration the browser needs.
    const severity = Array.from(document.querySelectorAll<HTMLElement>(".mantine-Badge-root")).find((badge) => badge.textContent === "ERROR");
    expect(severity).not.toBeUndefined();
    expect(severity?.style.minWidth).toBe("max-content");
    await act(async () => root.unmount());
  });

  it("performance charts the trend and lists the top endpoints", async () => {
    const root = await mount({ id: "w5", type: "performance", title: "Performance", enabled: true });
    await vi.waitFor(() => expect(document.body.textContent).toContain("/charge"));
    expect(document.querySelector('[data-chart="Operations and P95 latency"]')).not.toBeNull();
    await act(async () => root.unmount());
  });

  it("assistant offers window-scoped questions", async () => {
    const openChat = vi.fn();
    const root = await mount({ id: "w6", type: "assistant", title: "Ask Fanout", enabled: true }, { onOpenChat: openChat });
    const question = Array.from(document.querySelectorAll<HTMLButtonElement>("button")).find((button) => button.textContent?.includes("Summarize the last 1 hour"));
    expect(question).not.toBeUndefined();
    await act(async () => question?.click());
    expect(openChat).toHaveBeenCalledWith("Summarize the last 1 hour");
    expect(fetchMock).not.toHaveBeenCalled();
    await act(async () => root.unmount());
  });

  // A dashboard saved by a newer build can name a view this one has never
  // heard of. That card says so; it does not take the page down. `type` is a
  // plain string on Widget (the server, not this build, is the source of
  // truth for it), so "mystery" needs no cast to reach the component.
  it("says so when a widget type is unknown", async () => {
    const root = await mount({ id: "w8", type: "mystery", title: "Mystery", enabled: true });
    expect(document.body.textContent).toContain("This view type is not supported by this version");
    expect(document.body.textContent).toContain("Mystery");
    expect(fetchMock).not.toHaveBeenCalled();
    await act(async () => root.unmount());
  });

  it("falls back only for the unknown type — a known type still renders its own body", async () => {
    const unknown = await mount({ id: "w8a", type: "not-a-real-widget", title: "Mystery", enabled: true });
    expect(document.body.textContent).toContain("This view type is not supported by this version");
    await act(async () => unknown.unmount());

    const known = await mount({ id: "w8b", type: "overview", title: "System health", enabled: true });
    await vi.waitFor(() => expect(document.body.textContent).toContain("Unhealthy"));
    expect(document.body.textContent).not.toContain("This view type is not supported by this version");
    await act(async () => known.unmount());
  });

  it("removes through the actions menu and saves configuration", async () => {
    const onRemove = vi.fn();
    const onConfigure = vi.fn();
    const root = await mount({ id: "w7", type: "logs", title: "Logs", enabled: true }, { onRemove, onConfigure });
    await vi.waitFor(() => expect(document.body.textContent).toContain("POST /charge failed"));
    expect(document.querySelector('button[aria-label="Remove Logs"]')).toBeNull();
    const actions = document.querySelector('button[aria-label="Actions for Logs"]') as HTMLButtonElement;
    await act(async () => actions.click());
    const configure = Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).find((item) => item.textContent?.includes("Configure"));
    await act(async () => configure?.click());
    const search = document.querySelector('[role="dialog"] input[aria-label="Search"]') as HTMLInputElement;
    await act(async () => setValue(search, "timeout"));
    const save = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')).find((button) => button.textContent?.trim() === "Save");
    await act(async () => save?.click());
    expect(onConfigure).toHaveBeenCalledWith(expect.objectContaining({ search: "timeout" }));

    await act(async () => actions.click());
    const remove = Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).find((item) => item.textContent?.includes("Remove"));
    await act(async () => remove?.click());
    // The menu item asks; it does not remove. A dashboard has no undo.
    expect(onRemove).not.toHaveBeenCalled();
    const confirm = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')).find((button) => button.textContent?.trim() === "Remove");
    await act(async () => confirm?.click());
    expect(onRemove).toHaveBeenCalled();
    await act(async () => root.unmount());
  });

  it("keeps the widget when the removal is cancelled", async () => {
    const onRemove = vi.fn();
    const root = await mount({ id: "w7b", type: "logs", title: "Logs", enabled: true }, { onRemove });
    await vi.waitFor(() => expect(document.body.textContent).toContain("POST /charge failed"));
    const actions = document.querySelector('button[aria-label="Actions for Logs"]') as HTMLButtonElement;
    await act(async () => actions.click());
    const remove = Array.from(document.querySelectorAll<HTMLElement>('[role="menuitem"]')).find((item) => item.textContent?.includes("Remove"));
    await act(async () => remove?.click());
    const cancel = Array.from(document.querySelectorAll<HTMLButtonElement>('[role="dialog"] button')).find((button) => button.textContent?.trim() === "Cancel");
    await act(async () => cancel?.click());
    expect(onRemove).not.toHaveBeenCalled();
    await act(async () => root.unmount());
  });

  it("recent activity rows lead to an investigation", async () => {
    const onOpenChat = vi.fn();
    const root = await mount({ id: "w9", type: "activity", title: "Recent activity", enabled: true }, { onOpenChat });
    await vi.waitFor(() => expect(document.body.textContent).toContain("payments"));
    const row = document.querySelector('[aria-label="Investigate payments"]') as HTMLElement;
    expect(row).not.toBeNull();
    await act(async () => row.click());
    expect(onOpenChat).toHaveBeenCalledWith(expect.stringContaining("payments"));
    await act(async () => root.unmount());
  });
});
