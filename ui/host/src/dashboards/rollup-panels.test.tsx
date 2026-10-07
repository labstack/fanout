import { MantineProvider } from "@mantine/core";
import { act } from "react";
import { createRoot } from "react-dom/client";
import { afterEach, describe, expect, it, vi } from "vitest";
import type { Frame, Panel, PanelResult } from "../../../panels/types";
import { serviceMapModel } from "../../../panels/rollups";
import { ok, warn, bad } from "../../../tokens";

vi.mock("./echart-canvas", () => ({ EChartCanvas: ({ label }: { label: string }) => <div role="img" aria-label={label} /> }));
import { Viz } from "./viz";

const columns = ["kind", "service", "caller", "callee", "edge_type", "calls", "average_ms", "error_rate", "health", "p95_ms", "spans"];
const rows = [
  ["node", "payment", "", "", "", null, null, 0, "healthy", 40, 20],
  ["node", "checkout", "", "", "", null, null, 10, "unhealthy", 100, 10],
  ["edge", "", "checkout", "payment", "call", 10, 20, 10, "", null, null],
  ["edge", "", "payment", "sink", "call", 10000, 30, 0, "", null, null],
];
const mapFrame: Frame = { rows: rows.length, columns: columns.map((name) => ({ name, type: "string", role: "dimension" })), values: columns.map((_, i) => rows.map((r) => r[i])) };
const cleanups: (() => void)[] = [];
afterEach(async () => { await act(async () => cleanups.splice(0).forEach((f) => f())); document.body.innerHTML = ""; });
async function render(panel: Panel, result: PanelResult) {
  const host = document.createElement("div"); document.body.append(host);
  const root = createRoot(host); cleanups.push(() => root.unmount());
  await act(async () => root.render(<MantineProvider theme={{ colors: { ok: [...ok], warn: [...warn], bad: [...bad] } }}><Viz panel={panel} result={result} dark={false} height={200} group="test" /></MantineProvider>));
  return host;
}

describe("Task 6 rollup panels", () => {
  it("retains node metrics and percentage edge health in the graph model", () => {
    const graph = serviceMapModel(mapFrame);
    expect(graph.nodes.map(n => n.id)).toEqual(["checkout", "payment", "sink"]);
    expect(graph.nodes[0]).toMatchObject({ health: "unhealthy", p95_ms: 100 });
    expect(graph.nodes[2].health).toBe("unknown");
    expect(graph.edges[0].status).toBe("bad"); expect(graph.edges[1].status).toBeNull();
  });
  it("renders the overview tiles, percent trend, operations and shaped distribution from the frame", async () => {
    const frame: Frame = { columns: [], values: [], rows: 3, health: { health: "unhealthy", counts: { healthy: 1, degraded: 1, unhealthy: 1 }, total_spans: 1234, error_rate: 10, service_count: 3, error_trend: [0, 10] } };
    const host = await render({ id: "h", title: "Health", viz: "health" }, { id: "h", status: "ok", frame, elapsed_ms: 1 });
    expect(host.textContent).toContain("Unhealthy");
    expect(host.textContent).toContain("10.00%");
    expect(host.textContent).toContain("1,234 operations");
    expect(host.textContent).toContain("3 services");
    expect(host.querySelector('[aria-label="Service health distribution"]')).not.toBeNull();
    expect(host.querySelector('[aria-label="Error rate trend"]')).not.toBeNull();
    expect(host.querySelector("svg circle")).not.toBeNull();
    expect(host.querySelector("svg rect")).not.toBeNull();
    expect(host.querySelector("svg polygon")).not.toBeNull();
    expect(host.querySelector("table")).toBeNull();
  });
  it("never reports healthy or operations for an unknown window", async () => {
    const frame: Frame = { columns: [], values: [], rows: 0, health: { health: "unknown", counts: { healthy: 0, degraded: 0, unhealthy: 0 }, total_spans: 0, error_rate: 0, service_count: 0, error_trend: [] } };
    const host = await render({ id: "h", title: "Health", viz: "health" }, { id: "h", status: "empty", frame, elapsed_ms: 1 });
    expect(host.textContent).toContain("No data");
    expect(host.textContent).not.toContain("Healthy");
    expect(host.textContent).not.toContain("operations");
  });
  it("renders the map through its accessible dependency region", async () => {
    const host = await render({ id: "m", title: "Map", viz: "service_map" }, { id: "m", status: "ok", frame: mapFrame, elapsed_ms: 1 });
    expect(host.querySelector('[aria-label^="Map: service dependency graph"]')).not.toBeNull();
  });
});
