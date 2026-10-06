import { act } from "react";
import { createRoot } from "react-dom/client";
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { afterEach, beforeEach, expect, it, vi } from "vitest";
import { usePanelResults } from "./use-panel-results";
import type { DashboardSpec } from "../../../panels/types";
const wire = vi.hoisted(() => ({ panels: vi.fn(), annotations: vi.fn() }));
vi.mock("./api", async (importOriginal) => ({ ...await importOriginal<typeof import("./api")>(), queryPanels: wire.panels, queryAnnotations: wire.annotations }));
beforeEach(() => vi.stubGlobal("IS_REACT_ACT_ENVIRONMENT", true));
afterEach(() => vi.unstubAllGlobals());
it("S8 integrates one annotation request into every 20-panel manual refresh", async () => {
  const spec: DashboardSpec = { version: 1, name: "S8", time: { range: "1h" }, panels: Array.from({ length: 20 }, (_, i) => ({ id: `p_${i}`, title: `P ${i}`, viz: "timeseries", query: { from: "spans", measures: ["count()"] } })) };
  wire.panels.mockResolvedValue(spec.panels.map(p => ({ id: p.id, status: "ok", elapsed_ms: 1, from_ms: 1000, to_ms: 2000, frame: { columns: [{ name: "time", type: "time", role: "time" }, { name: "count", type: "number", role: "measure" }], values: [[1000], [1]], rows: 1 } }))); wire.annotations.mockResolvedValue({ deploys: [], anomalies: [] });
  const client = new QueryClient({ defaultOptions: { queries: { retry: false } } }); const node = document.createElement("div"); document.body.append(node); const root = createRoot(node); let current: ReturnType<typeof usePanelResults>;
  function Host() { current = usePanelResults({ dashboardId: "d", version: 1, spec, time: spec.time, vars: {}, compare: false, widths: {}, visible: spec.panels.map(p => p.id), refresh: "off" }); return null; }
  try {
    await act(async () => { root.render(<QueryClientProvider client={client}><Host /></QueryClientProvider>); });
    async function settled(expected: number) {
      const until = Date.now() + 5000;
      // HTTP mocks can finish before React Query's scheduled notification.
      do { await act(async () => { await new Promise(resolve => setTimeout(resolve, 10)); }); }
      while ((wire.annotations.mock.calls.length < expected || current!.fetching) && Date.now() < until);
      expect(current!.fetching).toBe(false); expect(wire.panels).toHaveBeenCalledTimes(expected); expect(wire.annotations).toHaveBeenCalledTimes(expected); expect(current!.results.size).toBe(20);
    }
    await settled(1); for (let i = 2; i <= 4; i++) { await act(async () => current!.refetch()); await settled(i); }
    wire.annotations.mockRejectedValueOnce(new Error("Unavailable")); await act(async () => current!.refetch()); await settled(5); expect(current!.annotationError).toBe("Unavailable"); expect(current!.results.get("p_0")?.status).toBe("ok");
  } finally {
    await act(async () => root.unmount()); client.clear(); node.remove();
  }
});
