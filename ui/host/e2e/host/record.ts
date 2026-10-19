import { Client, StreamableHTTPClientTransport } from "@modelcontextprotocol/client";
import { readFile, writeFile } from "node:fs/promises";
import setup from "../support/global-setup";

if (process.env.FANOUT_LANDING_BASE_URL) throw new Error("Recordings require the disposable synthetic seed");
const cleanup = await setup();
const client = new Client({ name: "fanout-e2e-recorder", version: "1.0.0" }, {
  supportedProtocolVersions: ["2026-07-28"], versionNegotiation: { mode: { pin: "2026-07-28" } },
});
try {
  const state = JSON.parse(await readFile(process.env.FANOUT_E2E_STORAGE_STATE!, "utf8"));
  const cookie = state.cookies.map((c: { name: string; value: string }) => `${c.name}=${c.value}`).join("; ");
  await client.connect(new StreamableHTTPClientTransport(new URL("/api/mcp", process.env.FANOUT_E2E_BASE_URL), {
    fetch: (url, init) => fetch(url, { ...init, headers: { ...Object.fromEntries(new Headers(init?.headers)), Cookie: cookie, "Fanout-Request": "1" } }),
  }));
  const time = JSON.parse(process.env.FANOUT_E2E_PERFORMANCE_WINDOW!);
  const window = { from: time.from, to: time.to };
  const panel = { id: "custom", title: "Seeded span count", viz: "stat", query: { from: "spans", measures: ["count()"] } };
  const inputs: Record<string, { name: string; arguments: Record<string, unknown> }> = {
    overview: { name: "get_observability_overview", arguments: window },
    topology: { name: "get_service_topology", arguments: window },
    performance: { name: "get_service_performance", arguments: { ...window, service: "checkout" } },
    trace: { name: "inspect_trace", arguments: window },
    logs: { name: "search_logs", arguments: { ...window, limit: 20 } },
    custom: { name: "query_telemetry", arguments: { panel, time } },
    no_data: { name: "search_logs", arguments: { ...window, search: "no-synthetic-event-matches-this" } },
    error: { name: "get_service_performance", arguments: { ...window, service: "missing-synthetic-service" } },
  };
  const recordings: Record<string, unknown> = {};
  for (const [scenario, input] of Object.entries(inputs)) {
    const result = await client.callTool(input);
    if (scenario === "no_data" || scenario === "error") {
      if (!result.isError) throw new Error(`${scenario} did not produce a tool error`);
    } else if (result.isError || !result.structuredContent) throw new Error(`${scenario} did not produce a fragment`);
    const helpers = [];
    if (!result.isError) {
      const fragment = result.structuredContent as {
        dashboard: { panels: { id: string; drill?: string; viz: string }[]; time: unknown }; vars?: unknown;
        results: { id: string; from_ms: number; to_ms: number; frame: { columns: { name: string; role: string }[]; values: unknown[][] } }[];
      };
      const shared = { dashboard: fragment.dashboard, time: fragment.dashboard.time, vars: fragment.vars ?? {} };
      const requests: { name: string; arguments: Record<string, unknown> }[] = [
        { name: "query_panel_fragment", arguments: shared },
        { name: "resolve_panel_variables", arguments: shared },
      ];
      const drill = fragment.dashboard.panels.find(panel => panel.viz === "table") ?? fragment.dashboard.panels.find(panel => panel.drill);
      const rows = fragment.results.find(result => result.id === drill?.id);
      if (drill && rows) {
        const from = new Date(rows.from_ms).toISOString(), to = new Date(rows.to_ms).toISOString();
        const dimensions = drill.viz === "table" ? Object.fromEntries(rows.frame.columns.flatMap((column, index) => column.role === "dimension" ? [[column.name, rows.frame.values[index][0]]] : [])) : {};
        requests.push({ name: "list_panel_exemplars", arguments: { ...shared, time: { from, to, refresh: "off" }, panel_id: drill.id, kind: "traces", from, to, dimensions } });
        if (drill.viz === "traces") {
          const value = (name: string) => rows.frame.values[rows.frame.columns.findIndex(column => column.name === name)][0];
          requests.push({ name: "inspect_trace", arguments: { trace_id: value("trace_id"), namespace: value("namespace"), from, to, limit: 200 } });
        }
      }
      for (const request of requests) {
        const result = await client.callTool(request);
        if (result.isError || !result.structuredContent) throw new Error(`${scenario} ${request.name} helper failed`);
        helpers.push({ input: request, result });
      }
    }
    recordings[scenario] = { input, result, helpers };
  }
  await writeFile(new URL("../data/mcp.json", import.meta.url), JSON.stringify(recordings, null, 2) + "\n");
  console.log("Recorded eight MCP scenarios from the disposable synthetic seed.");
} finally { try { await client.close(); } finally { await cleanup?.(); } }
