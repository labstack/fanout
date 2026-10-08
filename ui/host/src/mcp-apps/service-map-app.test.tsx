import { MantineProvider } from "@mantine/core";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { FragmentView } from "../dashboards/fragment-view";
import { layoutServiceMapRaw } from "../dashboards/viz/service-map-layout";
import type { PanelFragment } from "../../../panels/fragment";
import { fixture } from "../../tests/fixtures";

const workers = vi.hoisted(() => ({ create: vi.fn(), post: vi.fn(), terminate: vi.fn(), current: null as unknown as Worker }));
vi.mock("../dashboards/viz/service-map.worker?worker&inline", () => ({ default: class {
  onmessage?: Worker["onmessage"]; onerror?: Worker["onerror"];
  constructor() { workers.create(); workers.current = this as unknown as Worker; }
  postMessage(body: unknown) { workers.post(body); }
  terminate() { workers.terminate(); }
} }));
beforeEach(() => {
  vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true); workers.create.mockReset(); workers.post.mockReset(); workers.terminate.mockReset();
});
afterEach(() => { vi.restoreAllMocks(); vi.unstubAllGlobals(); });
function mapFragment(count: number): PanelFragment {
  const f = fixture("service_map");
  f.results[0] = { ...f.results[0], status: "ok", frame: {
    rows: count, columns: ["kind", "service", "health", "spans", "error_rate", "p95_ms"].map(name => ({ name, type: "string", role: "dimension" })),
    values: [Array(count).fill("node"), Array.from({ length: count }, (_, i) => `service_${i}`), Array(count).fill("healthy"), Array(count).fill(10), Array(count).fill(0), Array(count).fill(2)],
  } };
  return f;
}
async function mount(count: number) {
  const fetch = vi.spyOn(globalThis, "fetch");
  const node = document.createElement("div"); document.body.append(node); const root = createRoot(node);
  const client = new QueryClient();
  await act(async () => root.render(<MantineProvider><QueryClientProvider client={client}><FragmentView fragment={mapFragment(count)} dark={false} drillClient={{ exemplars: vi.fn(), trace: vi.fn() }} onQuery={vi.fn()} /></QueryClientProvider></MantineProvider>));
  return { node, fetch, async cleanup() { await act(async () => root.unmount()); node.remove(); client.clear(); } };
}
it.each([61, 400])("lays out %s services off-thread in the shared app renderer without host fetch", async count => {
  const view = await mount(count);
  try {
    expect(workers.create).toHaveBeenCalledOnce(); expect(workers.post).toHaveBeenCalledOnce();
    const { model, size, widths } = workers.post.mock.lastCall![0];
    expect(model.nodes).toHaveLength(count);
    await act(async () => workers.current.onmessage?.({ data: layoutServiceMapRaw(model, size, widths) } as MessageEvent));
    expect(view.node.querySelectorAll('[data-service-node]')).toHaveLength(count);
    expect(view.fetch).not.toHaveBeenCalled();
  } finally { await view.cleanup(); }
  expect(workers.terminate).toHaveBeenCalledOnce();
});
it.each(["initialization", "post", "layout"])("shows a visible service-map %s error instead of a forbidden fallback", async kind => {
  if (kind === "initialization") workers.create.mockImplementation(() => { throw new Error("CSP blocked"); });
  if (kind === "post") workers.post.mockImplementation(() => { throw new Error("Layout unavailable"); });
  const view = await mount(61);
  try {
    if (kind === "layout") await act(async () => workers.current.onerror?.(new Event("error") as ErrorEvent));
    expect(view.node.textContent).toContain("Service layout unavailable"); expect(view.fetch).not.toHaveBeenCalled();
  } finally { await view.cleanup(); }
});
